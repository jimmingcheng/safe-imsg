package backend

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jimmingcheng/safe-imsg/internal/config"
	"github.com/jimmingcheng/safe-imsg/internal/securefile"
)

const (
	maxBackendLineBytes   = 2 << 20
	maxBackendOutputBytes = 64 << 20
)

var (
	ErrFailed   = errors.New("imsg backend failed")
	ErrOverflow = errors.New("incremental scan overflow")
)

type RawChat struct {
	ID           int64     `json:"id"`
	Identifier   string    `json:"identifier"`
	GUID         string    `json:"guid"`
	Service      string    `json:"service"`
	AccountID    *string   `json:"account_id"`
	IsGroup      *bool     `json:"is_group"`
	Participants *[]string `json:"participants"`
}

type RawMessage struct {
	ID             int64     `json:"id"`
	ChatID         int64     `json:"chat_id"`
	GUID           string    `json:"guid"`
	Sender         string    `json:"sender"`
	IsFromMe       bool      `json:"is_from_me"`
	Text           string    `json:"text"`
	CreatedAt      string    `json:"created_at"`
	ChatIdentifier string    `json:"chat_identifier"`
	ChatGUID       string    `json:"chat_guid"`
	IsGroup        *bool     `json:"is_group"`
	Participants   *[]string `json:"participants"`
}

func (m RawMessage) Conversation() RawChat {
	return RawChat{ID: m.ChatID, Identifier: m.ChatIdentifier, GUID: m.ChatGUID, IsGroup: m.IsGroup, Participants: m.Participants}
}

type Service interface {
	Generation() string
	ListChats(context.Context, int) ([]RawChat, error)
	Chat(context.Context, int64) (RawChat, error)
	History(context.Context, int64, int) ([]RawMessage, error)
	Collect(context.Context, int64, int) ([]RawMessage, error)
}

type Process struct {
	path       string
	database   string
	generation string
	identity   string
	timeout    time.Duration
}

func New(cfg config.Config) (*Process, error) {
	if err := securefile.CheckOwnerFile(cfg.BackendPath, true); err != nil {
		return nil, err
	}
	if err := checkVersion(cfg.BackendPath, cfg.BackendVersion); err != nil {
		return nil, err
	}
	if err := securefile.CheckOwnerFile(cfg.DatabasePath, false); err != nil {
		return nil, err
	}
	identity, err := databaseIdentity(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("inspect Messages database: %w", err)
	}
	digest := sha256.Sum256([]byte(cfg.AccountID + "\x00" + cfg.BackendAccountID + "\x00" + cfg.DatabaseGeneration + "\x00" + identity))
	generation := "dbgen_" + base64.RawURLEncoding.EncodeToString(digest[:18])
	return &Process{
		path: cfg.BackendPath, database: cfg.DatabasePath, identity: identity,
		generation: generation, timeout: time.Duration(cfg.BackendTimeoutMillis) * time.Millisecond,
	}, nil
}

func checkVersion(path, expected string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Dir = "/"
	cmd.Stderr = io.Discard
	output := cappedBuffer{max: 256}
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil || ctx.Err() != nil || strings.TrimSpace(output.String()) != expected {
		return fmt.Errorf("backend version does not match audited imsg %s", expected)
	}
	return nil
}

type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	if len(value) > b.max-b.Len() {
		return 0, fmt.Errorf("output limit exceeded")
	}
	return b.Buffer.Write(value)
}

func (p *Process) Generation() string { return p.generation }

func (p *Process) checkIdentity() error {
	current, err := databaseIdentity(p.database)
	if err != nil || current != p.identity {
		return fmt.Errorf("database identity changed: %w", ErrFailed)
	}
	return nil
}

