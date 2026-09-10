package broker

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jimmingcheng/safe-imsg/internal/backend"
	"github.com/jimmingcheng/safe-imsg/internal/config"
	"github.com/jimmingcheng/safe-imsg/internal/policy"
	"github.com/jimmingcheng/safe-imsg/internal/rpc"
)

type fakeBackend struct {
	generation    string
	chats         []backend.RawChat
	history       []backend.RawMessage
	collected     []backend.RawMessage
	err           error
	historyFn     func() []backend.RawMessage
	historyLimits []int
	after         int64
}

func (f *fakeBackend) Generation() string { return f.generation }
func (f *fakeBackend) ListChats(context.Context, int) ([]backend.RawChat, error) {
	return f.chats, f.err
}
func (f *fakeBackend) Chat(_ context.Context, id int64) (backend.RawChat, error) {
	if f.err != nil {
		return backend.RawChat{}, f.err
	}
	for _, chat := range f.chats {
		if chat.ID == id {
			return chat, nil
		}
	}
	return backend.RawChat{}, backend.ErrFailed
}
func (f *fakeBackend) History(_ context.Context, _ int64, limit int) ([]backend.RawMessage, error) {
	f.historyLimits = append(f.historyLimits, limit)
	if f.err != nil {
		return nil, f.err
	}
	rows := f.history
	if f.historyFn != nil {
		rows = f.historyFn()
	}
	return rows[:min(limit, len(rows))], nil
}
func (f *fakeBackend) Collect(_ context.Context, after int64, _ int) ([]backend.RawMessage, error) {
	f.after = after
	if f.err != nil {
		return nil, f.err
	}
	return f.collected, nil
}

func boolp(value bool) *bool              { return &value }
func stringsp(values ...string) *[]string { return &values }
func stringp(value string) *string        { return &value }

func dm(id int64, identity string) backend.RawChat {
	return backend.RawChat{ID: id, Identifier: identity, GUID: "iMessage;-;" + identity, Service: "iMessage", AccountID: stringp("imsg-account"), IsGroup: boolp(false), Participants: stringsp(identity)}
}

func group(id int64, guid string, participants ...string) backend.RawChat {
	return backend.RawChat{ID: id, Identifier: strings.Split(guid, ";")[2], GUID: guid, Service: "iMessage", AccountID: stringp("imsg-account"), IsGroup: boolp(true), Participants: &participants}
}

func message(id int64, chat backend.RawChat, sender, text string) backend.RawMessage {
	return backend.RawMessage{ID: id, ChatID: chat.ID, GUID: "message-" + strconv.FormatInt(id, 10), Sender: sender, Text: text,
		IsFromMe:  boolp(false),
		CreatedAt: "2026-09-09T00:00:00Z", ChatIdentifier: chat.Identifier, ChatGUID: chat.GUID,
		IsGroup: chat.IsGroup, Participants: chat.Participants}
}

