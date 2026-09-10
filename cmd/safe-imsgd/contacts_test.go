package main

import "testing"

func TestContactsCLIRejectsAmbiguousOrMutatingCommands(t *testing.T) {
	for _, args := range [][]string{
		{}, {"send"}, {"authorize"}, {"groups"}, {"preview"},
		{"groups", "--helper", "/helper", "--config", "/config"},
		{"preview", "--config", "/config", "--helper", "/helper"},
		{"groups", "--helper", "/helper", "--show-identities"},
		{"groups", "--helper", "/helper", "extra"},
	} {
		if code := contactsCommand(args); code != 2 {
			t.Fatalf("accepted ambiguous command %v: %d", args, code)
		}
	}
}
