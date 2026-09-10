package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jimmingcheng/safe-imsg/internal/backend"
	"github.com/jimmingcheng/safe-imsg/internal/rpc"
)

func collectPage(t *testing.T, server *Server, params rpc.CollectParams) rpc.CollectResult {
	t.Helper()
	resp := server.dispatch(context.Background(), request(t, rpc.MethodCollect, params))
	if !resp.OK {
		t.Fatalf("response=%+v", resp)
	}
	return resp.Result.(rpc.CollectResult)
}

func TestCollectionBacklogSurvivesRestartAndConcurrentArrivals(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat}}
	for id := int64(1); id <= 1205; id++ {
		fake.collected = append(fake.collected, message(id, chat, "+14155550100", "ordinary"))
	}
	server := testServer(t, fake, policyPath)
	first := collectPage(t, server, rpc.CollectParams{AfterRowID: 0, DatabaseGeneration: "gen", Limit: 10})
	c, err := server.decodeCursor(first.Cursor)
	if err != nil || c.RowID != 10 || c.ThroughRowID != 1205 || !first.More || first.RangeComplete {
		t.Fatalf("cursor=%+v result=%+v", c, first)
	}
	fake.collected = append(fake.collected, message(1206, chat, "+14155550100", "new arrival"))
	// Recreate the broker with the persisted key, as a restart does.
	server = testServer(t, fake, policyPath)
	last := first
	count := len(first.Messages)
	for last.More {
		last = collectPage(t, server, rpc.CollectParams{Cursor: last.Cursor, Limit: 10})
		count += len(last.Messages)
		if fake.through != 1205 {
			t.Fatal("pending range was widened")
		}
	}
	if count != 1205 || !last.RangeComplete {
		t.Fatalf("count=%d complete=%v", count, last.RangeComplete)
	}
	next := collectPage(t, server, rpc.CollectParams{Cursor: last.Cursor, Limit: 10})
	if len(next.Messages) != 1 || next.Messages[0].RowID != 1206 || !next.RangeComplete {
		t.Fatalf("next=%+v", next)
	}
	quiet := collectPage(t, server, rpc.CollectParams{Cursor: next.Cursor, Limit: 10})
	if len(quiet.Messages) != 0 || !quiet.RangeComplete || quiet.More {
		t.Fatalf("quiet=%+v", quiet)
	}
}

func TestCollectionTimestampHorizonPersistsAcrossPendingSnapshotOnly(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, nil, nil)
	fake := &fakeBackend{generation: "gen"}
	fake.collectionFn = func(after, through int64, limit int) (backend.CollectionPage, error) {
		if after == 0 {
			return backend.CollectionPage{Rows: []backend.CollectionRow{{RowID: 1}},
				ThroughRowID: 2, ScannedThroughRowID: 1, Complete: false}, nil
		}
		if through == 2 {
			return backend.CollectionPage{Rows: []backend.CollectionRow{{RowID: 2}},
				ThroughRowID: 2, ScannedThroughRowID: 2, Complete: true}, nil
		}
		return backend.CollectionPage{ThroughRowID: after, ScannedThroughRowID: after, Complete: true}, nil
	}
	server := testServer(t, fake, policyPath)
	result := collectPage(t, server, rpc.CollectParams{AfterRowID: 0, DatabaseGeneration: "gen",
		NotBefore: "2026-09-08T17:00:00-07:00", Limit: 1})
	if fake.notBefore != "2026-09-09T00:00:00Z" || result.RangeComplete {
		t.Fatalf("not_before=%q result=%+v", fake.notBefore, result)
	}
	c, err := server.decodeCursor(result.Cursor)
	if err != nil || c.NotBefore != "2026-09-09T00:00:00Z" {
		t.Fatalf("pending horizon was not sealed into cursor: cursor=%+v err=%v", c, err)
	}
	result = collectPage(t, server, rpc.CollectParams{Cursor: result.Cursor, Limit: 1})
	if fake.notBefore != "2026-09-09T00:00:00Z" || !result.RangeComplete {
		t.Fatalf("pending horizon was not reused: not_before=%q result=%+v", fake.notBefore, result)
	}
	c, err = server.decodeCursor(result.Cursor)
	if err != nil || c.NotBefore != "" {
		t.Fatalf("completed horizon remained in cursor: cursor=%+v err=%v", c, err)
	}
	collectPage(t, server, rpc.CollectParams{Cursor: result.Cursor, Limit: 1})
	if fake.notBefore != "" {
		t.Fatalf("horizon was reused after completed snapshot: %q", fake.notBefore)
	}
	for _, params := range []rpc.CollectParams{
		{AfterRowID: 1, DatabaseGeneration: "gen", NotBefore: "2026-09-09T00:00:00Z"},
		{AfterRowID: 0, DatabaseGeneration: "gen", NotBefore: "not-a-date"},
		{Cursor: result.Cursor, NotBefore: "2026-09-09T00:00:00Z"},
	} {
		resp := server.dispatch(context.Background(), request(t, rpc.MethodCollect, params))
		if resp.OK || resp.Error.Code != "invalid_params" {
			t.Fatalf("unsafe horizon accepted: params=%+v response=%+v", params, resp)
		}
	}
}

