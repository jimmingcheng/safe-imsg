package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig() Config {
	uid := uint32(os.Geteuid()) + 1
	if uid == 0 {
		uid = 1
	}
	return Config{
		Instance: "personal", AccountID: "apple-personal", ClientUID: uid,
		SocketPath: "/tmp/safe-imsg-test/broker.sock", SocketMode: "0660",
		BackendPath: "/usr/local/bin/imsg", BackendVersion: "0.13.1", DatabasePath: "/Users/owner/Library/Messages/chat.db",
		BackendAccountID: "iMessage:owner", DatabaseGeneration: "install-1", PolicyPath: "/Users/owner/.config/safe-imsg/policy.json",
		MaxResults: 100, MaxChatScan: 500, MaxMessageScan: 1000, MaxCollectionScan: 500,
		BackendTimeoutMillis: 10000,
	}
}

func TestValidateRejectsUnsafeConfig(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"relative database", func(c *Config) { c.DatabasePath = "chat.db" }},
		{"same uid", func(c *Config) { c.ClientUID = uint32(os.Geteuid()) }},
		{"world socket", func(c *Config) { c.SocketMode = "0666" }},
		{"unbounded results", func(c *Config) { c.MaxResults = 501 }},
		{"short timeout", func(c *Config) { c.BackendTimeoutMillis = 1 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}

func TestLoadRejectsUnknownAndWritableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("writable config accepted")
	}
}