func (p *Process) ListChats(ctx context.Context, limit int) ([]RawChat, error) {
	var rows []RawChat
	err := p.run(ctx, []string{"chats", "--db", p.database, "--limit", strconv.Itoa(limit), "--json"}, limit, func(line []byte) error {
		var row RawChat
		if err := decodeLine(line, &row); err != nil {
			return err
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}

func (p *Process) Chat(ctx context.Context, chatID int64) (RawChat, error) {
	var rows []RawChat
	err := p.run(ctx, []string{"group", "--db", p.database, "--chat-id", strconv.FormatInt(chatID, 10), "--json"}, 1, func(line []byte) error {
		var row RawChat
		if err := decodeLine(line, &row); err != nil {
			return err
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return RawChat{}, err
	}
	if len(rows) != 1 {
		return RawChat{}, ErrFailed
	}
	return rows[0], nil
}

func (p *Process) History(ctx context.Context, chatID int64, limit int) ([]RawMessage, error) {
	var rows []RawMessage
	err := p.run(ctx, []string{"history", "--db", p.database, "--chat-id", strconv.FormatInt(chatID, 10), "--limit", strconv.Itoa(limit), "--json"}, limit, func(line []byte) error {
		var row RawMessage
		if err := decodeLine(line, &row); err != nil {
			return err
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}

func decodeLine(line []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	if err := dec.Decode(dst); err != nil {
		return ErrFailed
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return ErrFailed
	}
	return nil
}

func (p *Process) run(ctx context.Context, args []string, maxRows int, handle func([]byte) error) error {
	if err := p.checkIdentity(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.path, args...)
	cmd.Dir = "/"
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ErrFailed
	}
	if err := cmd.Start(); err != nil {
		return ErrFailed
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxBackendLineBytes)
	count := 0
	totalBytes := 0
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		count++
		totalBytes += len(scanner.Bytes())
		if count > maxRows || totalBytes > maxBackendOutputBytes {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return ErrFailed
		}
		if err := handle(bytes.Clone(scanner.Bytes())); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return ErrFailed
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil || waitErr != nil || ctx.Err() != nil {
		return ErrFailed
	}
	if err := p.checkIdentity(); err != nil {
		return err
	}
	return nil
}

type collectEvent struct {
	line []byte
	done error
}

// Collect runs imsg's exclusive since-rowid watch just long enough to drain its
// immediate backlog. The caller owns policy filtering and cursor advancement.
func (p *Process) Collect(ctx context.Context, afterRowID int64, scanLimit int) ([]RawMessage, error) {
	if afterRowID <= 0 || scanLimit <= 0 {
		return nil, ErrFailed
	}
	if err := p.checkIdentity(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.path, "watch", "--db", p.database, "--since-rowid", strconv.FormatInt(afterRowID, 10), "--debounce", "0ms", "--json")
	cmd.Dir = "/"
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, ErrFailed
	}
	if err := cmd.Start(); err != nil {
		return nil, ErrFailed
	}
	events := make(chan collectEvent, 1)
	stopEvents := make(chan struct{})
	defer close(stopEvents)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), maxBackendLineBytes)
		for scanner.Scan() {
			line := bytes.Clone(scanner.Bytes())
			if len(bytes.TrimSpace(line)) > 0 {
				select {
				case events <- collectEvent{line: line}:
				case <-stopEvents:
					return
				}
			}
		}
		scanErr := scanner.Err()
		waitErr := cmd.Wait()
		if scanErr != nil {
			select {
			case events <- collectEvent{done: scanErr}:
			case <-stopEvents:
			}
		} else {
			select {
			case events <- collectEvent{done: waitErr}:
			case <-stopEvents:
			}
		}
	}()

	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	rows := make([]RawMessage, 0)
	totalBytes := 0
	for {
		select {
		case event := <-events:
			if event.line != nil {
				totalBytes += len(event.line)
				if totalBytes > maxBackendOutputBytes {
					_ = cmd.Process.Kill()
					return nil, ErrOverflow
				}
				var row RawMessage
				if err := decodeLine(event.line, &row); err != nil {
					_ = cmd.Process.Kill()
					return nil, ErrFailed
				}
				rows = append(rows, row)
				if len(rows) > scanLimit {
					_ = cmd.Process.Kill()
					return nil, ErrOverflow
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(500 * time.Millisecond)
				continue
			}
			if event.done != nil {
				return nil, ErrFailed
			}
			if err := p.checkIdentity(); err != nil {
				return nil, err
			}
			return rows, nil
		case <-timer.C:
			_ = cmd.Process.Kill()
			drainDeadline := time.After(time.Second)
			for {
				select {
				case event := <-events:
					if event.line == nil {
						goto drained
					}
					totalBytes += len(event.line)
					if totalBytes > maxBackendOutputBytes {
						return nil, ErrOverflow
					}
					var row RawMessage
					if err := decodeLine(event.line, &row); err != nil {
						return nil, ErrFailed
					}
					rows = append(rows, row)
					if len(rows) > scanLimit {
						return nil, ErrOverflow
					}
				case <-drainDeadline:
					return nil, ErrFailed
				}
			}
		drained:
			if err := p.checkIdentity(); err != nil {
				return nil, err
			}
			return rows, nil
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, ErrFailed
		}
	}
}
