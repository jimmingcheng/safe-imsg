package rpc

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func replySocket(t *testing.T, response string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := ReadFrame(conn, 4096); err != nil {
			return
		}
		_ = WriteFrame(conn, []byte(response))
	}()
	return path
}

func TestClientPreservesInt64IDs(t *testing.T) {
	path := replySocket(t, `{"v":1,"id":"x","ok":true,"result":{"row_id":9223372036854775807}}`)
	resp, err := Call(context.Background(), path, Request{V: 1, ID: "x", Method: MethodSystemInfo, Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(resp.Result)
	if err != nil || !strings.Contains(string(payload), "9223372036854775807") {
		t.Fatalf("row ID was rounded: %s (%v)", payload, err)
	}
}

func TestClientAcceptsPreRequestAuthError(t *testing.T) {
	path := replySocket(t, `{"v":1,"id":"","ok":false,"error":{"code":"unauthorized_peer","message":"peer uid is not allowed","retryable":false}}`)
	resp, err := Call(context.Background(), path, Request{V: 1, ID: "x", Params: json.RawMessage(`{}`)})
	if err != nil || resp.Error == nil || resp.Error.Code != "unauthorized_peer" {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
}

func TestClientCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Call(ctx, path, Request{V: 1, ID: "x", Params: json.RawMessage(`{}`)})
		done <- err
	}()
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not close client I/O")
	}
}
