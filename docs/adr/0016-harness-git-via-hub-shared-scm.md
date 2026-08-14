# ADR 0016: Harness Git Operations via Hub's Shared SCM Package

**Status:** proposed
**Date:** 2026-08-14
**Relates to:** issue #85
**Authors:** Savitha Raghunathan

## Context

- The harness reimplements git plumbing in-process against
  [go-git](https://github.com/go-git/go-git) (`harness/internal/git/git.go`,
  `credentials.go`): clone, configure author, strip push credentials,
  checkout branch, commit, push.
- Hub (`github.com/konveyor/tackle2-hub`) already implements the same
  operation set for its own addons in `shared/scm`, by shelling out to
  the system `git` binary rather than reimplementing git in-process. Two
  independently maintained git backends in the same project accrue
  separate bugs and separate behavior drift for the same job.
- Issue #85: replace go-git with Hub's `shared/scm` workflow for clone
  and other git operations.

## Decision

**The harness's git backend is `tackle2-hub/shared/scm.Git`, wrapped by
`Repository`** (`harness/internal/git/repository.go`). `git.go` and
`credentials.go` are deleted; nothing in the harness talks to go-git.

`Repository` exposes `Fetch`, `Branch`, `CommitAndPush`, `Push`,
`ConfigureAuthor`, `IsDirty`, `HeadSHA`, `Clean` — the same operation set
the harness needs, backed by the system `git` binary instead of an
in-process reimplementation.

### Branch creation is always local-only

`Repository.Branch` checks out `ref`; if it doesn't exist yet, it
creates it with a local `git checkout -b ref` and stops there — no push.
It never uses `scm.Git`'s `CREATE` option, which pushes the new branch
immediately and unconditionally, before any commits exist. Branch
creation and publishing a branch are two separate decisions; `Push`
alone decides whether a branch is worth publishing.

### Push publishes only real work

`Repository.Push(ctx, baseSHA)` compares current `HEAD` against the base
captured at the start of the run and skips the push when they're equal,
logging `"no commits produced; skipping push"`. This is what keeps
no-op, refused, and skill-less runs from littering the remote with empty
branches (PR #114) — and is exactly why branch creation has to stay
local-only above: the empty-branch check only works if nothing pushes
before it runs.

### Push is not context-bounded

`scm.Git.Push()` takes no `context.Context` — it runs under
`context.TODO()` three calls deep in `tackle2-hub/shared/command`.
`Repository.Push` calls it directly, so a stalled push can no longer be
cancelled by the caller's `ctx` or bounded by the harness's 25-second
final-push timeout (`main.go`'s `pushCtx`). Accepted for now; documented
at the call site with the concrete restoration path (reintroduce
`exec.CommandContext(ctx, "git", "push", ...)`) if a hung push is ever
observed blocking pod exit in production.

### Credentials no longer need stripping from the clone

`StripCredentials` existed because go-git's clone could leave a
credential-bearing URL in the cloned repo's own `.git/config`. `scm.Git`
never puts one there: it authenticates via a `credential.helper` file in
a separate home directory, not by embedding the token in the remote URL.
Verified by running a fake identity through the full `Repository`
lifecycle (fetch, branch, commit, push) and inspecting the result —
nothing in `cloneDir` carries the token. `StripCredentials` and its
URL-parsing logic are deleted, not ported.

That credential-helper file is itself a new consideration — it's a real
file on disk where go-git kept the token in memory only — and who can
read it is the subject of ADR 0017, not this one.

## Alternatives Considered

**Keep go-git.** Rejected: perpetuates two independently maintained git
backends for the same operation set, forgoing any shared hardening from
Hub's own addon experience.

## Consequences

- One git backend (`scm.Git`) instead of two (go-git and `scm.Git`)
  across Hub and the harness.
- `StripCredentials` and its URL-parsing logic are gone, not ported —
  nothing in the new design writes credentials into a repo's own git
  config for it to strip.
- The empty-branch-push safety from PR #114 survives the migration,
  verified by `TestPushSkipsWhenNoNewCommits` and
  `TestPushWithNewCommitsPushes`.
- `Push` is no longer cancellable or time-bounded by the caller, until
  `scm.Git` gains context support or the `exec.CommandContext` fallback
  is restored in `repository.go`.
- Git credentials move from in-memory-only to a file on disk
  (`scm.Git`'s credential-helper store). ADR 0017 decides who is allowed
  to read that file and revises ADR 0007's credential-isolation claim
  accordingly.
