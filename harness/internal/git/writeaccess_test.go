package git

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// receivePackProbe records what CheckWriteAccess asked the remote for and
// answers with the given status, standing in for a server that refuses
// the write service while still serving reads.
type receivePackProbe struct {
	status  int
	body    string
	service string
	auth    string
	calls   int
}

func (p *receivePackProbe) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.calls++
		p.service = r.URL.Query().Get("service")
		p.auth = r.Header.Get("Authorization")
		w.WriteHeader(p.status)
		_, _ = w.Write([]byte(p.body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/acme/app.git"
}

// The probe must hit git-receive-pack. git-upload-pack would succeed on
// every public repository and prove nothing about pushing (#247).
func TestCheckWriteAccessProbesTheWriteService(t *testing.T) {
	probe := &receivePackProbe{status: http.StatusUnauthorized, body: "No anonymous write access.\n"}
	url := probe.start(t)

	err := CheckWriteAccess(context.Background(), &Credentials{RepoURL: url, Branch: "migration-1"})
	if err == nil {
		t.Fatal("CheckWriteAccess should fail when the remote refuses receive-pack")
	}
	if probe.service != transport.ReceivePackServiceName {
		t.Errorf("probed service = %q, want %q", probe.service, transport.ReceivePackServiceName)
	}
	if probe.calls != 1 {
		t.Errorf("remote round trips = %d, want 1", probe.calls)
	}
}

func TestCheckWriteAccessAnonymousRejected(t *testing.T) {
	probe := &receivePackProbe{status: http.StatusUnauthorized, body: "No anonymous write access.\n"}
	url := probe.start(t)

	err := CheckWriteAccess(context.Background(), &Credentials{RepoURL: url, Branch: "migration-1"})
	if err == nil {
		t.Fatal("CheckWriteAccess should fail for an anonymous run against a write-protected remote")
	}
	if !errors.Is(err, ErrNoWriteAccess) {
		t.Errorf("error %v does not wrap ErrNoWriteAccess", err)
	}
	if !errors.Is(err, transport.ErrAuthenticationRequired) {
		t.Errorf("error %v does not wrap transport.ErrAuthenticationRequired", err)
	}
	if probe.auth != "" {
		t.Errorf("Authorization header = %q, want none for an anonymous credential", probe.auth)
	}
}

func TestCheckWriteAccessReadOnlyTokenRejected(t *testing.T) {
	probe := &receivePackProbe{status: http.StatusForbidden, body: "Write access to repository not granted.\n"}
	url := probe.start(t)

	cred := &Credentials{
		Username:     "x-access-token",
		Token:        "read-only",
		RepoURL:      url,
		Branch:       "migration-1",
		IdentityName: "github-readonly",
	}
	err := CheckWriteAccess(context.Background(), cred)
	if err == nil {
		t.Fatal("CheckWriteAccess should fail for a token the remote will not let push")
	}
	if !errors.Is(err, ErrNoWriteAccess) {
		t.Errorf("error %v does not wrap ErrNoWriteAccess", err)
	}
	if !errors.Is(err, transport.ErrAuthorizationFailed) {
		t.Errorf("error %v does not wrap transport.ErrAuthorizationFailed", err)
	}
	if probe.auth == "" {
		t.Error("the probe must present the resolved credential, not fall back to anonymous")
	}
}

// A remote that does open receive-pack passes, and the probe leaves it
// untouched: no ref is created and no commit appears.
func TestCheckWriteAccessWritableRemotePasses(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)
	seedBareRepo(t, remoteDir)

	cred := &Credentials{RepoURL: remoteDir, Branch: "migration-1"}
	if err := CheckWriteAccess(context.Background(), cred); err != nil {
		t.Fatalf("CheckWriteAccess: %v", err)
	}

	_, repo := cloneLocal(t, remoteDir)
	refs, err := repo.References()
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if err := refs.ForEach(func(r *plumbing.Reference) error {
		if r.Name().Short() == "migration-1" {
			t.Errorf("the probe created %s on the remote; it must not write", r.Name())
		}
		return nil
	}); err != nil {
		t.Fatalf("ForEach: %v", err)
	}
}

// An empty repository is a legitimate push target: the refless
// advertisement must not be mistaken for a refusal.
func TestCheckWriteAccessEmptyRemotePasses(t *testing.T) {
	remoteDir, _ := setupBareRemote(t)

	cred := &Credentials{RepoURL: remoteDir, Branch: "migration-1"}
	if err := CheckWriteAccess(context.Background(), cred); err != nil {
		t.Fatalf("CheckWriteAccess on an empty remote: %v", err)
	}
}

// A probe that reaches no verdict must say so rather than accuse the
// credential: the clone already succeeded, so the caller has to be able
// to tell "the remote refused you" from "the round trip proved nothing"
// and keep the run alive for the latter.
func TestCheckWriteAccessMissingRemoteIsInconclusive(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere.git")

	err := CheckWriteAccess(context.Background(), &Credentials{RepoURL: missing, Branch: "migration-1"})
	if err == nil {
		t.Fatal("CheckWriteAccess should fail when the remote does not exist")
	}
	if !errors.Is(err, ErrWriteAccessUnknown) {
		t.Errorf("error %v does not wrap ErrWriteAccessUnknown", err)
	}
	if errors.Is(err, ErrNoWriteAccess) {
		t.Errorf("error %v wraps ErrNoWriteAccess; an unreachable remote is not a refused credential", err)
	}
}

func TestCheckWriteAccessServerErrorIsInconclusive(t *testing.T) {
	probe := &receivePackProbe{status: http.StatusInternalServerError, body: "boom\n"}
	url := probe.start(t)

	err := CheckWriteAccess(context.Background(), &Credentials{RepoURL: url, Branch: "migration-1"})
	if err == nil {
		t.Fatal("CheckWriteAccess should report a 500 rather than passing silently")
	}
	if !errors.Is(err, ErrWriteAccessUnknown) {
		t.Errorf("error %v does not wrap ErrWriteAccessUnknown", err)
	}
	if errors.Is(err, ErrNoWriteAccess) {
		t.Errorf("error %v wraps ErrNoWriteAccess; a 5xx says nothing about the credential", err)
	}
}
