package backend

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jimmingcheng/safe-imsg/internal/config"
)

func makeProcess(t *testing.T, script string) (*Process, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "imsg-fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '0.13.1-safe-imsg.1\\n'; exit 0; fi\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(dir, "chat.db")
	if err := os.WriteFile(database, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := New(config.Config{BackendPath: path, BackendVersion: config.CollectionBackendVersion, DatabasePath: database, AccountID: "acct", BackendAccountID: "imsg-account", DatabaseGeneration: "g", BackendTimeoutMillis: 3000})
	if err != nil {
		t.Fatal(err)
	}
	return p, database
}

func TestCollectionRequiresCheckpointAndSuccessfulExit(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		want         error
	}{
		{"missing checkpoint", `printf '%s\n' '{"kind":"row","row_id":11}'; exit 0`, ErrFailed},
		{"failure after checkpoint", `printf '%s\n' '{"kind":"checkpoint","schema":"safe-imsg.collect.v1","through_row_id":10,"scanned_through_row_id":10,"complete":true}'; exit 1`, ErrFailed},
		{"hung process", `exec sleep 30`, context.DeadlineExceeded},
		{"oversized line", `head -c 2097153 /dev/zero | tr '\000' x; exec sleep 30`, ErrFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := makeProcess(t, tc.script)
			p.timeout = 100 * time.Millisecond
			start := time.Now()
			page, err := p.Collect(context.Background(), 10, 0, 10)
			if !errors.Is(err, tc.want) || page.Rows != nil {
				t.Fatalf("page=%v error=%v, want %v", page, err, tc.want)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("collection failure did not terminate promptly")
			}
		})
	}
}

func TestStreamCloseJoinsAndReapsProcess(t *testing.T) {
	p, _ := makeProcess(t, `printf '%s\n' '{"id":11}' '{"id":12}'; sleep 30`)
	s, err := startStream(context.Background(), p.path, []string{"watch"})
	if err != nil {
		t.Fatal(err)
	}
	// The reader is blocked delivering the second row when close is called.
	<-s.events
	s.close()
	if s.cmd.ProcessState == nil {
		t.Fatal("stream returned before reaping its child")
	}
}

func TestBackendCancellationClosesInheritedPipes(t *testing.T) {
	p, _ := makeProcess(t, `sleep 30 & exit 0`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := p.ListChats(ctx, 1); !errors.Is(err, ErrFailed) {
		t.Fatalf("error=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("inherited stdout kept the request alive")
	}
}

func TestBackendTimeoutPreservesCause(t *testing.T) {
	for _, operation := range []string{"history", "collect"} {
		t.Run(operation, func(t *testing.T) {
			p, _ := makeProcess(t, `exec sleep 30`)
			p.timeout = 50 * time.Millisecond
			start := time.Now()
			var err error
			if operation == "history" {
				_, err = p.History(context.Background(), 1, 1)
			} else {
				_, err = p.Collect(context.Background(), 1, 0, 1)
			}
			if !errors.Is(err, ErrFailed) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout lost its cause: %v", err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("timed-out backend did not terminate promptly")
			}
		})
	}
}

func TestVersionBufferCannotBypassLimitThroughReadFrom(t *testing.T) {
	output := cappedBuffer{max: 16}
	_, err := io.Copy(&output, io.LimitReader(bytes.NewBufferString(strings.Repeat("x", 256)), 256))
	if err == nil || len(output.String()) > 16 {
		t.Fatal("version output exceeded bound")
	}
}

func TestBackendArgumentConstructionAndNarrowDecode(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("ARGS_LOG", logPath)
	p, database := makeProcess(t, `
printf '%s\n' "$@" > "$ARGS_LOG"
case "$1" in
chats) printf '%s\n' '{"id":7,"identifier":"+14155550100","guid":"iMessage;-;+14155550100","service":"iMessage","account_id":"imsg-account","is_group":false,"participants":["+14155550100"],"display_name":"PRIVATE"}' ;;
group) printf '%s\n' '{"id":7,"identifier":"+14155550100","guid":"iMessage;-;+14155550100","service":"iMessage","account_id":"imsg-account","is_group":false,"participants":["+14155550100"]}' ;;
history) printf '%s\n' '{"id":9,"chat_id":7,"guid":"m9","sender":"+14155550100","is_from_me":false,"text":"hello","created_at":"2026-09-09T00:00:00Z","chat_identifier":"+14155550100","chat_guid":"iMessage;-;+14155550100","is_group":false,"participants":["+14155550100"],"reply_to_text":"PRIVATE","attachments":[{"filename":"PRIVATE"}]}' ;;
esac
`)
	chats, err := p.ListChats(context.Background(), 3)
	if err != nil || len(chats) != 1 {
		t.Fatalf("ListChats: %#v, %v", chats, err)
	}
	args, _ := os.ReadFile(logPath)
	want := []string{"chats", "--db", database, "--limit", "3", "--json"}
	if got := strings.Fields(string(args)); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if _, err := p.Chat(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	args, _ = os.ReadFile(logPath)
	want = []string{"group", "--db", database, "--chat-id", "7", "--json"}
	if got := strings.Fields(string(args)); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	messages, err := p.History(context.Background(), 7, 4)
	if err != nil || len(messages) != 1 || messages[0].Text != "hello" {
		t.Fatalf("History: %#v, %v", messages, err)
	}
	args, _ = os.ReadFile(logPath)
	want = []string{"history", "--db", database, "--chat-id", "7", "--limit", "4", "--json"}
	if got := strings.Fields(string(args)); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBackendErrorsDoNotIncludeStderr(t *testing.T) {
	p, _ := makeProcess(t, `printf 'TOP SECRET backend error\n' >&2; exit 1`)
	_, err := p.ListChats(context.Background(), 2)
	if !errors.Is(err, ErrFailed) || strings.Contains(err.Error(), "TOP SECRET") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestCollectArgumentConstructionAndCheckpoint(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "collect-args")
	t.Setenv("ARGS_LOG", logPath)
	p, _ := makeProcess(t, `
printf '%s\n' "$@" > "$ARGS_LOG"
if [ "$1" = collect ]; then
  printf '%s\n' '{"kind":"row","row_id":11}'
  printf '%s\n' '{"kind":"row","row_id":12}'
  printf '%s\n' '{"kind":"checkpoint","schema":"safe-imsg.collect.v1","through_row_id":20,"scanned_through_row_id":12,"complete":false}'
fi
`)
	page, err := p.Collect(context.Background(), 10, 20, 2)
	if err != nil || len(page.Rows) != 2 || page.Rows[0].RowID != 11 || page.ScannedThroughRowID != 12 || page.Complete {
		t.Fatalf("Collect: %#v, %v", page, err)
	}
	args, _ := os.ReadFile(logPath)
	want := []string{"collect", "--db", p.database, "--since-rowid", "10", "--limit", "2", "--account-id", "imsg-account", "--json", "--through-rowid", "20"}
	if got := strings.Fields(string(args)); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if _, err := p.Collect(context.Background(), 10, 20, 1); !errors.Is(err, ErrFailed) {
		t.Fatalf("excessive backend page error = %v", err)
	}
}

func TestDatabaseReplacementFailsClosed(t *testing.T) {
	p, database := makeProcess(t, `exit 0`)
	replacement := database + ".new"
	if err := os.WriteFile(replacement, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, database); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListChats(context.Background(), 1); !errors.Is(err, ErrFailed) {
		t.Fatalf("replacement error = %v", err)
	}
}
