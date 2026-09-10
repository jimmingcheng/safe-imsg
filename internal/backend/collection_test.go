package backend

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestCollectionRejectsInvalidProtocol(t *testing.T) {
	checkpoint := `{"kind":"checkpoint","schema":"safe-imsg.collect.v1","through_row_id":20,"scanned_through_row_id":20,"complete":true}`
	for name, output := range map[string]string{
		"missing checkpoint":     `{"kind":"row","row_id":11}`,
		"duplicate checkpoint":   checkpoint + "\n" + checkpoint,
		"row after checkpoint":   checkpoint + "\n" + `{"kind":"row","row_id":11}`,
		"out of order":           `{"kind":"row","row_id":12}` + "\n" + `{"kind":"row","row_id":11}` + "\n" + checkpoint,
		"before cursor":          `{"kind":"row","row_id":10}` + "\n" + checkpoint,
		"past upper bound":       `{"kind":"row","row_id":21}` + "\n" + checkpoint,
		"invented progress":      `{"kind":"checkpoint","schema":"safe-imsg.collect.v1","through_row_id":20,"scanned_through_row_id":15,"complete":false}`,
		"contradictory complete": `{"kind":"checkpoint","schema":"safe-imsg.collect.v1","through_row_id":20,"scanned_through_row_id":15,"complete":true}`,
		"changed boundary":       `{"kind":"checkpoint","schema":"safe-imsg.collect.v1","through_row_id":21,"scanned_through_row_id":21,"complete":true}`,
		"missing complete":       `{"kind":"checkpoint","schema":"safe-imsg.collect.v1","through_row_id":20,"scanned_through_row_id":20}`,
		"unknown field":          `{"kind":"row","row_id":11,"attachments":["private"]}` + "\n" + checkpoint,
		"message without chat":   `{"kind":"row","row_id":11,"message":{"id":11}}` + "\n" + checkpoint,
		"wrong row identity":     `{"kind":"row","row_id":11,"message":{"id":12,"chat_id":1},"chat":{"id":1}}` + "\n" + checkpoint,
		"wrong chat identity":    `{"kind":"row","row_id":11,"message":{"id":11,"chat_id":1},"chat":{"id":2}}` + "\n" + checkpoint,
	} {
		t.Run(name, func(t *testing.T) {
			p, _ := makeProcess(t, fmt.Sprintf("printf '%%s\\n' '%s'\n", output))
			page, err := p.Collect(context.Background(), 10, 20, 2, "")
			if !errors.Is(err, ErrFailed) || page.Rows != nil {
				t.Fatalf("page=%+v err=%v", page, err)
			}
		})
	}
}

func TestCollectionEmptyRangeAndLargeInteger(t *testing.T) {
	for _, position := range []int64{0, 9007199254740993} {
		p, _ := makeProcess(t, fmt.Sprintf(`printf '%%s\n' '{"kind":"checkpoint","schema":"safe-imsg.collect.v1","through_row_id":%d,"scanned_through_row_id":%d,"complete":true}'`, position, position))
		page, err := p.Collect(context.Background(), position, 0, 1, "")
		if err != nil || !page.Complete || len(page.Rows) != 0 || page.ScannedThroughRowID != position {
			t.Fatalf("page=%+v err=%v", page, err)
		}
	}
}

func TestUnpatchedBackendDoesNotFallBackToWatch(t *testing.T) {
	p, _ := makeProcess(t, "exit 99")
	p.boundedCollection = false
	if _, err := p.Collect(context.Background(), 1, 0, 1, ""); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}