func TestCollectionFilteredOnlyPageMakesProgressAndRevocationApplies(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat}, collected: []backend.RawMessage{
		message(11, chat, "+14155550100", "Your verification code is 123456"),
		message(12, chat, "+14155550100", "ordinary"),
	}}
	server := testServer(t, fake, policyPath)
	first := collectPage(t, server, rpc.CollectParams{AfterRowID: 10, DatabaseGeneration: "gen", Limit: 1})
	if len(first.Messages) != 0 || !first.More || first.RangeComplete {
		t.Fatalf("first=%+v", first)
	}
	writePolicy(t, policyPath, nil, nil)
	last := collectPage(t, server, rpc.CollectParams{Cursor: first.Cursor, Limit: 1})
	if len(last.Messages) != 0 || !last.RangeComplete {
		t.Fatalf("revocation bypassed: %+v", last)
	}
	data, _ := json.Marshal(last)
	if strings.Contains(string(data), "row_id") || strings.Contains(string(data), "ordinary") {
		t.Fatal("filtered position or text leaked")
	}
}

func TestCollectionNativeSkippedRowsAndPolicyReload(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	msg := message(12, chat, "+14155550100", "revoked during backend read")
	fake := &fakeBackend{generation: "gen"}
	fake.collectionFn = func(after, through int64, limit int) (backend.CollectionPage, error) {
		writePolicy(t, policyPath, nil, nil)
		return backend.CollectionPage{Rows: []backend.CollectionRow{{RowID: 11}, {RowID: 12, Chat: &chat, Message: &msg}}, ThroughRowID: 12, ScannedThroughRowID: 12, Complete: true}, nil
	}
	server := testServer(t, fake, policyPath)
	page := collectPage(t, server, rpc.CollectParams{AfterRowID: 10, DatabaseGeneration: "gen", Limit: 2})
	if len(page.Messages) != 0 || !page.RangeComplete {
		t.Fatalf("page=%+v", page)
	}
}

func TestCollectionUnsafeMessageIsSuppressedWithoutWedgingCursor(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, []string{"+14155550100"}, nil)
	chat := dm(1, "+14155550100")
	bad := message(11, chat, "+14155550100", "must not escape")
	bad.IsGroup = boolp(true)
	fake := &fakeBackend{generation: "gen"}
	fake.collectionFn = func(_, _ int64, _ int) (backend.CollectionPage, error) {
		return backend.CollectionPage{Rows: []backend.CollectionRow{{RowID: 11, Chat: &chat, Message: &bad}},
			ThroughRowID: 11, ScannedThroughRowID: 11, Complete: true}, nil
	}
	result := collectPage(t, testServer(t, fake, policyPath), rpc.CollectParams{
		AfterRowID: 0, DatabaseGeneration: "gen", Limit: 1})
	if len(result.Messages) != 0 || !result.RangeComplete || result.More {
		t.Fatalf("unsafe row wedged or escaped: %+v", result)
	}
}

func TestCursorOpaqueAuthenticatedAndLegacyUpgrade(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, nil, nil)
	server := testServer(t, &fakeBackend{generation: "gen"}, policyPath)
	legacy := encodeCursor(cursor{V: 1, AccountID: "acct", Generation: "gen", RowID: 10})
	result := collectPage(t, server, rpc.CollectParams{Cursor: legacy, Limit: 1})
	if !strings.HasPrefix(result.Cursor, "c2_") {
		t.Fatal("legacy cursor was not upgraded")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(result.Cursor[3:])
	if err != nil || json.Valid(sealed) || strings.Contains(string(sealed), "row_id") {
		t.Fatal("cursor is not opaque")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := server.decodeCursor("c2_" + base64.RawURLEncoding.EncodeToString(sealed)); err == nil {
		t.Fatal("tampered cursor accepted")
	}
	other := testServer(t, &fakeBackend{generation: "gen"}, policyPath)
	other.cfg.Instance = "another-instance"
	if _, err := other.decodeCursor(result.Cursor); err == nil {
		t.Fatal("cursor crossed instance boundary")
	}
}

func TestCursorKeyRejectsUnsafeOrCorruptFiles(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o644} {
		path := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(path, []byte("invalid key"), mode); err != nil {
			t.Fatal(err)
		}
		if _, err := loadCursorCipher(path); err == nil {
			t.Fatal("unsafe or corrupt key accepted")
		}
	}
}
