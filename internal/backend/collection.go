package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"time"
)

const CollectionProtocol = "bounded_rows_v2"

type CollectionRow struct {
	RowID   int64
	Message *RawMessage
	Chat    *RawChat
}

type CollectionPage struct {
	Rows                []CollectionRow
	ThroughRowID        int64
	ScannedThroughRowID int64
	Complete            bool
}

type collectionEvent struct {
	Kind                string      `json:"kind"`
	RowID               *int64      `json:"row_id"`
	Message             *RawMessage `json:"message"`
	Chat                *RawChat    `json:"chat"`
	Schema              string      `json:"schema"`
	ThroughRowID        *int64      `json:"through_row_id"`
	ScannedThroughRowID *int64      `json:"scanned_through_row_id"`
	Complete            *bool       `json:"complete"`
}

// Collect consumes a finite backend page and requires both a validated final
// checkpoint and a successful process exit. A watch window is never a fallback.
// throughRowID == 0 captures a new boundary; nonzero pins a pending cycle.
func (p *Process) Collect(ctx context.Context, afterRowID, throughRowID int64, limit int, notBefore string) (CollectionPage, error) {
	if !p.boundedCollection {
		return CollectionPage{}, ErrUnsupported
	}
	if afterRowID < 0 || throughRowID < 0 || (throughRowID != 0 && throughRowID < afterRowID) || limit < 1 || limit > 1000 || (notBefore != "" && (afterRowID != 0 || throughRowID != 0)) {
		return CollectionPage{}, ErrFailed
	}
	if notBefore != "" {
		if _, err := time.Parse(time.RFC3339, notBefore); err != nil {
			return CollectionPage{}, ErrFailed
		}
	}
	args := []string{"collect", "--db", p.database, "--since-rowid", strconv.FormatInt(afterRowID, 10), "--limit", strconv.Itoa(limit), "--account-id", p.accountID, "--json"}
	if throughRowID != 0 {
		args = append(args, "--through-rowid", strconv.FormatInt(throughRowID, 10))
	}
	if notBefore != "" {
		args = append(args, "--not-before", notBefore)
	}
	page := CollectionPage{}
	checkpoint := false
	err := p.run(ctx, args, limit+1, func(line []byte) error {
		var event collectionEvent
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			return ErrFailed
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF || checkpoint {
			return ErrFailed
		}
		switch event.Kind {
		case "row":
			if event.RowID == nil || event.Schema != "" || event.ThroughRowID != nil || event.ScannedThroughRowID != nil || event.Complete != nil {
				return ErrFailed
			}
			page.Rows = append(page.Rows, CollectionRow{RowID: *event.RowID, Message: event.Message, Chat: event.Chat})
		case "checkpoint":
			if event.RowID != nil || event.Message != nil || event.Chat != nil || event.Schema != "safe-imsg.collect.v1" || event.ThroughRowID == nil || event.ScannedThroughRowID == nil || event.Complete == nil {
				return ErrFailed
			}
			page.ThroughRowID, page.ScannedThroughRowID, page.Complete = *event.ThroughRowID, *event.ScannedThroughRowID, *event.Complete
			checkpoint = true
		default:
			return ErrFailed
		}
		return nil
	})
	if err != nil {
		return CollectionPage{}, err
	}
	if !checkpoint || ValidateCollectionPage(page, afterRowID, throughRowID, limit) != nil {
		return CollectionPage{}, ErrFailed
	}
	return page, nil
}

// ValidateCollectionPage also protects the broker when using an injected backend.
func ValidateCollectionPage(page CollectionPage, after, through int64, limit int) error {
	if page.ThroughRowID < after || (through != 0 && page.ThroughRowID != through) || len(page.Rows) > limit {
		return ErrFailed
	}
	last := after
	for _, row := range page.Rows {
		if row.RowID <= last || row.RowID > page.ThroughRowID || (row.Message == nil) != (row.Chat == nil) {
			return ErrFailed
		}
		if row.Message != nil && (row.Message.ID != row.RowID || row.Chat.ID != row.Message.ChatID) {
			return ErrFailed
		}
		last = row.RowID
	}
	if page.Complete {
		if page.ScannedThroughRowID != page.ThroughRowID {
			return ErrFailed
		}
	} else if len(page.Rows) != limit || last <= after || last >= page.ThroughRowID || page.ScannedThroughRowID != last {
		return ErrFailed
	}
	return nil
}