func writePolicy(t *testing.T, path string, allowed []string, excluded []string) {
	t.Helper()
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"messages": map[string]any{
		"owner_aliases": []string{"owner@example.com"}, "allowed_direct": allowed,
		"excluded_conversations": excluded,
	}}
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func testConfig(t *testing.T, policyPath string) config.Config {
	t.Helper()
	uid := uint32(os.Geteuid()) + 1
	if uid == 0 {
		uid = 1
	}
	socketDir := t.TempDir()
	if err := os.Chmod(socketDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return config.Config{
		Instance: "test", AccountID: "acct", ClientUID: uid,
		SocketPath: filepath.Join(socketDir, "broker.sock"), SocketMode: "0600",
		BackendPath: "/fake/imsg", BackendVersion: "0.13.1", DatabasePath: "/fake/chat.db", BackendAccountID: "imsg-account", DatabaseGeneration: "configured",
		PolicyPath: policyPath, MaxResults: 10, MaxChatScan: 20, MaxMessageScan: 20,
		MaxCollectionScan: 20, BackendTimeoutMillis: 2000,
	}
}

func testServer(t *testing.T, fake *fakeBackend, policyPath string) *Server {
	t.Helper()
	cfg := testConfig(t, policyPath)
	server, err := NewWithDeps(cfg, Dependencies{Backend: fake, LoadPolicy: policy.Load, PeerUID: func(*net.UnixConn) (uint32, error) { return cfg.ClientUID, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func request(t *testing.T, method string, params any) rpc.Request {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return rpc.Request{V: rpc.Version1, ID: "test", Method: method, Params: payload}
}

func TestListChatsPolicyMatrix(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, []string{"group;+;excluded"})
	allowed := dm(1, "+14155550100")
	denied := dm(2, "+14155550101")
	owner := dm(3, "owner@example.com")
	verifiedGroup := group(4, "group;+;allowed", "+14155550999", "stranger@example.net")
	excludedGroup := group(5, "group;+;excluded", "+14155550999", "stranger@example.net")
	missing := dm(6, "+14155550100")
	missing.IsGroup = nil
	contradictory := dm(7, "+14155550100")
	contradictory.IsGroup = boolp(true)
	foreignAccount := dm(8, "+14155550100")
	foreignAccount.AccountID = stringp("other-imsg-account")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{allowed, denied, owner, verifiedGroup, excludedGroup, missing, contradictory, foreignAccount}}
	resp := testServer(t, fake, policyPath).dispatch(context.Background(), request(t, rpc.MethodListChats, rpc.ListChatsParams{Limit: 10}))
	if !resp.OK {
		t.Fatalf("response = %#v", resp)
	}
	result := resp.Result.(rpc.ListChatsResult)
	if len(result.Chats) != 2 || result.Chats[0].ChatID != 1 || result.Chats[1].ChatID != 4 {
		t.Fatalf("visible chats = %#v", result.Chats)
	}
	data, _ := json.Marshal(resp)
	for _, forbidden := range []string{"display_name", "contact_name", "last_message_at", "unread_count"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("response leaks %s: %s", forbidden, data)
		}
	}
}

func TestHistorySuppressesSecretsAndPreservesNewestFirst(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	newest := message(30, chat, "+14155550100", "ordinary newest")
	secret := message(29, chat, "+14155550100", "Your verification code is 123456")
	older := message(20, chat, "+14155550100", "ordinary older")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat}, history: []backend.RawMessage{newest, secret, older}}
	server := testServer(t, fake, policyPath)
	resp := server.dispatch(context.Background(), request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: "gen", Limit: 10}))
	if !resp.OK {
		t.Fatalf("response = %#v", resp)
	}
	result := resp.Result.(rpc.HistoryResult)
	if len(result.Messages) != 2 || result.Messages[0].RowID != 30 || result.Messages[1].RowID != 20 {
		t.Fatalf("messages = %#v", result.Messages)
	}
	data, _ := json.Marshal(resp)
	for _, forbidden := range []string{"123456", "attachments", "reply_to", "reaction", "preview", "poll", "sender_name"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("response leaks %q: %s", forbidden, data)
		}
	}
}

func TestHistoryUsesOneRequestSizedScan(t *testing.T) {
	for _, tc := range []struct {
		name                                                 string
		limit, available, suppressed, wantScan, wantMessages int
		complete                                             bool
	}{
		{"one from large history", 1, 1000, 0, 1, 1, false},
		{"exactly full", 2, 2, 0, 2, 2, false},
		{"short history", 3, 2, 0, 3, 2, true},
		{"empty history", 3, 0, 0, 3, 0, true},
		{"filtered full page", 2, 1000, 1, 2, 1, false},
		{"entire page suppressed", 2, 1000, 2, 2, 0, false},
		{"filtered short history", 3, 2, 1, 3, 1, true},
		{"default capped by max results", 0, 1000, 0, 10, 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policyPath := filepath.Join(t.TempDir(), "policy.json")
			writePolicy(t, policyPath, []string{"+14155550100"}, nil)
			chat := dm(1, "+14155550100")
			fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat}}
			for i := 0; i < tc.available; i++ {
				body := "ordinary message"
				if i < tc.suppressed {
					body = "Your verification code is 123456"
				}
				fake.history = append(fake.history, message(int64(2000-i), chat, "+14155550100", body))
			}
			server := testServer(t, fake, policyPath)
			server.cfg.MaxMessageScan = 1000
			resp := server.dispatch(context.Background(), request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: "gen", Limit: tc.limit}))
			if !resp.OK {
				t.Fatalf("response=%+v", resp)
			}
			if len(fake.historyLimits) != 1 || fake.historyLimits[0] != tc.wantScan {
				t.Fatalf("backend history scans=%v, want exactly [%d]", fake.historyLimits, tc.wantScan)
			}
			result := resp.Result.(rpc.HistoryResult)
			if len(result.Messages) != tc.wantMessages || result.ScanComplete != tc.complete {
				t.Fatalf("history=%+v", result)
			}
			for i, msg := range result.Messages {
				if msg.RowID != int64(2000-tc.suppressed-i) {
					t.Fatalf("message %d has unexpected row ID %d", i, msg.RowID)
				}
			}
		})
	}
}

