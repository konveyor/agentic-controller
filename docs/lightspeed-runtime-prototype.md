# Lightspeed runtime integration prototype

This implements the runtime-first direction in [ADR 0021](adr/0021-shared-runtime-and-conditional-lightspeed-execution.md).
An existing AgentRun invokes an Agent whose `spec.image` contains the current
Konveyor harness and a patched Lightspeed DeepAgents runtime. The controller
still creates an Agent Sandbox. No Lightspeed operator, new CRD, or Gateway
migration is involved.

The harness continues to prepare the application from Hub, isolate push
credentials, assemble rules and instructions, expose the existing ACP tee,
verify work, push commits, and revoke the Hub token. The alternate runtime is
opt-in through `HARNESS_RUNTIME=lightspeed`; Goose remains the default.

## Runtime contribution

The companion repository is `openshift/lightspeed-agentic-sandbox`, branch
`feat/reusable-sessions-acp`, starting at upstream commit
`5ea23f34ad2ad5fc01fb6264fb9304116664dd3d`. The controller branch starts at
`8c3c7cd` on upstream main. Final runtime and verification revisions are recorded
in `hack/lightspeed-runtime/reference.json`.

The reusable session owns graph/checkpoint state and model clients. It accepts
additional prompts on the same in-memory thread, including automatic
limit-triggered handoff. Tool approval and questions use callbacks with no ACP
dependency. Steering is consumed at a safe model boundary; cancellation awaits
active work and terminates shell child processes.

The optional `lightspeed_agentic.acp` package adapts callbacks and normalized
events to the existing harness connection. Removing it does not remove core
sessions or the existing batch entrypoint. With Lightspeed inspection enabled,
untrusted tool results reach the model and ACP viewers only after inspection
allows them. The trusted `ask_user` bridge calls the harness's existing MCP
executable; viewers cannot configure arbitrary local MCP commands. Remote MCP
servers continue through Lightspeed's admission path.

Sessions and replay history last only for the runtime process. Viewer reconnect
and further prompts during that lifetime do not imply recovery after pod restart.

The harness explicitly maps the UI's logical `/workspace` directory to the
prepared `/workspace/repo` session. This alias does not change the runtime's
execution directory. It also enables primary-session replay for read-only viewer
sessions after the harness has created the execution session. The adapter reports
active run identifiers and discovery errors in the format the existing UI uses
for steering. These compatibility details are covered separately from the core,
transport-independent session behavior.

## Usage and limits

The runtime emits cumulative token observations as model calls complete. The
harness consumes snapshots rather than summing notifications or recounting the
first prompt during handoff. Missing measurements remain absent; measured zero
is preserved. Observations cover agent, subagent, summarization, and shaping
calls, but exclude isolated inspection classifier calls. Shaping calls do not
consume the main session's turn budget. These observations are not a billing total.

