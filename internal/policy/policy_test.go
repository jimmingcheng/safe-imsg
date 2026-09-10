package policy

import (
	"reflect"
	"testing"
)

func boolPtr(v bool) *bool             { return &v }
func stringsPtr(v ...string) *[]string { return &v }

func testPolicy() *Policy {
	return &Policy{
		owners:   map[string]struct{}{`owner@example.com`: {}},
		direct:   map[string]struct{}{`+14155550100`: {}, `friend@example.com`: {}},
		excluded: map[string]struct{}{`iMessage;+;excluded`: {}, `iMessage;-;friend@example.com`: {}},
	}
}

func TestAuthorize(t *testing.T) {
	p := testPolicy()
	tests := []struct {
		name string
		c    Conversation
		want bool
	}{
		{"allowed dm", Conversation{GUID: "iMessage;-;+14155550100", Identifier: "+1 (415) 555-0100", IsGroup: boolPtr(false), Participants: stringsPtr("+14155550100")}, true},
		{"denied dm", Conversation{GUID: "iMessage;-;+14155550101", Identifier: "+14155550101", IsGroup: boolPtr(false), Participants: stringsPtr("+14155550101")}, false},
		{"owner alias", Conversation{GUID: "iMessage;-;owner@example.com", Identifier: "owner@example.com", IsGroup: boolPtr(false), Participants: stringsPtr("owner@example.com")}, false},
		{"excluded allowed dm", Conversation{GUID: "iMessage;-;friend@example.com", Identifier: "friend@example.com", IsGroup: boolPtr(false), Participants: stringsPtr("friend@example.com")}, false},
		{"group unknown members", Conversation{GUID: "iMessage;+;group", Identifier: "group", IsGroup: boolPtr(true), Participants: stringsPtr("+14155550199", "stranger@example.net")}, true},
		{"excluded group", Conversation{GUID: "iMessage;+;excluded", Identifier: "excluded", IsGroup: boolPtr(true), Participants: stringsPtr("+14155550199", "stranger@example.net")}, false},
		{"missing metadata", Conversation{GUID: "x", Identifier: "+14155550100", IsGroup: nil, Participants: stringsPtr("+14155550100")}, false},
		{"contradictory metadata", Conversation{GUID: "x;+;x", Identifier: "x;+;x", IsGroup: boolPtr(false), Participants: stringsPtr("+14155550100")}, false},
		{"group one remaining participant", Conversation{GUID: "x;+;x", Identifier: "x;+;x", IsGroup: boolPtr(true), Participants: stringsPtr("+14155550100")}, true},
		{"wrong DM GUID counterpart", Conversation{GUID: "iMessage;-;other@example.com", Identifier: "+14155550100", IsGroup: boolPtr(false), Participants: stringsPtr("+14155550100")}, false},
		{"malformed DM GUID", Conversation{GUID: "not-native", Identifier: "+14155550100", IsGroup: boolPtr(false), Participants: stringsPtr("+14155550100")}, false},
		{"group flag contradicts direct GUID", Conversation{GUID: "iMessage;-;+14155550100", Identifier: "iMessage;+;group", IsGroup: boolPtr(true), Participants: stringsPtr("+14155550100")}, false},
		{"group identifier mismatch", Conversation{GUID: "iMessage;+;group", Identifier: "other-group", IsGroup: boolPtr(true), Participants: stringsPtr("+14155550100")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Authorize(tc.c).Allowed; got != tc.want {
				t.Fatalf("allowed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeRejectsDisplayName(t *testing.T) {
	if _, err := NormalizeIdentity("Alice <alice@example.com>"); err == nil {
		t.Fatal("display name was accepted")
	}
	if _, err := NormalizeIdentity("*@example.com"); err == nil {
		t.Fatal("wildcard identity was accepted")
	}
}

func TestDerivedGrantsPreserveStaticPolicyWithoutMutatingIt(t *testing.T) {
	base := testPolicy()
	derived, err := base.WithDirect([]string{"NEW@example.com", "owner@example.com", "+14155550100"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"+14155550100", "friend@example.com", "new@example.com"}; !reflect.DeepEqual(derived.DirectIdentities(), want) {
		t.Fatal("derived grants did not union, normalize and deduplicate safely")
	}
	if len(base.DirectIdentities()) != 2 {
		t.Fatal("derived grants mutated the static policy")
	}
	// A new empty snapshot drops derived grants, not explicit manual grants.
	empty, err := base.WithDirect(nil)
	if err != nil || !reflect.DeepEqual(empty.DirectIdentities(), base.DirectIdentities()) {
		t.Fatal("empty snapshot changed manual grants")
	}
	if result, err := base.WithDirect([]string{"new@example.com", "*@example.com"}); err == nil || result != nil {
		t.Fatal("invalid derived input produced a partial grant")
	}
	if decision := derived.Authorize(Conversation{GUID: "iMessage;-;friend@example.com", Identifier: "friend@example.com", IsGroup: boolPtr(false), Participants: stringsPtr("friend@example.com")}); decision.Allowed {
		t.Fatal("derived policy lost explicit exclusions")
	}
}
