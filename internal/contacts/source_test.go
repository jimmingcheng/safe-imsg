package contacts

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jimmingcheng/safe-imsg/internal/config"
)

func sourceConfig() config.ContactsSource {
	cfg := config.ContactsSource{HelperPath: "/trusted/helper", ContainerID: "icloud", GroupIDs: []string{"friends", "family"}, DefaultPhoneRegion: "US"}
	cfg.ApplyDefaults()
	return cfg
}

func sourceSnapshot() Snapshot {
	return Snapshot{ContainerID: "icloud", GroupIDs: []string{"family", "friends"}, Complete: true,
		Contacts: []Contact{{ID: "person", Phones: []string{"(415) 555-0123", "+44 20 7946 0123"}, Emails: []string{"FRIEND@example.test"}}}}
}

func TestNormalizeIdentitiesAndOmitInvalidFields(t *testing.T) {
	snapshot := sourceSnapshot()
	snapshot.Contacts[0].Phones = append(snapshot.Contacts[0].Phones, "+14155550123", "555-0100", "4155550123 ext 9")
	snapshot.Contacts[0].Emails = append(snapshot.Contacts[0].Emails, "bad", "*.test@example.test")
	got, skipped, err := Identities(sourceConfig(), snapshot)
	want := []string{"+14155550123", "+442079460123", "friend@example.test"}
	if err != nil || skipped != 4 || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v skipped=%d err=%v", got, skipped, err)
	}
	if _, err := NormalizePhone("4155550123", ""); err == nil {
		t.Fatal("guessed an unconfigured phone region")
	}
	if got, err := NormalizePhone("1-415-555-0123", "CA"); err != nil || got != "+14155550123" {
		t.Fatal("NANP normalization failed")
	}
}

func TestIncompleteWrongAccountAndMalformedSnapshotsCannotGrant(t *testing.T) {
	for name, edit := range map[string]func(*Snapshot){
		"incomplete":        func(s *Snapshot) { s.Complete = false },
		"foreign account":   func(s *Snapshot) { s.ContainerID = "work" },
		"missing group":     func(s *Snapshot) { s.GroupIDs = []string{"friends"} },
		"duplicate group":   func(s *Snapshot) { s.GroupIDs = []string{"friends", "friends"} },
		"foreign group":     func(s *Snapshot) { s.GroupIDs = []string{"friends", "work"} },
		"null contacts":     func(s *Snapshot) { s.Contacts = nil },
		"duplicate contact": func(s *Snapshot) { s.Contacts = append(s.Contacts, s.Contacts[0]) },
		"null phones":       func(s *Snapshot) { s.Contacts[0].Phones = nil },
		"oversized field":   func(s *Snapshot) { s.Contacts[0].Emails = []string{strings.Repeat("x", 513)} },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := sourceSnapshot()
			edit(&snapshot)
			if _, _, err := Identities(sourceConfig(), snapshot); err == nil {
				t.Fatal("unsafe snapshot accepted")
			}
		})
	}
}

func TestAllListsDefaultAcceptsNewListsButNotUnlistedOrForeignSnapshots(t *testing.T) {
	cfg := sourceConfig()
	cfg.GroupIDs = nil
	snapshot := sourceSnapshot()
	snapshot.GroupIDs = append(snapshot.GroupIDs, "new-list")
	if _, _, err := Identities(cfg, snapshot); err != nil {
		t.Fatal("new list excluded from all-lists mode")
	}
	snapshot.GroupIDs = []string{}
	if _, _, err := Identities(cfg, snapshot); err == nil {
		t.Fatal("contacts with no lists were accepted")
	}
	snapshot.Contacts = []Contact{}
	if ids, _, err := Identities(cfg, snapshot); err != nil || len(ids) != 0 {
		t.Fatal("complete empty store did not revoke grants")
	}
	snapshot.ContainerID = "work"
	if _, _, err := Identities(cfg, snapshot); err == nil {
		t.Fatal("all-lists mode crossed the configured account boundary")
	}
}