func TestHistoryRejectsContradictoryMessageMetadata(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	bad := message(2, chat, "+14155550100", "hello")
	bad.IsGroup = boolp(true)
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat}, history: []backend.RawMessage{bad}}
	resp := testServer(t, fake, policyPath).dispatch(context.Background(), request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: "gen"}))
	if resp.OK || resp.Error.Code != "backend_invalid" {
		t.Fatalf("response = %#v", resp)
	}
	bad.IsGroup = chat.IsGroup
	bad.IsFromMe = nil
	fake.history = []backend.RawMessage{bad}
	resp = testServer(t, fake, policyPath).dispatch(context.Background(), request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: "gen"}))
	if resp.OK || resp.Error.Code != "backend_invalid" {
		t.Fatalf("missing direction metadata was admitted: %#v", resp)
	}
}

func TestPolicyRevocationDuringRequest(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat}}
	fake.historyFn = func() []backend.RawMessage {
		writePolicy(t, policyPath, nil, nil)
		return []backend.RawMessage{message(2, chat, "+14155550100", "must not escape")}
	}
	resp := testServer(t, fake, policyPath).dispatch(context.Background(), request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: "gen"}))
	if resp.OK || resp.Error.Code != "not_visible" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestGetMessageBoundedLookup(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat}, history: []backend.RawMessage{message(2, chat, "+14155550100", "hello")}}
	server := testServer(t, fake, policyPath)
	resp := server.dispatch(context.Background(), request(t, rpc.MethodGetMessage, rpc.GetMessageParams{ChatID: 1, DatabaseGeneration: "gen", GUID: fake.history[0].GUID}))
	if !resp.OK || resp.Result.(rpc.GetMessageResult).Message.Text != "hello" {
		t.Fatalf("response = %#v", resp)
	}
	if len(fake.historyLimits) != 1 || fake.historyLimits[0] != server.cfg.MaxMessageScan {
		t.Fatalf("exact GUID lookup must retain its independent scan bound: %v", fake.historyLimits)
	}
	fake.history = make([]backend.RawMessage, server.cfg.MaxMessageScan)
	for i := range fake.history {
		fake.history[i] = message(int64(100-i), chat, "+14155550100", "other")
	}
	resp = server.dispatch(context.Background(), request(t, rpc.MethodGetMessage, rpc.GetMessageParams{ChatID: 1, DatabaseGeneration: "gen", GUID: "absent"}))
	if resp.OK || resp.Error.Code != "lookup_incomplete" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestCollectCursorFilteringAndErrors(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	allowed := dm(1, "+14155550100")
	denied := dm(2, "+14155550101")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{allowed, denied}}
	fake.collected = []backend.RawMessage{
		message(11, allowed, "+14155550100", "allowed"),
		message(12, allowed, "+14155550100", "Your login code is 998877"),
		message(13, denied, "+14155550101", "denied"),
	}
	server := testServer(t, fake, policyPath)
	resp := server.dispatch(context.Background(), request(t, rpc.MethodCollect, rpc.CollectParams{AfterRowID: 10, DatabaseGeneration: "gen", Limit: 10}))
	if !resp.OK {
		t.Fatalf("response = %#v", resp)
	}
	result := resp.Result.(rpc.CollectResult)
	if len(result.Messages) != 1 || result.Messages[0].RowID != 11 || fake.after != 10 {
		t.Fatalf("result = %#v, after = %d", result, fake.after)
	}
	c, err := decodeCursor(result.Cursor)
	if err != nil || c.RowID != 13 || c.AccountID != "acct" {
		t.Fatalf("cursor = %#v, %v", c, err)
	}
	fake.collected = nil
	resp = server.dispatch(context.Background(), request(t, rpc.MethodCollect, rpc.CollectParams{Cursor: result.Cursor, Limit: 10}))
	if !resp.OK || fake.after != 13 {
		t.Fatalf("cursor replay response = %#v, after = %d", resp, fake.after)
	}
	resp = server.dispatch(context.Background(), request(t, rpc.MethodCollect, rpc.CollectParams{Cursor: encodeCursor(cursor{V: 1, AccountID: "acct", Generation: "old", RowID: 13})}))
	if resp.OK || resp.Error.Code != "stale_cursor" {
		t.Fatalf("stale response = %#v", resp)
	}
	fake.err = backend.ErrOverflow
	resp = server.dispatch(context.Background(), request(t, rpc.MethodCollect, rpc.CollectParams{AfterRowID: 10, DatabaseGeneration: "gen"}))
	if resp.OK || resp.Error.Code != "collection_overflow" {
		t.Fatalf("overflow response = %#v", resp)
	}
}

