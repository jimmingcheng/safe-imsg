package broker

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jimmingcheng/safe-imsg/internal/backend"
	"github.com/jimmingcheng/safe-imsg/internal/config"
	"github.com/jimmingcheng/safe-imsg/internal/contacts"
	"github.com/jimmingcheng/safe-imsg/internal/rpc"
)

func TestContactsDerivedPolicyRefreshExclusionsAndReadRevocation(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, nil, []string{"iMessage;-;blocked@example.test"})
	direct, other, owner, excluded := dm(1, "friend@example.test"), dm(2, "stranger@example.test"), dm(3, "owner@example.com"), dm(4, "blocked@example.test")
	shared := group(5, "iMessage;+;group", "stranger@example.test")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{direct, other, owner, excluded, shared}, history: []backend.RawMessage{message(11, direct, "friend@example.test", "hello")}}
	cfg := testConfig(t, policyPath)
	cfg.Contacts = &config.ContactsSource{HelperPath: "/trusted/native", ContainerID: "icloud", GroupIDs: []string{"family"}}
	cfg.Contacts.ApplyDefaults()
	snapshot := contacts.Snapshot{ContainerID: "icloud", GroupIDs: []string{"family"}, Complete: true,
		Contacts: []contacts.Contact{{ID: "contact", Phones: []string{}, Emails: []string{"friend@example.test", "owner@example.com", "blocked@example.test"}}}}
	server, err := NewWithDeps(cfg, Dependencies{Backend: fake, ContactsFetch: func(context.Context, config.ContactsSource) (contacts.Snapshot, error) { return snapshot, nil }})
	if err != nil {
		t.Fatal(err)
	}
	list := request(t, rpc.MethodListChats, rpc.ListChatsParams{Limit: 10})
	if response := server.dispatch(context.Background(), list); response.OK || response.Error.Code != "policy_unavailable" {
		t.Fatal("uninitialized Contacts source admitted reads")
	}
	server.contacts.Refresh(context.Background())
	response := server.dispatch(context.Background(), list)
	if !response.OK {
		t.Fatal(response.Error)
	}
	chats := response.Result.(rpc.ListChatsResult).Chats
	if len(chats) != 2 || chats[0].ChatID != 1 || chats[1].ChatID != 5 {
		t.Fatalf("wrong grants: %+v", chats)
	}
	info := server.dispatch(context.Background(), request(t, rpc.MethodSystemInfo, struct{}{}))
	payload, _ := json.Marshal(info)
	if !strings.Contains(string(payload), `"contacts_policy":{"state":"ready"`) {
		t.Fatal("missing source health")
	}
	for _, value := range []string{"icloud", "family", "friend@example.test", "blocked@example.test", "contact_count"} {
		if strings.Contains(string(payload), value) {
			t.Fatal("Contacts information leaked through system.info")
		}
	}
	// Membership removal during a backend read is enforced before serialization.
	fake.historyFn = func() []backend.RawMessage {
		snapshot.Contacts = []contacts.Contact{}
		server.contacts.Refresh(context.Background())
		return fake.history
	}
	response = server.dispatch(context.Background(), request(t, rpc.MethodHistory, rpc.GenerationParams{ChatID: 1, DatabaseGeneration: "gen", Limit: 10}))
	if response.OK || response.Error.Code != "not_visible" {
		t.Fatalf("revoked read returned: %+v", response)
	}
	response = server.dispatch(context.Background(), list)
	if !response.OK || len(response.Result.(rpc.ListChatsResult).Chats) != 1 {
		t.Fatal("group rule changed with DM membership")
	}
}

func TestContactsUnavailableCollectCannotAdvanceCursorOrExposeContactOperations(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	writePolicy(t, policyPath, nil, nil)
	chat := group(1, "iMessage;+;group", "unknown@example.test")
	fake := &fakeBackend{generation: "gen", chats: []backend.RawChat{chat}, collected: []backend.RawMessage{message(11, chat, "unknown@example.test", "group evidence")}}
	cfg := testConfig(t, policyPath)
	cfg.Contacts = &config.ContactsSource{HelperPath: "/trusted/native", ContainerID: "icloud", GroupIDs: []string{"family"}}
	cfg.Contacts.ApplyDefaults()
	server, err := NewWithDeps(cfg, Dependencies{Backend: fake, ContactsFetch: func(context.Context, config.ContactsSource) (contacts.Snapshot, error) {
		return contacts.Snapshot{}, contacts.PermissionDenied
	}})
	if err != nil {
		t.Fatal(err)
	}
	server.contacts.Refresh(context.Background())
	response := server.dispatch(context.Background(), request(t, rpc.MethodCollect, rpc.CollectParams{AfterRowID: 10, DatabaseGeneration: "gen", Limit: 10}))
	if response.OK || response.Result != nil || response.Error.Code != "policy_unavailable" || fake.after != 0 {
		t.Fatal("collection advanced or read backend while policy was unavailable")
	}
	for _, method := range []string{"contacts.groups", "contacts.preview", "contacts.refresh", "contacts.authorize", "policy.update"} {
		response = server.dispatch(context.Background(), request(t, method, struct{}{}))
		if response.OK || response.Error.Code != "method_not_allowed" {
			t.Fatalf("exposed owner operation %s", method)
		}
	}
	response = server.dispatch(context.Background(), request(t, rpc.MethodListChats, map[string]any{"limit": 1, "group_ids": []string{"another"}}))
	if response.OK || response.Error.Code != "invalid_params" {
		t.Fatal("client can select Contacts groups")
	}
}
