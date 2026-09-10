package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ContactsSource is owner configuration, never a client RPC parameter.
// Absence retains the existing static policy. Selection is explicit and local
// to this Mac. Omitted group_ids means all lists in the selected container;
// an explicit nonempty array restricts selection. No Contacts source is implicit.
type ContactsSource struct {
	HelperPath         string   `json:"helper_path"`
	ContainerID        string   `json:"container_id"`
	GroupIDs           []string `json:"group_ids,omitempty"`
	DefaultPhoneRegion string   `json:"default_phone_region,omitempty"`
	RefreshSeconds     int      `json:"refresh_seconds,omitempty"`
	MaxAgeSeconds      int      `json:"max_age_seconds,omitempty"`
	TimeoutMillis      int      `json:"timeout_ms,omitempty"`
	MaxContacts        int      `json:"max_contacts,omitempty"`
}

func (c *ContactsSource) ApplyDefaults() {
	if c.RefreshSeconds == 0 {
		c.RefreshSeconds = 900
	}
	if c.MaxAgeSeconds == 0 {
		c.MaxAgeSeconds = 3600
	}
	if c.TimeoutMillis == 0 {
		c.TimeoutMillis = 10000
	}
	if c.MaxContacts == 0 {
		c.MaxContacts = 1000
	}
}

func (c ContactsSource) Validate() error {
	if !filepath.IsAbs(c.HelperPath) || filepath.Clean(c.HelperPath) != c.HelperPath || c.HelperPath == "/" {
		return fmt.Errorf("contacts: helper_path must be a clean absolute non-root path")
	}
	validID := func(s string) bool {
		return s != "" && strings.TrimSpace(s) == s && len(s) <= 512 && !strings.ContainsAny(s, "\x00\r\n")
	}
	if !validID(c.ContainerID) || (c.GroupIDs != nil && len(c.GroupIDs) == 0) || len(c.GroupIDs) > 100 {
		return fmt.Errorf("contacts: select one container; omit group_ids for all lists or provide 1–100 exact IDs")
	}
	seen := map[string]bool{}
	for _, id := range c.GroupIDs {
		if !validID(id) || seen[id] {
			return fmt.Errorf("contacts: invalid or duplicate group ID")
		}
		seen[id] = true
	}
	if c.DefaultPhoneRegion != "" && c.DefaultPhoneRegion != "US" && c.DefaultPhoneRegion != "CA" {
		return fmt.Errorf("contacts: default_phone_region must be US, CA, or empty (international numbers only)")
	}
	if c.RefreshSeconds < 60 || c.RefreshSeconds > 3600 || c.MaxAgeSeconds < c.RefreshSeconds || c.MaxAgeSeconds > 86400 || c.TimeoutMillis < 1000 || c.TimeoutMillis > 60000 || c.MaxContacts < 1 || c.MaxContacts > 10000 {
		return fmt.Errorf("contacts: invalid refresh, expiry, timeout, or contact bound")
	}
	return nil
}
