package kai

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadHubTokenRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kai")
	want := hubCredentials{HubURL: "https://hub.example.com", Token: "pat-abc123"}
	if err := saveHubToken(dir, want); err != nil {
		t.Fatalf("saveHubToken: %v", err)
	}

	// The file must not be world/group readable.
	info, err := os.Stat(filepath.Join(dir, hubTokenFile))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file perm = %o, want 600", perm)
	}

	got, err := loadHubToken(dir)
	if err != nil {
		t.Fatalf("loadHubToken: %v", err)
	}
	if got.HubURL != want.HubURL || got.Token != want.Token {
		t.Errorf("round-trip mismatch: got %#v, want %#v", got, want)
	}
}

func TestLoadHubTokenMissingFile(t *testing.T) {
	// A missing file yields the zero value and no error so runs stay clean.
	got, err := loadHubToken(t.TempDir())
	if err != nil {
		t.Fatalf("loadHubToken on missing file: %v", err)
	}
	if got.Token != "" {
		t.Errorf("expected empty token, got %q", got.Token)
	}
}

func TestDeleteHubToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kai")
	if err := saveHubToken(dir, hubCredentials{Token: "x"}); err != nil {
		t.Fatalf("saveHubToken: %v", err)
	}
	if err := deleteHubToken(dir); err != nil {
		t.Fatalf("deleteHubToken: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, hubTokenFile)); !os.IsNotExist(err) {
		t.Errorf("token file still present after delete: %v", err)
	}
	// Deleting an already-absent file is not an error.
	if err := deleteHubToken(dir); err != nil {
		t.Errorf("deleteHubToken on missing file: %v", err)
	}
}
