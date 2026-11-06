# Lightspeed runtime integration prototype

This implements the runtime-first direction in [ADR 0021](adr/0021-shared-runtime-and-conditional-lightspeed-execution.md).
An existing AgentRun selects an image containing the current Konveyor harness
and a patched Lightspeed DeepAgents runtime. The controller still creates an
Agent Sandbox. No Lightspeed operator, new CRD, or Gateway migration is involved.

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
sessions or the existing batch entrypoint. Only inspection-approved tool results
reach the model and ACP viewers. The trusted `ask_user` bridge calls the harness's
existing MCP executable; viewers cannot configure arbitrary local MCP commands.
Remote MCP servers continue through Lightspeed's admission path.

Sessions and replay history last only for the runtime process. Viewer reconnect
and further prompts during that lifetime do not imply recovery after pod restart.

## Usage and limits

The runtime emits cumulative token observations as model calls complete. The
harness consumes snapshots rather than summing notifications or recounting the
first prompt during handoff. Missing measurements remain absent; measured zero
is preserved. Observations cover agent, subagent, and summarization calls, but
exclude isolated inspection classifier calls. They are not a billing total.

This prototype adds no monetary estimator. Its ACP consumer can read supplied
cost, but this Lightspeed fork supplies none. A Lightspeed run configured with
`maxCost` therefore fails before model execution with an unsupported-budget error.
Token reporting and turn limits can be demonstrated without claiming monetary
budget enforcement. Full adoption still requires preserving that behavior.
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

Set `CONTAINER_TOOL=docker` if needed. The image records the runtime Git revision
in its labels. It contains Python, Git, and the Go harness; extend it with the
language toolchain needed by the selected application. This is a development
image, not a qualified production or FIPS build. The script does not publish it.

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
