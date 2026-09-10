// Package contacts derives bounded DM grants from the owner's macOS lists.
// It does not edit Contacts, persist an address book, or expose contact RPCs.
package contacts

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jimmingcheng/safe-imsg/internal/config"
	"github.com/jimmingcheng/safe-imsg/internal/policy"
)

type Error string

func (e Error) Error() string { return string(e) }

const (
	Unavailable        Error = "contacts_unavailable"
	Invalid            Error = "contacts_invalid_response"
	PermissionRequired Error = "contacts_permission_required"
	PermissionDenied   Error = "contacts_permission_denied"
	SelectionMissing   Error = "contacts_selection_missing"
	Overflow           Error = "contacts_overflow"
	StoreChanged       Error = "contacts_store_changed"
	Timeout            Error = "contacts_timeout"
	UnsafeHelper       Error = "contacts_unsafe_helper"
	Unsupported        Error = "contacts_unsupported_platform"
	InvalidConfig      Error = "contacts_invalid_config"
)

type Contact struct {
	ID     string   `json:"id"`
	Phones []string `json:"phones"`
	Emails []string `json:"emails"`
}

type Snapshot struct {
	ContainerID string    `json:"container_id"`
	GroupIDs    []string  `json:"group_ids"`
	Contacts    []Contact `json:"contacts"`
	Complete    bool      `json:"complete"`
}

type FetchFunc func(context.Context, config.ContactsSource) (Snapshot, error)

// NormalizePhone permits national NANP numbers only with an explicit owner
// region. Extensions, ambiguous short numbers, and guessed countries are not grants.
func NormalizePhone(raw, region string) (string, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "+") && (region == "US" || region == "CA") {
		var digits strings.Builder
		for _, r := range value {
			switch {
			case r >= '0' && r <= '9':
				digits.WriteRune(r)
			case r == ' ' || r == '(' || r == ')' || r == '-' || r == '.':
			default:
				return "", Invalid
			}
		}
		value = digits.String()
		if len(value) == 10 {
			value = "1" + value
		}
		if len(value) != 11 || value[0] != '1' || value[1] < '2' || value[4] < '2' {
			return "", Invalid
		}
		value = "+" + value
	}
	return policy.NormalizeIdentity(value)
}

// Identities rejects incomplete/mismatched snapshots before granting anything.
// Invalid individual phone/email fields are omitted and counted for owner preview.
func Identities(cfg config.ContactsSource, snapshot Snapshot) ([]string, int, error) {
	if !snapshot.Complete || snapshot.ContainerID != cfg.ContainerID || snapshot.Contacts == nil || len(snapshot.Contacts) > cfg.MaxContacts || snapshot.GroupIDs == nil || len(snapshot.GroupIDs) > 100 || (cfg.GroupIDs != nil && len(snapshot.GroupIDs) != len(cfg.GroupIDs)) {
		return nil, 0, Invalid
	}
	want := map[string]bool{}
	for _, id := range cfg.GroupIDs {
		want[id] = true
	}
	seenGroups := map[string]bool{}
	for _, id := range snapshot.GroupIDs {
		if id == "" || len(id) > 512 || seenGroups[id] || (cfg.GroupIDs != nil && !want[id]) {
			return nil, 0, Invalid
		}
		seenGroups[id] = true
		delete(want, id)
	}
	if len(snapshot.GroupIDs) == 0 && len(snapshot.Contacts) != 0 {
		return nil, 0, Invalid
	}
	seenContacts, identities := map[string]bool{}, map[string]bool{}
	skipped := 0
	for _, contact := range snapshot.Contacts {
		if contact.ID == "" || len(contact.ID) > 512 || seenContacts[contact.ID] || contact.Phones == nil || contact.Emails == nil || len(contact.Phones) > 100 || len(contact.Emails) > 100 {
			return nil, 0, Invalid
		}
		seenContacts[contact.ID] = true
		for _, phone := range contact.Phones {
			if len(phone) > 512 {
				return nil, 0, Invalid
			}
			normalized, err := NormalizePhone(phone, cfg.DefaultPhoneRegion)
			if err != nil || !strings.HasPrefix(normalized, "+") {
				skipped++
				continue
			}
			identities[normalized] = true
		}
		for _, email := range contact.Emails {
			if len(email) > 512 {
				return nil, 0, Invalid
			}
			normalized, err := policy.NormalizeIdentity(email)
			if err != nil || !strings.Contains(normalized, "@") {
				skipped++
				continue
			}
			identities[normalized] = true
		}
	}
	result := make([]string, 0, len(identities))
	for value := range identities {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, skipped, nil
}

type Status struct {
	State         string `json:"state"`
	LastAttemptAt string `json:"last_attempt_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	LastErrorCode string `json:"last_error_code,omitempty"`
}

type Manager struct {
	cfg                      config.ContactsSource
	fetch                    FetchFunc
	now                      func() time.Time
	refresh                  sync.Mutex
	mu                       sync.RWMutex
	identities               []string
	lastAttempt, lastSuccess time.Time
	lastError                string
}

func NewManager(cfg config.ContactsSource, fetch FetchFunc, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{cfg: cfg, fetch: fetch, now: now}
}

// Run is owned by the broker's lifetime; the client cannot trigger refreshes.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(m.cfg.RefreshSeconds) * time.Second)
	defer ticker.Stop()
	for {
		m.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) Refresh(ctx context.Context) {
	m.refresh.Lock()
	defer m.refresh.Unlock()
	started := m.now()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.cfg.TimeoutMillis)*time.Millisecond)
	defer cancel()
	snapshot, err := m.fetch(ctx, m.cfg)
	var values []string
	if err == nil {
		values, _, err = Identities(m.cfg, snapshot)
	}
	if ctx.Err() != nil {
		err = Timeout
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAttempt = started
	if err == nil {
		m.identities, m.lastSuccess, m.lastError = values, m.now(), ""
		return
	}
	code := ErrorCode(err)
	m.lastError = string(code)
	// Explicit revocation, changed selection and untrustworthy output invalidate
	// immediately. Only transient failures may use a still-unexpired snapshot.
	if code != Unavailable && code != Timeout && code != StoreChanged {
		m.identities = nil
	}
}

func ErrorCode(err error) Error {
	var code Error
	if errors.As(err, &code) {
		switch code {
		case Unavailable, Invalid, PermissionRequired, PermissionDenied, SelectionMissing, Overflow, StoreChanged, Timeout, UnsafeHelper, Unsupported, InvalidConfig:
			return code
		}
	}
	return Unavailable
}

func (m *Manager) available(now time.Time) bool {
	return m.identities != nil && !now.Before(m.lastSuccess) && now.Before(m.lastSuccess.Add(time.Duration(m.cfg.MaxAgeSeconds)*time.Second))
}

func (m *Manager) Get() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.available(m.now()) {
		return nil, Unavailable
	}
	return append([]string{}, m.identities...), nil
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := Status{State: "unavailable", LastErrorCode: m.lastError}
	if m.available(m.now()) {
		status.State = "ready"
		if m.lastError != "" {
			status.State = "degraded"
		}
	}
	if !m.lastAttempt.IsZero() {
		status.LastAttemptAt = m.lastAttempt.UTC().Format(time.RFC3339)
	}
	if !m.lastSuccess.IsZero() {
		status.LastSuccessAt = m.lastSuccess.UTC().Format(time.RFC3339)
		status.ExpiresAt = m.lastSuccess.Add(time.Duration(m.cfg.MaxAgeSeconds) * time.Second).UTC().Format(time.RFC3339)
	}
	return status
}
