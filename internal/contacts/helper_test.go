package contacts

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestHelperResponseSchemaAndSanitizedErrors(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          error
	}{
		{"success", `{"v":1,"ok":true,"result":{"container_id":"icloud","group_ids":["family","friends"],"contacts":[],"complete":true}}`, nil},
		{"denied", `{"v":1,"ok":false,"code":"contacts_permission_denied"}`, PermissionDenied},
		{"leak", `{"v":1,"ok":false,"code":"PRIVATE ADDRESS BOOK"}`, Invalid},
		{"extra", `{"v":1,"ok":true,"result":{"contacts":[],"complete":true,"raw_notes":"PRIVATE"}}`, Invalid},
		{"invalid", `PRIVATE MALFORMED`, Invalid},
		{"trailing", `{"v":1,"ok":true,"result":{}} {}`, Invalid},
		{"wrong version", `{"v":2,"ok":false,"code":"contacts_unavailable"}`, Invalid},
		{"ambiguous", `{"v":1,"ok":false,"result":{},"code":"contacts_unavailable"}`, Invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result Snapshot
			err := decodeHelperResponse([]byte(tc.payload), &result)
			if err != tc.want {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if err == nil {
				if _, _, err := Identities(sourceConfig(), result); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestUnsupportedPlatformsCannotUseNativeHelper(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("covered by macOS native tests")
	}
	if err := CheckHelper("/fake/helper"); err != Unsupported {
		t.Fatalf("got %v", err)
	}
}

func TestTrustedHelperTransportOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native platform check")
	}
	for _, path := range []string{"relative/helper", "/tmp/../helper", "/"} {
		if err := CheckHelper(path); err != UnsafeHelper {
			t.Fatalf("unsafe path accepted: %s", path)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckHelper(executable); err != UnsafeHelper {
		t.Fatal("non-bundled executable accepted")
	}
	helper := os.Getenv("SAFE_IMSG_CONTACTS_APP_EXECUTABLE")
	if helper == "" {
		t.Skip("set SAFE_IMSG_CONTACTS_APP_EXECUTABLE for native app transport integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var result Snapshot
	// An unknown operation is rejected before any Contacts access. This exercises
	// the real signed GUI app and socket transport without reading personal data.
	if err := runHelper(ctx, helper, map[string]any{"v": 1, "operation": "synthetic-invalid"}, &result); err != Invalid {
		t.Fatalf("native transport: %v", err)
	}
}
