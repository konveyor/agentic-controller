# Creating agentic base images

This guide explains how to build an agent image that an `Agent` can use.
The image contains the runtime and language/toolchain dependencies. Skills
are delivered separately by `SkillCard` resources and mounted at run time.

The design follows [ADR 0001](adr/0001-agentic-platform-crd-architecture.md)
and [ADR 0007](adr/0007-harness-thin-runner-and-skillcard-skills.md).

## Image hierarchy

`agent-base` is the foundation. Language images extend it and add only the
toolchain needed by that language:

```text
agent-base             UBI + goose + git + harness
├── agent-java         JDK 21 + Maven
├── agent-go           Go toolchain
├── agent-csharp       .NET SDK
└── agent-nodejs       Node.js + npm
```

The source for these images is in `images/`. The controller does not require
a particular language image; an `Agent` selects its image with `spec.image`.

## What a compatible base image provides

An agentic base image must provide:

| Component | Contract |
| --- | --- |
| Harness binary | `/opt/migration-harness/bin/migration-harness`, normally exposed as `migration-harness` on `PATH` |
| Agent runtime | The `goose` CLI, started by the harness with `goose serve` |
| Git | Required for cloning the source repository and pushing the agent's commits |
| Skills mount point | `/opt/skills`, populated by the controller's `skill-loader` init container |
| Writable workspace | `/workspace`; the default clone directory is `/workspace/repo` |
| User | UID `1001`, running as a non-root user; the image must leave the workspace and harness home writable by that user or group 0 |
| Entrypoint | `ENTRYPOINT ["migration-harness"]` and `CMD ["run"]` |

A specialized base image may add other tools, but it must preserve the harness
contract and non-root execution.

Do not bake skills into a language image. The controller assembles selected
SkillCards under `/opt/skills/<name>/SKILL.md` for each run.

## Extending `agent-base`

Create a language directory and a `Containerfile` that follows this pattern:

```Dockerfile
ARG BASE_IMAGE=quay.io/konveyor/agent-base:latest
FROM ${BASE_IMAGE}

USER root

# Install the language SDK and tools using the base image's package manager.
RUN dnf install -y <language-sdk> <build-tool> \
    && dnf clean all

USER 1001

ENTRYPOINT ["migration-harness"]
CMD ["run"]
```

Use `USER root` only while installing packages. Return to `USER 1001` in
the final image. Keep the repository root as the build context because the
base image builds the harness from `harness/` and `api/`:

```bash
podman build \
  --build-arg BASE_IMAGE=quay.io/konveyor/agent-base:latest \
  -t quay.io/<org>/agent-rust:dev \
  -f images/agent-rust/Containerfile .
```

For a new shipped language, also add an `agent-<lang>-build` target to the
root `Makefile`, include it in `agent-images-build`, and add it to the image
workflow matrices.

## Build and publish

Install Podman or Docker and log in to the registry before pushing. The
Makefile defaults to Podman-compatible commands; set `CONTAINER_TOOL=docker`
when using Docker.

Build one image or the complete hierarchy:

```bash
make agent-base-build CONTAINER_TOOL=podman
make agent-java-build CONTAINER_TOOL=podman
make agent-images-build CONTAINER_TOOL=podman
```

The image names default to `quay.io/konveyor/agent-*`. Override them for a
development registry without changing the repository:

```bash
make agent-java-build \
  CONTAINER_TOOL=podman \
  AGENT_BASE_IMG=quay.io/<org>/agent-base:dev \
  AGENT_JAVA_IMG=quay.io/<org>/agent-java:dev
```

Push the five shipped images after logging in:

```bash
podman login quay.io
make agent-images-push CONTAINER_TOOL=podman
```

`agent-images-push` builds first and then pushes single-architecture images.
Do not use it against the production `latest` tags when a multi-architecture
manifest already exists: a single-architecture push replaces that tag's
manifest. For local multi-architecture builds and pushes use:

```bash
make agent-images-multiarch-build
make agent-images-multiarch-push
```

Set `AGENT_PLATFORMS` to change the platform list. The default is
`linux/amd64,linux/arm64`.

### CI publishing

- `.github/workflows/images.yml` builds pull-request artifacts for both
  architectures. It does not publish them to Quay.
- `.github/workflows/image-build-push.yml` publishes versioned images on
  `main`, `release-*` branches, and `v*` tags. It builds `agent-base` before
  the language images and assembles their multi-architecture manifests.

## Harness lifecycle

