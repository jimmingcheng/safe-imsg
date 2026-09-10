package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jimmingcheng/safe-imsg/internal/securefile"
)

const (
	CollectionBackendVersion = "0.13.1-safe-imsg.3"
	defaultSocketMode        = "0660"
	defaultMaxResults        = 100
	defaultMaxChatScan       = 500
	defaultMaxMessageScan    = 1000
	defaultMaxCollectionScan = 500
	defaultBackendTimeoutMS  = 10000
	requestMaxBytes          = 1 << 20
)

type Config struct {
	Instance             string          `json:"instance"`
	AccountID            string          `json:"account_id"`
	ClientUID            uint32          `json:"client_uid"`
	SocketPath           string          `json:"socket_path"`
	SocketMode           string          `json:"socket_mode,omitempty"`
	BackendPath          string          `json:"backend_path"`
	BackendVersion       string          `json:"backend_version"`
	DatabasePath         string          `json:"database_path"`
	BackendAccountID     string          `json:"backend_account_id"`
	DatabaseGeneration   string          `json:"database_generation"`
	PolicyPath           string          `json:"policy_path"`
	MaxResults           int             `json:"max_results,omitempty"`
	MaxChatScan          int             `json:"max_chat_scan,omitempty"`
	MaxMessageScan       int             `json:"max_message_scan,omitempty"`
	MaxCollectionScan    int             `json:"max_collection_scan,omitempty"`
	BackendTimeoutMillis int             `json:"backend_timeout_ms,omitempty"`
	Contacts             *ContactsSource `json:"contacts,omitempty"`
}

func Load(path string) (Config, error) {
	data, err := securefile.ReadOwnerFile(path, 1<<20)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("parse config: expected one object")
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Contacts != nil {
		c.Contacts.ApplyDefaults()
	}
	if c.SocketMode == "" {
		c.SocketMode = defaultSocketMode
	}
	if c.MaxResults == 0 {
		c.MaxResults = defaultMaxResults
	}
	if c.MaxChatScan == 0 {
		c.MaxChatScan = defaultMaxChatScan
	}
	if c.MaxMessageScan == 0 {
		c.MaxMessageScan = defaultMaxMessageScan
	}
	if c.MaxCollectionScan == 0 {
		c.MaxCollectionScan = defaultMaxCollectionScan
	}
	if c.BackendTimeoutMillis == 0 {
		c.BackendTimeoutMillis = defaultBackendTimeoutMS
	}
}

func (c Config) Validate() error {
	if c.Contacts != nil {
		if err := c.Contacts.Validate(); err != nil {
			return err
		}
	}
	for field, value := range map[string]string{
		"instance": c.Instance, "account_id": c.AccountID,
		"backend_version": c.BackendVersion, "backend_account_id": c.BackendAccountID, "database_generation": c.DatabaseGeneration,
	} {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("config: %s is required", field)
		}
		if len(value) > 128 {
			return fmt.Errorf("config: %s is too long", field)
		}
	}
	if c.BackendVersion != "0.13.1" && c.BackendVersion != CollectionBackendVersion {
		return fmt.Errorf("config: backend_version must be an audited imsg release")
	}
	for field, value := range map[string]string{
		"socket_path": c.SocketPath, "backend_path": c.BackendPath,
		"database_path": c.DatabasePath, "policy_path": c.PolicyPath,
	} {
		if !filepath.IsAbs(value) || filepath.Clean(value) == string(filepath.Separator) || filepath.Clean(value) != value {
			return fmt.Errorf("config: %s must be a clean absolute non-root path", field)
		}
	}
	if c.ClientUID == 0 {
		return fmt.Errorf("config: client_uid must be non-zero")
	}
	if c.ClientUID == uint32(os.Geteuid()) {
		return fmt.Errorf("config: client_uid must differ from the broker owner uid")
	}
	mode, err := c.SocketFileMode()
	if err != nil {
		return err
	}
	if mode.Perm()&0o007 != 0 || mode.Perm()&0o600 != 0o600 {
		return fmt.Errorf("config: socket_mode must grant owner rw and no access to other users")
	}
	if c.MaxResults < 1 || c.MaxResults > 500 {
		return fmt.Errorf("config: max_results must be between 1 and 500")
	}
	if c.MaxChatScan < c.MaxResults || c.MaxChatScan > 5000 {
		return fmt.Errorf("config: max_chat_scan must be between max_results and 5000")
	}
	if c.MaxMessageScan < c.MaxResults || c.MaxMessageScan > 10000 {
		return fmt.Errorf("config: max_message_scan must be between max_results and 10000")
	}
	if c.MaxCollectionScan < c.MaxResults || c.MaxCollectionScan > 1000 {
		return fmt.Errorf("config: max_collection_scan must be between max_results and 1000")
	}
	if c.BackendTimeoutMillis < 1000 || c.BackendTimeoutMillis > 60000 {
		return fmt.Errorf("config: backend_timeout_ms must be between 1000 and 60000")
	}
	return nil
}

func (c Config) SocketFileMode() (os.FileMode, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(c.SocketMode), 8, 32)
	if err != nil || parsed > 0o777 {
		return 0, fmt.Errorf("config: invalid socket_mode %q", c.SocketMode)
	}
	return os.FileMode(parsed), nil
}

func RequestMaxBytes() uint32 { return requestMaxBytes }
