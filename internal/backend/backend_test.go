package backend

import (
	"context"
	"errors"
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
