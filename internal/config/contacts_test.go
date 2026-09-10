package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func contactsConfig() ContactsSource {
	c := ContactsSource{HelperPath: "/trusted/contacts", ContainerID: "icloud", GroupIDs: []string{"friends"}}
	c.ApplyDefaults()
	return c
}

func TestContactsConfigDefaultsAndBounds(t *testing.T) {
	cfg := contactsConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.RefreshSeconds != 900 || cfg.MaxAgeSeconds != 3600 || cfg.DefaultPhoneRegion != "" {
		t.Fatal("unsafe defaults")
	}
	for name, edit := range map[string]func(*ContactsSource){
		"relative helper":       func(c *ContactsSource) { c.HelperPath = "helper" },
		"missing container":     func(c *ContactsSource) { c.ContainerID = "" },
		"explicit empty groups": func(c *ContactsSource) { c.GroupIDs = []string{} },
		"duplicate group":       func(c *ContactsSource) { c.GroupIDs = []string{"friends", "friends"} },
		"invalid id":            func(c *ContactsSource) { c.GroupIDs = []string{"bad\x00id"} },
		"unsupported region":    func(c *ContactsSource) { c.DefaultPhoneRegion = "guess" },
		"invalid refresh":       func(c *ContactsSource) { c.RefreshSeconds = 1 },
		"invalid expiry":        func(c *ContactsSource) { c.MaxAgeSeconds = 90000 },
		"expiry before refresh": func(c *ContactsSource) { c.MaxAgeSeconds = 10 },
		"too many contacts":     func(c *ContactsSource) { c.MaxContacts = 10001 },
		"invalid timeout":       func(c *ContactsSource) { c.TimeoutMillis = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			c := contactsConfig()
			edit(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe Contacts configuration accepted")
			}
		})
	}
}

func TestOmittedGroupSelectionDefaultsToAllListsInExplicitContainer(t *testing.T) {
	var cfg ContactsSource
	if err := json.Unmarshal([]byte(`{"helper_path":"/trusted/helper","container_id":"icloud"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.ApplyDefaults()
	if cfg.GroupIDs != nil || cfg.Validate() != nil {
		t.Fatal("omitted group_ids did not select all lists")
	}
	cfg.ContainerID = ""
	if cfg.Validate() == nil {
		t.Fatal("all-lists default selected an implicit account")
	}
}

func TestLoadContactsConfigAndRejectUnknownFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	c := validConfig()
	c.Contacts = &ContactsSource{HelperPath: "/trusted/helper", ContainerID: "icloud", GroupIDs: []string{"friends"}}
	data, _ := json.Marshal(c)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Contacts.RefreshSeconds != 900 {
		t.Fatalf("config failed: %v", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	raw["contacts"].(map[string]any)["all_contacts"] = true
	data, _ = json.Marshal(raw)
	_ = os.WriteFile(path, data, 0600)
	if _, err := Load(path); err == nil {
		t.Fatal("silently accepted a broader Contacts selection")
	}
}