func TestRefreshExpiryRevocationAndNoPersistentStaleGrants(t *testing.T) {
	cfg := sourceConfig()
	now := time.Unix(1000, 0)
	snapshot := sourceSnapshot()
	var fetchErr error
	manager := NewManager(cfg, func(context.Context, config.ContactsSource) (Snapshot, error) { return snapshot, fetchErr }, func() time.Time { return now })
	if _, err := manager.Get(); err == nil {
		t.Fatal("uninitialized snapshot authorized")
	}
	manager.Refresh(context.Background())
	if got, err := manager.Get(); err != nil || len(got) != 3 {
		t.Fatalf("initial refresh: %v %v", got, err)
	}
	fetchErr = Unavailable
	now = now.Add(15 * time.Minute)
	manager.Refresh(context.Background())
	if _, err := manager.Get(); err != nil || manager.Status().State != "degraded" {
		t.Fatal("lost still-fresh snapshot on transient failure")
	}
	now = time.Unix(1000, 0).Add(time.Hour)
	if _, err := manager.Get(); err == nil {
		t.Fatal("expired snapshot authorized")
	}
	fetchErr = nil
	manager.Refresh(context.Background())
	if _, err := manager.Get(); err != nil {
		t.Fatal("failed to recover")
	}
	// Successful empty membership means revocation, not a transient failure.
	snapshot.Contacts = []Contact{}
	manager.Refresh(context.Background())
	if got, err := manager.Get(); err != nil || len(got) != 0 {
		t.Fatal("empty complete snapshot retained old grants")
	}
	fetchErr = PermissionDenied
	manager.Refresh(context.Background())
	if _, err := manager.Get(); err == nil {
		t.Fatal("permission denial retained cached grants")
	}
	if manager.Status().LastErrorCode != string(PermissionDenied) {
		t.Fatal("missing health error")
	}
	encoded, _ := json.Marshal(manager.Status())
	for _, private := range []string{"friend@example.test", "icloud", "family", "identity_count"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("health leaks contact information")
		}
	}
}

func TestInvalidSnapshotFailsClosedImmediately(t *testing.T) {
	for _, code := range []Error{Invalid, PermissionRequired, PermissionDenied, SelectionMissing, Overflow, UnsafeHelper, Unsupported} {
		var err error
		manager := NewManager(sourceConfig(), func(context.Context, config.ContactsSource) (Snapshot, error) { return sourceSnapshot(), err }, nil)
		manager.Refresh(context.Background())
		err = code
		manager.Refresh(context.Background())
		if _, failure := manager.Get(); failure == nil {
			t.Fatalf("retained grants after %s", code)
		}
	}
}

func TestRefreshPublishesWholeSnapshotWithoutBlockingReaders(t *testing.T) {
	manager := NewManager(sourceConfig(), func(context.Context, config.ContactsSource) (Snapshot, error) { return sourceSnapshot(), nil }, nil)
	manager.Refresh(context.Background())
	started, finish := make(chan struct{}), make(chan struct{})
	manager.fetch = func(context.Context, config.ContactsSource) (Snapshot, error) {
		close(started)
		<-finish
		s := sourceSnapshot()
		s.Contacts = []Contact{}
		return s, nil
	}
	done := make(chan struct{})
	go func() { manager.Refresh(context.Background()); close(done) }()
	<-started
	var readers sync.WaitGroup
	for i := 0; i < 20; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			got, err := manager.Get()
			if err != nil || len(got) != 3 {
				t.Error("observed a partial refresh")
			}
			_ = manager.Status()
		}()
	}
	readers.Wait()
	close(finish)
	<-done
	if got, err := manager.Get(); err != nil || len(got) != 0 {
		t.Fatal("revocation not published")
	}
}

func TestRunCancelsNativeFetchAndTerminates(t *testing.T) {
	started := make(chan struct{})
	manager := NewManager(sourceConfig(), func(ctx context.Context, _ config.ContactsSource) (Snapshot, error) {
		close(started)
		<-ctx.Done()
		return Snapshot{}, ctx.Err()
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { manager.Run(ctx); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("source outlived broker")
	}
	if !errors.Is(ErrorCode(errors.New("PRIVATE ERROR")), Unavailable) {
		t.Fatal("unsafe error code")
	}
}
