# ADR 0017: Process Isolation for Git Credentials from the Goose Agent

**Status:** proposed — revises ADR 0007's credential-isolation claim
**Date:** 2026-08-14
**Relates to:** ADR 0007 (harness thin runner), ADR 0008 (harness owns
pod ACP port), ADR 0016 (harness git via Hub's shared scm)
**Authors:** Savitha Raghunathan

## Context

ADR 0007 states, as an accepted decision: *"Git push credentials are
stripped from the cloned repo's remote... goose and any skill content it
executes cannot access Hub or push credentials."*

ADR 0016 moves the harness's git backend to `tackle2-hub/shared/scm`,
which authenticates via a `credential.helper = store --file=...` — a
real file (`scmHome/.git-credentials`) holding the token in cleartext
for the run's duration. Verified empirically. Previously (go-git), the
token lived in memory only, attached per-HTTP-request; nothing was ever
written to disk.

`goose` — the AI agent process that executes the actual migration work,
including arbitrary tool calls and skill-authored scripts — runs as a
**subprocess of the harness, sharing its OS UID**. Confirmed:
`harness/internal/goose/lifecycle.go` builds goose's environment from
`os.Environ()`; no UID-changing logic exists anywhere in the harness.
Unix file permissions do not distinguish between processes sharing a
UID — `chmod` on the credential file changes nothing for `goose`
specifically, regardless of mode. ADR 0007's claim that goose "cannot
access push credentials" is no longer true.

Today's Sandbox pod (`agentrun_controller.go`'s `createSandbox`) is a
single container named `agent`, running as a fixed non-root UID (`1001`,
from `images/agent-base/Containerfile`'s `USER 1001`). No pod or
container `securityContext` is set anywhere — the non-root posture comes
entirely from the image. The external CRD this operator builds
(`sigs.k8s.io/agent-sandbox v0.5.0`) types its pod field as a full,
unmodified `corev1.PodSpec`, so nothing upstream blocks adding a second
container or a `securityContext` — this repo's own `createSandbox` is
the only place that would need to change.

## Decision

**Move `goose` into its own sidecar container in the Sandbox pod, under
a distinct, unprivileged UID, with the `harness` container holding no
elevated capability at all.**

- `agent-base` gains a second unprivileged user (e.g. `useradd -u 1002
  -g 0 -d /home/goose -s /sbin/nologin goose`), mirroring exactly how
  the existing `harness` user (`1001`) is created today.
- `createSandbox` adds a `goose` container to `Containers[]` alongside
  `agent` (renamed conceptually to `harness`), each with its own
  `securityContext.runAsUser`.
- `/workspace` (the `EmptyDir` holding the cloned repo) stays shared
  between both containers. It already uses `chown -R 1001:0` +
  `chmod -R g=u` — group `0`, group bits mirroring owner bits — which is
  the same mechanism OpenShift uses to let an arbitrary assigned UID
  read/write it. Adding `goose` (UID `1002`, group `0`) to that scheme
  needs no permission change, only confirming it in practice.
- The credential home (`scmHome`) is **not** given the group-0 treatment.
  It stays owned by the `harness` container's specific UID, mode `0700`
  — no group bits at all — so `goose`'s different UID has no path to it
  even though both UIDs share group `0` elsewhere.
- Communication between the two containers reuses the ACP-over-loopback
  design already established by ADR 0008: containers in a pod share a
  network namespace, so `127.0.0.1:4000`/`:4001` needs no change to work
  across the container boundary.
- `goose` gets its own `$HOME` (e.g. `/home/goose`), separate from the
  harness's, for language-toolchain caches (`.m2`, `.npm`, `.cache`)
  that currently default into the shared one.

This closes the gap using the OS's actual permission model — file mode
bits mean nothing between processes sharing a UID, but mean everything
between different UIDs — without granting either container any
capability it doesn't already effectively have today.

## Alternatives Considered

Every option considered, with the trade-off that decided against it.

### 1. Cosmetic mitigation: `chmod 0700` + `defer repo.Clean()`

Tighten the credential home's permissions and delete it promptly on
exit.

- **Pros:** cheap, already-written, no design risk.
- **Cons:** `chmod 0700` restricts *other* UIDs, not `goose`'s — file
  permission bits don't distinguish between processes sharing a UID
  regardless of mode. `Clean()` shrinks the exposure window (no
  credential file surviving a crash) but does nothing while the process
  is running, which is when `goose` is actually active.
- **Rejected as a primary fix; adopted anyway as free defense-in-depth**
  underneath whichever primary option is chosen — shrinking the window
  costs nothing and is strictly better than not doing it.

### 2. Non-file credential helper (`GIT_ASKPASS` + per-subprocess env)

Resolve the git token via `GIT_ASKPASS`, passed only through the `Env`
of the one git subprocess that needs it — never the harness's own
ambient environment, never a file.

- **Pros:** if achievable, meaningfully shrinks exposure (a transient
  env var on a short-lived subprocess vs. a file readable for the
  entire run) without any UID or pod-topology change.
- **Cons:** not achievable without giving something up. `scm.Git.Fetch`
  / `Push` / `Commit` all call `initHome()` internally, which
  unconditionally writes the credential-helper file to disk — that's
  inside the vendored package, not something `Repository` can opt out
  of while still calling those methods. Doing this for real means not
  using `scm.Git`'s authenticated operations at all: reimplementing
  clone/push ourselves against a custom `GIT_ASKPASS`, for exactly the
  operations ADR 0016 adopted `scm.Git` to handle.
- **Rejected:** undoes most of ADR 0016's point for the operations that
  matter most. Revisit only if `tackle2-hub/shared/scm` grows a
  non-file credential path upstream.

### 3. Same-pod UID switch: grant `CAP_SETUID`/`CAP_SETGID` to the harness

Keep one container. The harness process itself calls
`exec.Cmd.SysProcAttr.Credential` to launch `goose` under a different
UID, which requires `CAP_SETUID`/`CAP_SETGID` granted via
`securityContext.capabilities.add`.

- **Pros:** no second container, no pod-topology change, no new image
  user needed on the pod-spec side (the harness spawns straight into
  the new UID). Smallest diff to `createSandbox`.
- **Cons:** the capability is held by the *entire* long-running,
  network-facing harness process — the same process that calls the Hub
  API, handles the ACP WebSocket, and parses content it doesn't fully
  control. `CAP_SETUID` lets a process become *any* UID; if the harness
  is ever compromised through any of that surface, the compromise now
  carries the power to impersonate arbitrary UIDs, not just whatever
  UID 1001 could already do. This is a broad, standing privilege on a
  large codebase, not a narrow one.
- **Rejected:** trades a narrow, contained problem (goose can read one
  file) for a broader one (the harness itself becomes a more valuable
  target). Also cuts against this project's own demonstrated posture —
  `config/manager/manager.yaml` runs the controller-manager under PSS
  "restricted" with no added capabilities.

### 4. Same-pod UID switch via a minimal setuid-root helper binary

Keep one container. Instead of granting the capability to the harness
itself, build a tiny, single-purpose setuid-root binary (`chmod u+s`,
owned by root) whose only job is: drop to the target UID, `exec(goose)`,
done. The classical `sudo`/`ping`/`mount` pattern.

- **Pros:** narrower than option 3 — the privileged surface is a small,
  short-lived, single-purpose binary instead of a capability held by the
  entire harness process for its whole lifetime.
- **Cons:** still puts a setuid-root binary inside the runtime image.
  That's a privilege-escalation primitive baked into the artifact
  itself, independent of who holds it — the kind of thing hardened
  container baselines and image scanners commonly flag or forbid
  outright, for the same underlying reason capabilities are avoided
  elsewhere in this project. One more artifact to build, sign, and
  maintain going forward.
- **Rejected:** smaller than option 3 but the same category of cost, for
  the same category of benefit as the sidecar — and the sidecar gets
  the same isolation without introducing any privileged binary at all.

### 5. Reduce blast radius instead of preventing the read (short-lived, narrowly-scoped credential)

Independent of *who* can read the file: make the token itself worth less
if read — single-repo, single-branch scope, short expiry.

- **Pros:** genuinely complementary to whichever option above is chosen;
  doesn't require picking between them. Mirrors the existing pattern for
  the Hub API token, which is already actively revoked
  (`hubClient.RevokeToken`) rather than left to expire on its own TTL.
- **Cons:** doesn't prevent the read, only bounds its value. Credential
  issuance is Hub's responsibility (`hubClient.FetchGitCreds`), not this
  repo's — whether Hub can mint scoped, short-lived git credentials
  per-run is outside what this ADR can decide.
- **Not adopted here, recommended as a parallel follow-up**: file against
  Hub's credential-issuance path, independent of and not blocking this
  ADR's decision.

## Consequences

- `agent-base` (and its language variants) gain a second unprivileged
  user; the Sandbox pod gains a second container and its first-ever
  `securityContext` usage — a precedent this operator hasn't set before.
- `createSandbox` needs shared-volume permissions (`/workspace`)
  verified in practice across the two UIDs, not just inferred from the
  existing `g=u` convention.
- `goose` needs its own `$HOME`; anything currently assuming the
  harness's `$HOME` is also goose's `$HOME` needs to be found and fixed.
- The harness container's own privilege level is unchanged — no added
  capability, no setuid binary. The isolation cost is paid in pod
  topology, not in the harness's own attack surface.
- ADR 0007's credential-isolation claim is corrected: Hub credentials
  are isolated from `goose` (unaffected by this ADR); git push
  credentials are isolated by this ADR's sidecar split, not by the
  environment-variable convention ADR 0007 originally described.
- `chmod 0700` on `scmHome` and `defer repo.Clean()` are adopted
  alongside the sidecar split as free, no-cost defense-in-depth — not a
  substitute for it.
- Scoping down the git credential itself (short-lived, single-repo) is
  recommended as a follow-up against Hub's credential-issuance path,
  independent of this ADR.
