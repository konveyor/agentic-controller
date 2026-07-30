# ADR 0007: Skill Script Execution and Staging

**Status:** Proposed
**Date:** 2026-07-28
**Authors:** Fabian von Feilitzsch

> Numbering note: 0004, 0005, and 0006 are claimed by open PRs (#47, #35, #53).

## Context

Skills are packaged as OCI images and mounted into agent pods as Kubernetes
ImageVolumes at `/opt/skills/<name>/` (`agentrun_controller.go`,
`resolveSkillVolumes`). As skills grow beyond pure prose into procedures with
concrete commands, the question arises: **can an agent execute a script that
ships with a skill?**

The concern was that skill mounts are read-only and possibly mounted `noexec`,
which would make executable payloads unusable. The concern was well-founded on
paper:

- CRI-O passes `[]string{"ro", "noexec", "nosuid", "nodev"}` to
  `Store().MountImage()` in `mountImage()` — verified identical on every branch
  from `release-1.31` through `main`.
- KEP-4639 dropped `noexec` as a *conformance requirement* when retargeting beta
  in v1.34, but that only means runtimes are no longer obliged to apply it.
  CRI-O still does.
- containerd's `container_image_mount_linux.go` shows no such flags, so the
  behaviour is **runtime-dependent, not merely version-dependent**.

Two things resolved the question, one by reframing and one by measurement.

**The reframe.** Agents do not execute scripts *from* the skill mount. They
either emit the script text from `SKILL.md` into a writable directory, or copy
it off the mount — then run it from there. The skill mount is a **read** surface,
not an **exec** surface. `noexec` blocks `execve()`; it never blocks reads. So
the mount's exec semantics do not gate the approach at all. What gates it is
whether some writable directory in the pod is executable.

**The measurement.** `hack/probe/probe.sh` was run on minikube with CRI-O
1.35.0, Kubernetes v1.34.0, ImageVolume gate BETA/enabled, against both a
hand-written pod and a real controller-created Sandbox. Both agreed:

| Location | per-mount opts | superblock | noexec reaches container | write | execve |
|---|---|---|---|---|---|
| `/opt/skills/<name>` | `rw,relatime` | `ro,seclabel,...` | **no** | EROFS | allowed |
| `/tmp` | `rw,relatime` (overlay) | `rw` | no | ok | ok |
| `/workspace` | `rw,relatime` (xfs) | `rw` | no | ok | ok |

The skill mount line, verbatim:

```
1267 1259 0:180 / /opt/skills/probe-skill rw,relatime - overlay overlay ro,seclabel,lowerdir=...
```

**`noexec` does not reach the container even under CRI-O.** CRI-O applies it to
the lowerdir image-store mount, then layers an `overlay` on top; overlayfs does
not inherit `MS_NOEXEC` from its lower mount. Read-only *is* enforced, but at the
superblock rather than in the per-mount flags — which is why writes fail with
EROFS while the per-mount options read `rw`.

Independently measured kernel semantics (ubi9-minimal, a real `noexec` tmpfs)
that hold wherever `noexec` *is* applied: `./x.sh` fails, while `sh x.sh`,
`bash x.sh`, `sh < x.sh`, `. x.sh` and `python3 x.py` all succeed — an
interpreter only reads the file. Compiled binaries fail. `chmod` on a read-only
mount fails with EROFS regardless.

## Decision

**1. Skills are a read surface. Agents stage scripts before executing them.**

Executing directly from `/opt/skills/` is not a supported pattern, even though it
currently happens to work under CRI-O. Relying on it would couple skill authoring
to a container-runtime implementation detail that KEP-4639 explicitly stopped
requiring, and that containerd and CRI-O already disagree about.

**2. The staging directory is `/tmp`, not `/workspace`.**

`/workspace` is the git worktree. The harness commits and force-pushes it, and
the filesystem watcher introduced in PR #53 auto-commits during a run — so a
script staged there can land on the user's branch. `/tmp` is writable,
executable, and outside the worktree. Both were measured exec-capable; the
difference is the commit risk, not the capability.

**3. Skill authoring guidance must state this explicitly.**

This is **measured, not predicted**. A real run — claude-sonnet-5 via goose, the
PR #53 harness, Hub-resolved application, coolstore cloned to `/workspace/repo` —
was given a skill that said only:

> 1. Write the verification script above to a file.
> 2. Make the file executable.
> 3. Run it.

with no mention of location. The model did:

```
tool: write · /workspace/repo/verify.sh
tool: shell · chmod +x /workspace/repo/verify.sh && /workspace/repo/verify.sh
```

It wrote **into the git worktree**. `/tmp` was left empty. Execution succeeded,
confirming again that the exec surface is not the constraint — placement is.

Left unsaid, the wrong behaviour is the default behaviour. The convention has to
be stated in the skill text itself; it will not be inferred.

**3b. The rule belongs in the harness, not in each skill or Agent prompt.**

Where the rule lives matters as much as its content. It is a property of the
execution environment — the working directory is a git worktree that gets pushed
— not of any particular skill, so requiring every SkillCard author (or every
Agent author) to restate it is how it gets forgotten. The harness injects it into
every prompt as a `## Working Environment` preamble in `buildPrompt`, ahead of
the Agent prompt, the playbook context, the skill, and the stage task.

**Verified.** Rerunning the identical experiment — same skill, same silent
instructions, same model, only the harness changed:

```
before:  tool: write · /workspace/repo/verify.sh
         tool: shell · chmod +x /workspace/repo/verify.sh && /workspace/repo/verify.sh

after:   tool: shell · cat > /tmp/verify.sh << 'EOF'
```

Zero writes into the repository. The proposed patch is 22 lines in
`harness/cmd/migration-harness/main.go` and applies to PR #53.

**3a. The current safety net is incidental, and should not be relied on.**

The staged `verify.sh` was *not* committed — but only because the filesystem
watcher's `ShouldStageNewFile` allowlists by extension, and `.sh` happens not to
be listed. The base set is `.md .json .yaml .yml .xml .properties .txt`, and the
Java image adds `.java .gradle .kts .kt .groovy`.

So a stray `.sh` is safe by accident, while a stray `.md`, `.json`, `.yaml` or
`.txt` scratch file **would** be auto-committed and pushed to the user's branch —
and those are exactly the extensions an agent is most likely to use for notes,
plans, and intermediate output. The allowlist is protecting the wrong things by
coincidence. Staging outside the worktree is the actual fix.

For payloads too large to inline in `SKILL.md`, ship the file in the skill and
copy it out — reading from the mount always works:

```sh
cp /opt/skills/<name>/helper.sh /tmp/ && chmod +x /tmp/helper.sh && /tmp/helper.sh
```

**4. Skills do not ship compiled binaries.**

Binaries cannot run from a `noexec` mount at all, must be built per
architecture, and would make skills non-portable across the runtimes we intend to
support. Tools belong in the agent image, which is where the air-gap and image
composition story already lives (ADR 0001).

## Alternatives Considered

**Bake the executable bit into the OCI layer and execute in place.** Works today
under CRI-O, and `skillctl` already preserves modes via `tar.FileInfoHeader`
(`pkg/oci/build.go`). Rejected: it depends on `noexec` not being applied, which
is a runtime implementation detail rather than a guarantee. A hardened cluster,
a policy layer such as OpenShell (PR #47, which explicitly covers "binary
restrictions"), or a future CRI-O change would break every such skill at once.

**Have the harness stage `/opt/skills/*/scripts/` into a writable exec directory
at startup.** Robust, and it would make the agent's natural `./foo.sh` reflex
work. Rejected as unnecessary: agents already write scripts themselves, so this
adds a harness mechanism to solve a problem that does not exist. Worth revisiting
only if skills ever need to ship executable payloads.

**Mount skills exec-capable.** Not available: `ImageVolumeSource` exposes only
`reference` and `pullPolicy`. There is no mount-options knob.

## Consequences

- Skill authors write scripts as *content*, and the invocation convention is
  staging to `/tmp`. This must appear in authoring documentation and ideally in
  the stage skills themselves.
- The platform is insensitive to whether a given cluster applies `noexec` to
  image volumes, which removes a runtime-dependent variable from the support
  matrix.
- `hack/probe/run-probe.sh` re-answers the question on any cluster. It should be re-run
  against real OpenShift before dev preview, and whenever the target platform
  version moves — the finding here is from minikube/CRI-O 1.35.0 and is evidence,
  not a guarantee.
- The only outcome that would invalidate this decision is
  `BLOCKED_NO_EXEC_SURFACE`: a cluster where nothing writable is executable.
  Not observed, but plausible on hardened clusters that mount emptyDir `noexec`.

## Appendix: the injected rules

Verified text, added to `buildPrompt` in the harness ahead of every other
context layer. Reproduced here so the decision survives independently of any
particular branch.

```go
const stagingRules = `## Working Environment

Your working directory is a git repository. When this stage finishes it is
committed and pushed to the user's branch, so anything you leave in it ships to
the user.

Write ephemeral files to /tmp, never into the repository working tree:
  - scripts you need to run: write them to /tmp, make them executable there,
    and run them from there
  - scratch notes, plans, logs, and intermediate output

Only files the user actually asked for belong in the repository. This applies
even when a skill's instructions do not say where to put something.
`
```

## References

- `hack/probe/probe.sh`, `hack/probe/run-probe.sh` — the probe
- KEP-4639 (OCI VolumeSource); CRI-O `server/container_create_linux.go`
