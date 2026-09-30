package git

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
)

var (
	// ErrNoWriteAccess marks a remote that refused the credential the
	// harness resolved outright: the push at the end of the run is going
	// to fail, so the run should not be started. Returned errors wrap it.
	ErrNoWriteAccess = errors.New("no write access")

	// ErrWriteAccessUnknown marks a probe that reached no verdict — a
	// transport hiccup, an unexpected status, a server that will not talk
	// about receive-pack. Nothing was proved either way, and the caller
	// must not turn it into a refusal: the clone already succeeded, so
	// failing the run here would kill work over an inconclusive round
	// trip. Returned errors wrap it.
	ErrWriteAccessUnknown = errors.New("write access undetermined")
)

// CheckWriteAccess proves the resolved credential may push to
// cred.RepoURL before the run spends any LLM turns on work the final
// push would then throw away (issue #247).
//
// The probe is the reference-discovery half of a push: a
// git-receive-pack session, which is the first thing Push's own
// PushContext opens. Over HTTP that is
// GET <repo>/info/refs?service=git-receive-pack — the request a public
// GitHub repository answers with 401 "No anonymous write access." for
// an anonymous run, verbatim the failure #247 reported from the final
// push, and with 403 for a token without push rights. It is strictly a
// write probe: git-upload-pack (the read service) is never contacted,
// so a repository that is world-readable but not writable fails here
// exactly as it would at push time. Nothing is written to the remote —
// no ref update is sent and no packfile follows.
//
// What it does not prove: that one particular ref update will be
// accepted. Branch protection rules and pre-receive hooks only run once
// the update itself arrives. Those are per-branch policy; the missing
// or read-only credential #247 is about is caught here.
//
// Only an explicit refusal (the server demanded credentials, or
// rejected the ones given) wraps ErrNoWriteAccess. Every other failure
// wraps ErrWriteAccessUnknown — see that sentinel.
func CheckWriteAccess(ctx context.Context, cred *Credentials) error {
	ep, err := transport.NewEndpoint(cred.RepoURL)
	if err != nil {
		return fmt.Errorf("%w: endpoint %s: %w", ErrWriteAccessUnknown, cred.RepoURL, err)
	}

	tr, err := client.NewClient(ep)
	if err != nil {
		return fmt.Errorf("%w: transport for %s: %w", ErrWriteAccessUnknown, cred.RepoURL, err)
	}

	session, err := tr.NewReceivePackSession(ep, cred.Auth())
	if err != nil {
		return classifyProbeError(cred.RepoURL, err)
	}
	defer func() { _ = session.Close() }()

	// An empty remote is a legitimate push target — it is the fetch side
	// that treats a refless advertisement as an error — so accept it.
	_, err = session.AdvertisedReferencesContext(ctx)
	if err != nil && !errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return classifyProbeError(cred.RepoURL, err)
	}
	return nil
}

// classifyProbeError separates "the remote refused this credential" from
// "the probe learned nothing". go-git maps 401 to
// ErrAuthenticationRequired and 403 to ErrAuthorizationFailed, which is
// precisely the two-case split #247 asks the run to report; anything
// else is inconclusive and must not fail a run on its own.
func classifyProbeError(repoURL string, err error) error {
	if errors.Is(err, transport.ErrAuthenticationRequired) ||
		errors.Is(err, transport.ErrAuthorizationFailed) {
		return fmt.Errorf("%w: %s: %w", ErrNoWriteAccess, repoURL, err)
	}
	return fmt.Errorf("%w: %s: %w", ErrWriteAccessUnknown, repoURL, err)
}