func TestCollectPaginatesWithoutSkippingVisibleRows(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat}, collected: []backend.RawMessage{
		message(11, chat, "+14155550100", "one"), message(12, chat, "+14155550100", "two"),
	}}
	resp := testServer(t, fake, policyPath).dispatch(context.Background(), request(t, rpc.MethodCollect, rpc.CollectParams{AfterRowID: 10, DatabaseGeneration: "gen", Limit: 1}))
	result := resp.Result.(rpc.CollectResult)
	c, _ := decodeCursor(result.Cursor)
	if !result.More || len(result.Messages) != 1 || c.RowID != 11 {
		t.Fatalf("result = %#v cursor=%#v", result, c)
	}
}

func TestBackendErrorIsSanitizedAndUnknownMethodDenied(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	fake := &fakeBackend{generation: "gen", err: errors.New("TOP SECRET /Users/owner/Library/Messages/chat.db")}
	server := testServer(t, fake, policyPath)
	resp := server.dispatch(context.Background(), request(t, rpc.MethodListChats, rpc.ListChatsParams{}))
	data, _ := json.Marshal(resp)
	if resp.OK || strings.Contains(string(data), "TOP SECRET") || strings.Contains(string(data), "/Users/") {
		t.Fatalf("response = %s", data)
	}
	resp = server.dispatch(context.Background(), request(t, "imsg.send", struct{}{}))
	if resp.OK || resp.Error.Code != "method_not_allowed" {
		t.Fatalf("response = %#v", resp)
	}
}

func runTestServer(t *testing.T, server *Server) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case err := <-done:
			cancel()
			t.Fatalf("server exited before creating socket: %v", err)
		default:
		}
		if _, err := os.Lstat(server.cfg.SocketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server socket did not appear")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cancel, done
}

