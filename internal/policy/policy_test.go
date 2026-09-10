package policy

import "testing"

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
		{"group unknown members", Conversation{GUID: "iMessage;+;group", Identifier: "chat123;+;group", IsGroup: boolPtr(true), Participants: stringsPtr("+14155550199", "stranger@example.net")}, true},
		{"excluded group", Conversation{GUID: "iMessage;+;excluded", Identifier: "x;+;x", IsGroup: boolPtr(true), Participants: stringsPtr("+14155550199", "stranger@example.net")}, false},
		{"missing metadata", Conversation{GUID: "x", Identifier: "+14155550100", IsGroup: nil, Participants: stringsPtr("+14155550100")}, false},
		{"contradictory metadata", Conversation{GUID: "x;+;x", Identifier: "x;+;x", IsGroup: boolPtr(false), Participants: stringsPtr("+14155550100")}, false},
		{"group one remaining participant", Conversation{GUID: "x;+;x", Identifier: "x;+;x", IsGroup: boolPtr(true), Participants: stringsPtr("+14155550100")}, true},
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