This prototype adds no monetary estimator. Its ACP consumer can read supplied
cost, but this Lightspeed fork supplies none. A Lightspeed run configured with
`maxCost` therefore fails before model execution with an unsupported-budget error.
Token reporting and turn limits can be demonstrated without claiming monetary
budget enforcement. Full adoption still requires preserving that behavior.
The companion UI change accepts `_lightspeed/session/update`, displays token-only
usage, and retains the same absent-versus-zero distinction for live updates and
final termination data. It does not display a zero-dollar cost when cost is unknown.
The proposed [usage telemetry contract in PR 282](https://github.com/konveyor/agentic-controller/pull/282)
and the existing accounting research remain separate work.

## Local verification

Install the companion runtime with pinned dependencies, then run the checks:

```sh
cd /path/to/lightspeed-agentic-sandbox
uv sync --frozen --extra dev --extra acp

cd /path/to/agentic-controller
/path/to/lightspeed-agentic-sandbox/.venv/bin/python \
  hack/lightspeed-runtime/verify.py \
  --runtime-dir /path/to/lightspeed-agentic-sandbox \
  --evidence-dir .scratch/lightspeed-verification
```

The fixture uses the real harness, runtime, graph, tools, inspection, and ACP
transport with a scripted HTTP model, local Hub fixture, and temporary Git
repository. It requires local ports 4000 and 4001. It does not use real model
credentials or push to a remote repository. Evidence is written to the requested
directory; choose a fresh path for each run. Runtime tests cover negative cases
and cancellation separately. Token snapshots use the explicit ACP extension
`_lightspeed/session/update`, so they cannot be mistaken for context-window
occupancy in the standard ACP usage event.

Existing controller/workflow and harness regressions remain applicable. A green
stub workflow test alone does not establish real Lightspeed AgentWorkflowRun
execution. A separate workflow demonstration is not a gate for this prototype.

## Build and demonstrate an AgentRun

Commit the runtime changes, then build an image from that checkout:

```sh
hack/lightspeed-runtime/build-image.sh \
  /path/to/lightspeed-agentic-sandbox localhost/agent-lightspeed:prototype
```

The build uses Podman by default. The image records both the controller and
runtime Git revisions in its labels. It contains Python, Git, and the Go harness;
extend it with the language toolchain needed by the selected application. This is
a development image, not a qualified production or FIPS build. The script does
not publish it.

Make the image available to the cluster and select it on a demo Agent using the
existing `spec.image` field. Keep the Gateway, skills, and Hub-created
AgentRun configuration. The Lightspeed adapter requires the default ACP tee;
`HARNESS_ACP_TEE=off` is rejected. Provider mappings currently cover Anthropic,
OpenAI-compatible endpoints, Bedrock, and Vertex; each needs its usual credentials.

Through the existing UI, start a watched run with `mode: approve` and
`askUser: true`, approve a tool, answer a question, and send guidance. Confirm
the resulting branch, independent verification, and token revocation. Exercise
viewer cancellation on another run. Use a small `maxTurns` on a further run to
observe automatic handoff in the same session and inspect the committed handoff
and cumulative usage. Omit `maxCost` for this prototype.

Record image digest, source revisions, AgentRun names, outcomes, and failures.
Local scripted results do not substitute for cluster/UI evidence. Operator
placement, per-invocation image/entrypoint control, and provider/secret access
remain the separate operator adoption work.

For a repeatable browser demonstration, use the
[Playwright recording helper](../hack/lightspeed-runtime/browser/README.md).
It creates the AgentRun through Tackle UI and sends questions, approvals, and
steering through the UI's actual Hub connection. Its local model is explicitly
scripted; runtime inspection, tools, verification, and Git operations are real.

## Recorded verification (2026-11-06)

The pinned runtime passed 1,988 tests, with 13 skipped, plus mypy and Ruff checks.
The controller's `make test`, API tests, and harness tests and race checks passed.
All six local integration cases passed: automatic execution, approval, ask-user,
cancellation, steering/reconnect, and turn-limit handoff. An independent rerun
also passed all six cases.

A Podman-backed Kind cluster also passed the existing 16-check pipeline and three
real AgentRun checks. Automatic execution and approval both reached `Succeeded`
with `Verified=True`. The turn-limit case intentionally reached `Failed` with
`LimitReached`: it committed and pushed its handoff using a second prompt in the
same session. Each case reported cumulative usage and revoked its Hub fixture
token. These checks use a scripted HTTP model and Hub fixture, real Git transport,
and a direct ACP WebSocket viewer. They do not establish live-model quality or a
browser UI result.

An additional AgentRun created through the deployed Tackle Hub API also
succeeded. It used the real Hub's application metadata and scoped token, answered
an `ask_user` question and approved a tool through a direct ACP viewer, verified
and pushed the resulting commit, and revoked the Hub token. Its model and Git
repository remained local fixtures; UI/Hub login was disabled in this local
deployment, so production authentication remains untested.

To repeat the cluster checks, install the controller and Agent Sandbox, load the
prototype image into a disposable cluster, and use a fresh namespace and evidence
directory:

```sh
/path/to/lightspeed-agentic-sandbox/.venv/bin/python \
  hack/lightspeed-runtime/cluster_verify.py \
  --kubeconfig /path/to/task.kubeconfig \
  --image localhost/agent-lightspeed:prototype \
  --namespace lightspeed-validation \
  --evidence .scratch/lightspeed-cluster-verification
```

At that point, the deployed `tackle2-ui` assets and Hub APIs responded, but the
first browser observation showed a blank page and subsequent browser-control
calls stalled. No browser-created AgentRun or browser interaction had been
verified. The later Playwright check below closes that gap. Source revisions,
image IDs, outcomes, and local evidence paths are recorded in
[`reference.json`](../hack/lightspeed-runtime/reference.json).

## Browser recording and compatibility fixes (2026-11-09)

Playwright rendered the actual UI without the earlier blank page. Exercising its
real ACP client exposed three gaps missed by the direct WebSocket viewer: the
logical workspace path in `session/new`, active-run discovery for steering, and
Lightspeed token updates in the UI. The runtime/harness and companion UI changes
above address these gaps. The updated runtime passed 2,002 tests with 13 skipped;
ACP type and lint checks, Go runtime race tests, and the UI's 594 tests with three
skipped also passed. The UI production build, type checking, formatting, and lint
checks passed with existing unrelated warnings.

The recorded AgentRun `ui-n8fz9` was created entirely through Tackle UI, backed by
the real Hub and the Podman-based Kind installation. Playwright opened its
session, answered `staging`, approved runtime inspection, sent `Use the canary
rollout label.` while a tool was active, and approved the resulting commit.
The next model call received the answer and steering. Live usage showed 40 input
and 10 output tokens at the steering step, then 80 input and 20 output tokens at
completion. Unknown monetary cost remained absent.

The run reached `Succeeded`; Hub reported `Verified=True` and successful harness
verification. Its actual local Git remote received commit
`d32704b1cd87b6d074ce2a97817d072ccf1a1b87`, containing the selected target, canary
rollout, and runtime provenance. The UI displays `Succeeded`; verification is
asserted separately through Hub because the UI does not render that condition.
The harness revoked the real Hub token (HTTP 204); subsequent lookup returned
HTTP 404. There were no browser page, console, request, or HTTP errors in the
successful take.

An actual runtime tool and a separate active-pod inspection agreed on the imported
Lightspeed package, `RuntimeSession` source hash, DeepAgents version, and running
`python -m lightspeed_agentic.acp` process. The pod image digest and source labels
matched runtime revision `ecbd76a` and harness revision `3c8088c`. The recording
uses UI revision `aa4f9b93` from the companion `tackle2-ui` branch
`fix/lightspeed-acp-ui`.

This is explicitly a scripted-local-model demonstration of the modified runtime.
The four model responses supply deterministic usage; two real inspection calls
exercise the classifier path using scripted classifier responses. The browser,
Hub, controller, Agent Sandbox, harness, runtime, tools, verification, and Git
operations are real. It does not establish live-model reasoning, classifier
quality, monetary accounting, production authentication, pod-restart recovery,
or adoption of the Lightspeed operator. UI/Hub login was disabled locally.

The successful take, sanitized evidence, raw WebM, and presentation MP4 are stored
locally under `output/lightspeed-runtime-demo-2026-11-09/` in the main controller
checkout. Earlier failed recording attempts remain separate diagnostic evidence;
one stopped at a recorder selector and another at the independent image lookup,
before its paused tool was released.