The controller's `skill-loader` runs first as an init container. It validates
and assembles the selected sources into `/opt/skills`. The harness then runs
these ten operational steps:

1. Load required environment variables and `/run/konveyor/params.json`.
2. Resolve the application, repository, branch, analysis, and git
   credentials from Hub.
3. Clone the repository, configure the commit author, strip push credentials,
   and check out `TARGET_BRANCH`.
4. Write available Hub analysis data to `.konveyor/analysis.json`.
5. Discover `/opt/skills/*/SKILL.md` and always-loaded rules.
6. Link the skills into the runtime's expected skills directory.
7. Start `goose serve`, connect over ACP, and create a session.
8. Assemble the agent, workflow, skill, and stage context into one prompt.
9. Run the prompt, monitor the session, push commits incrementally, and wind
   down with a handoff when an execution limit is reached.
10. Perform the final push, write opaque usage/outcome JSON to the termination
    log, revoke the Hub token when appropriate, and exit.

The harness does not author the agent's commits and does not give the agent
push credentials. See [the entry point contract](entry-point.md) for ACP,
credential isolation, exit codes, and workflow-stage details.

## Configuration reference

### Required environment variables

| Variable | Purpose |
| --- | --- |
| `KONVEYOR_LLM_MODEL` | Model name; the legacy fallback is `KONVEYOR_MODEL_PRIMARY_MODEL` |
| `HUB_BASE_URL` | Hub API base URL |
| `APP_ID` | Application identifier in Hub |
| `KONVEYOR_ACP_SECRET_KEY` | Secret used for ACP WebSocket authentication |
| `TARGET_BRANCH` | Branch that receives the results; it must differ from the source branch |

### LLM and run configuration

| Variable | Purpose |
| --- | --- |
| `KONVEYOR_LLM_PROVIDER` | Provider name, such as `anthropic` or `openai` |
| `KONVEYOR_LLM_ENDPOINT` | Optional custom provider endpoint |
| `KONVEYOR_LLM_API_KEY` | Optional provider API key; provider-specific credentials may instead come from a Gateway Secret |
| `KONVEYOR_PROMPT` | Agent-level standing instructions |
| `KONVEYOR_WORKFLOW_GUIDE` | Workflow-level context |
| `KONVEYOR_INSTRUCTIONS` | Stage-specific instructions |
| `KONVEYOR_GIT_AUTHOR_NAME` / `KONVEYOR_GIT_AUTHOR_EMAIL` | Optional commit identity pair; both must be set, otherwise the harness defaults the identity |
| `KONVEYOR_WORKFLOW_STAGE` / `KONVEYOR_WORKFLOW_STAGE_COUNT` | Stage metadata supplied for workflow runs |

The older `KONVEYOR_MODEL_PRIMARY_*` names are accepted as fallbacks for the
three LLM settings. `KONVEYOR_PLAYBOOK_INSTRUCTIONS` is the legacy fallback
for `KONVEYOR_WORKFLOW_GUIDE`.

### Hub and local overrides

| Variable | Purpose |
| --- | --- |
| `HUB_TOKEN` / `HUB_TOKEN_ID` | Scoped Hub credentials and token ID |
| `HARNESS_WORK_DIR` | Clone directory; default `/workspace/repo` |
| `HARNESS_SKILLS_DIR` | Assembled skills directory; default `/opt/skills` |
| `HARNESS_PARAMS_FILE` | Parameters file override; default `/run/konveyor/params.json` |
| `HARNESS_TERMINATION_LOG_PATH` | Termination log override; default `/dev/termination-log` |

The ACP and human-in-the-loop switches are on by default. Set the following
to `off` only when the behavior is deliberately disabled:

| Variable | Purpose |
| --- | --- |
| `HARNESS_ACP_TEE` | Disable the harness ACP tee |
| `HARNESS_HITL_STEER` | Refuse viewer steer/cancel frames |
| `HARNESS_HITL_ASK` | Remove the `ask_user` tool |
| `HARNESS_HITL_TIMEOUT_SECONDS` | Viewer wait timeout, capped at 600 seconds |

The controller writes execution controls and parameter values as JSON:

```json
{
  "workflow": {"application_name": "coolstore"},
  "agent": {"dry_run": true},
  "execution": {"mode": "auto", "maxTurns": 200, "maxCost": "10.00"}
}
```

`execution.mode` is `auto` or `approve`; `maxTurns` and `maxCost` are
optional execution budgets. Workflow and agent values are rendered in the
prompt under `## Parameters`. The controller passes those values through
without interpreting their domain meaning.