func TestActualSocketSerializationAndPeerAuthorization(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	fake := &fakeBackend{generation: "gen"}
	server := testServer(t, fake, policyPath)
	cancel, done := runTestServer(t, server)
	resp, err := rpc.Call(context.Background(), server.cfg.SocketPath, request(t, rpc.MethodSystemInfo, struct{}{}))
	if err != nil || !resp.OK {
		t.Fatalf("socket call = %#v, %v", resp, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	unauthorizedCfg := testConfig(t, policyPath)
	unauthorized, err := NewWithDeps(unauthorizedCfg, Dependencies{Backend: fake, LoadPolicy: policy.Load, PeerUID: func(*net.UnixConn) (uint32, error) { return unauthorizedCfg.ClientUID + 1, nil }})
	if err != nil {
		t.Fatal(err)
	}
	cancel, done = runTestServer(t, unauthorized)
	conn, err := net.Dial("unix", unauthorized.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(request(t, rpc.MethodSystemPing, struct{}{}))
	if err := rpc.WriteFrame(conn, payload); err != nil {
		t.Fatal(err)
	}
	responsePayload, err := rpc.ReadFrame(conn, 4096)
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	var denied rpc.Response
	if err := json.Unmarshal(responsePayload, &denied); err != nil || denied.Error == nil || denied.Error.Code != "unauthorized_peer" {
		t.Fatalf("denied = %#v, %v", denied, err)
	}
	cancel()
	<-done
}

func TestEndToEndSocketWithFakeIMsgProcess(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(dir, "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	databasePath := filepath.Join(dir, "chat.db")
	if err := os.WriteFile(databasePath, []byte("synthetic database identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	backendPath := filepath.Join(dir, "imsg-fake")
	script := `#!/bin/sh
case "$1" in
--version) printf '0.13.1\n' ;;
group) printf '%s\n' '{"id":1,"identifier":"+14155550100","guid":"iMessage;-;+14155550100","service":"iMessage","account_id":"imsg-account","is_group":false,"participants":["+14155550100"]}' ;;
history) printf '%s\n' '{"id":2,"chat_id":1,"guid":"message-2","sender":"+14155550100","is_from_me":false,"text":"hello","created_at":"2026-09-09T00:00:00Z","chat_identifier":"+14155550100","chat_guid":"iMessage;-;+14155550100","is_group":false,"participants":["+14155550100"],"reply_to_text":"must not leak","attachments":[{"filename":"must-not-leak.jpg"}]}' ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(backendPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t, policyPath)
	cfg.SocketPath = filepath.Join(dir, "broker.sock")
	cfg.BackendPath = backendPath
	cfg.DatabasePath = databasePath
	process, err := backend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewWithDeps(cfg, Dependencies{Backend: process, LoadPolicy: policy.Load, PeerUID: func(*net.UnixConn) (uint32, error) { return cfg.ClientUID, nil }})
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := runTestServer(t, server)
	defer func() { cancel(); <-done }()
	resp, err := rpc.Call(context.Background(), cfg.SocketPath, request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: process.Generation(), Limit: 5}))
	if err != nil || !resp.OK {
		t.Fatalf("response = %#v, %v", resp, err)
	}
	data, _ := json.Marshal(resp)
	if !strings.Contains(string(data), "hello") || strings.Contains(string(data), "must not leak") || strings.Contains(string(data), "must-not-leak.jpg") {
		t.Fatalf("unsafe serialized response: %s", data)
	}
}

func TestOversizedRequestRejected(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, nil, nil)
	server := testServer(t, &fakeBackend{generation: "gen"}, policyPath)
	cancel, done := runTestServer(t, server)
	defer func() { cancel(); <-done }()
	conn, err := net.Dial("unix", server.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], config.RequestMaxBytes()+1)
	if _, err := conn.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	payload, err := rpc.ReadFrame(conn, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var resp rpc.Response
	if err := json.Unmarshal(payload, &resp); err != nil || resp.Error == nil || resp.Error.Code != "invalid_request" {
		t.Fatalf("response = %#v, %v", resp, err)
	}
	conn.Close()

	malformed, err := net.Dial("unix", server.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer malformed.Close()
	if err := rpc.WriteFrame(malformed, []byte(`{"v":1,"id":"x","method":"system.ping","params":{},"unexpected":true}`)); err != nil {
		t.Fatal(err)
	}
	payload, err = rpc.ReadFrame(malformed, 4096)
	if err != nil {
		t.Fatal(err)
	}
	resp = rpc.Response{}
	if err := json.Unmarshal(payload, &resp); err != nil || resp.Error == nil || resp.Error.Code != "invalid_request" {
		t.Fatalf("malformed response = %#v, %v", resp, err)
	}
}

func TestSocketPathLifecycle(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketPath(regular); err == nil {
		t.Fatal("regular file would be replaced")
	}
	if data, _ := os.ReadFile(regular); string(data) != "keep" {
		t.Fatal("regular file changed")
	}
	symlink := filepath.Join(dir, "link")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketPath(symlink); err == nil {
		t.Fatal("symlink would be replaced")
	}
	active := filepath.Join(dir, "active.sock")
	listener, err := net.Listen("unix", active)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := prepareSocketPath(active); err == nil {
		t.Fatal("active socket would be replaced")
	}
}

func TestShutdownKeepsReplacement(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, nil, nil)
	server := testServer(t, &fakeBackend{generation: "gen"}, policyPath)
	cancel, done := runTestServer(t, server)
	defer cancel()
	if err := os.Rename(server.cfg.SocketPath, server.cfg.SocketPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(server.cfg.SocketPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(server.cfg.SocketPath); err != nil || string(data) != "replacement" {
		t.Fatalf("shutdown removed replacement: %s, %v", data, err)
	}
}

type waitingBackend struct {
	backend.Service
	started chan context.Context
}

func (b waitingBackend) History(ctx context.Context, _ int64, _ int) ([]backend.RawMessage, error) {
	b.started <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestShutdownCancelsRequest(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{dm(1, "+14155550100")}}
	server := testServer(t, fake, policyPath)
	waiting := waitingBackend{Service: fake, started: make(chan context.Context, 1)}
	server.deps.Backend = waiting
	cancel, done := runTestServer(t, server)
	defer cancel()
	callDone := make(chan struct{})
	req := request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: "gen"})
	go func() {
		defer close(callDone)
		_, _ = rpc.Call(context.Background(), server.cfg.SocketPath, req)
	}()
	select {
	case ctx := <-waiting.started:
		if _, ok := ctx.Deadline(); !ok {
			t.Error("backend request has no overall deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("backend request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the backend")
	}
	<-callDone
}

func TestRequestTimeoutIsExplicit(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{dm(1, "+14155550100")}}
	server := testServer(t, fake, policyPath)
	server.cfg.BackendTimeoutMillis = 50
	server.deps.Backend = waitingBackend{Service: fake, started: make(chan context.Context, 1)}
	start := time.Now()
	resp := server.dispatch(context.Background(), request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: "gen", Limit: 1}))
	if resp.OK || resp.Error.Code != "backend_timeout" || !resp.Error.Retryable || resp.Result != nil {
		t.Fatalf("timeout response=%+v", resp)
	}
	if time.Since(start) > time.Second {
		t.Fatal("request exceeded its bounded time budget")
	}
}

func TestBackendTimeoutMapping(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, errors.Join(backend.ErrFailed, context.DeadlineExceeded)} {
		resp := backendFailure("test", err)
		if resp.Error.Code != "backend_timeout" || !resp.Error.Retryable || resp.Result != nil {
			t.Fatalf("timeout response=%+v", resp)
		}
	}
	for _, err := range []error{backend.ErrFailed, context.Canceled} {
		if resp := backendFailure("test", err); resp.Error.Code != "backend_unavailable" {
			t.Fatalf("non-timeout misclassified: %+v", resp)
		}
	}
}

func TestLimitsAndCompleteness(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat, dm(2, "+14155550100")}, history: []backend.RawMessage{
		message(12, chat, "+14155550100", "two"), message(11, chat, "+14155550100", "one"),
	}}
	server := testServer(t, fake, policyPath)
	server.cfg.MaxResults = 1
	for _, req := range []rpc.Request{
		request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: "gen"}),
		request(t, rpc.MethodListChats, rpc.ListChatsParams{}),
	} {
		resp := server.dispatch(context.Background(), req)
		if !resp.OK {
			t.Fatalf("response=%+v", resp)
		}
		switch r := resp.Result.(type) {
		case rpc.HistoryResult:
			if len(r.Messages) != 1 || r.ScanComplete {
				t.Fatalf("history=%+v", r)
			}
		case rpc.ListChatsResult:
			if len(r.Chats) != 1 || r.ScanComplete {
				t.Fatalf("chats=%+v", r)
			}
		}
	}
}

func TestCursorRejectsTrailingData(t *testing.T) {
	raw := `{"v":1,"account_id":"acct","generation":"gen","row_id":1} {}`
	if _, err := decodeCursor(base64.RawURLEncoding.EncodeToString([]byte(raw))); err == nil {
		t.Fatal("cursor with a second object was accepted")
	}
}

func TestRealPeerCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	uid, err := peerUID(conn)
	if err != nil || uid != uint32(os.Geteuid()) {
		t.Fatalf("uid=%d err=%v", uid, err)
	}
}
