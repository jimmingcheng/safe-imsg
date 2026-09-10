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
	if err := os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '0.13.1\\n'; exit 0; fi\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(dir, "chat.db")
	if err := os.WriteFile(database, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := New(config.Config{BackendPath: path, BackendVersion: "0.13.1", DatabasePath: database, AccountID: "acct", BackendAccountID: "imsg-account", DatabaseGeneration: "g", BackendTimeoutMillis: 3000})
	if err != nil {
		t.Fatal(err)
	}
	return p, database
}

func TestWatchFailureAndNoProgress(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		want         error
	}{
		{"clean exit", `printf '%s\n' '{"id":11}'; exit 0`, ErrFailed},
		{"failure after row", `printf '%s\n' '{"id":11}'; exit 1`, ErrFailed},
		{"quiet watch", `exec sleep 30`, ErrIncomplete},
		{"oversized line", `head -c 2097153 /dev/zero | tr '\000' x; exec sleep 30`, ErrFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := makeProcess(t, tc.script)
			start := time.Now()
			rows, err := p.Collect(context.Background(), 10, 10)
			if !errors.Is(err, tc.want) || rows != nil {
				t.Fatalf("rows=%v error=%v, want %v", rows, err, tc.want)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("watch failure did not terminate promptly")
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

func TestCollectOrderingAndOverflow(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "watch-args")
	t.Setenv("ARGS_LOG", logPath)
	p, _ := makeProcess(t, `
printf '%s\n' "$@" > "$ARGS_LOG"
if [ "$1" = watch ]; then
  printf '%s\n' '{"id":11,"chat_id":7,"guid":"m11"}'
  printf '%s\n' '{"id":12,"chat_id":7,"guid":"m12"}'
  exec sleep 10
fi
`)
	p.timeout = 3 * time.Second
	rows, err := p.Collect(context.Background(), 10, 2)
	if err != nil || len(rows) != 2 || rows[0].ID != 11 || rows[1].ID != 12 {
		t.Fatalf("Collect: %#v, %v", rows, err)
	}
	args, _ := os.ReadFile(logPath)
	want := []string{"watch", "--db", p.database, "--since-rowid", "10", "--debounce", "0ms", "--json"}
	if got := strings.Fields(string(args)); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if _, err := p.Collect(context.Background(), 10, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow error = %v", err)
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
