# Lightspeed runtime integration

The [ADR 0021](adr/0021-shared-runtime-and-conditional-lightspeed-execution.md)
runtime integration keeps Konveyor's controller, AgentRun and Agent Sandbox path.
The Go harness prepares Hub inputs and the checkout, launches Lightspeed, verifies
work, pushes commits and revokes the Hub token. Push credentials stay in the
harness. The controller does not interpret application parameters or relay ACP.

Use the existing UBI-based `agent-base` image with `HARNESS_RUNTIME=lightspeed`.
Language images inherit that runtime and add their toolchains. Select the
appropriate GitHub-built image through `Agent.spec.image`; see
[container images and PR artifacts](../images/README.md). The standalone harness
binary retains its Goose default when `HARNESS_RUNTIME` is unset.

## Runtime and configuration

The companion [runtime contribution at `2291ce9`](https://github.com/djzager/lightspeed-agentic-sandbox/tree/2291ce9e449976d080dd1a287a72a4e8c12f2359)
starts from upstream `5ea23f34ad2ad5fc01fb6264fb9304116664dd3d`.
It retains graph state and model clients across prompts, supports human
questions/approval, steering and cancellation, and provides optional ACP through
an official `AgentServerACP`. The [runtime ACP contract](https://github.com/djzager/lightspeed-agentic-sandbox/blob/2291ce9e449976d080dd1a287a72a4e8c12f2359/src/lightspeed_agentic/acp/README.md)
documents inspection, approved output release, replay and compatibility tests.

The harness maps existing Gateway provider/model configuration to Lightspeed.
Supported providers are `anthropic`, `openai`, `xai`, `aws-bedrock` and `gcp-vertex-ai`.
Existing `execution.mode`, `askUser` and `maxTurns` parameters retain their roles.
A limit-triggered handoff uses the same session. `/workspace` is a viewer alias
for the prepared `/workspace/repo`; it does not change the execution directory.
Session and viewer history last only for the runtime process.

Token observations are cumulative snapshots and distinguish missing values from
measured zero. They exclude isolated inspection-classifier calls and are not a
billing total. This runtime supplies no monetary estimator: the harness rejects
`maxCost` before execution. Main-model per-response output allowance can be set
with `LIGHTSPEED_MODEL_MAX_OUTPUT_TOKENS`; explicit provider output cutoff fails
before executing tools from the unfinished response. Reasoning settings remain
provider-specific and optional.

Official ACP is pinned to `deepagents-acp` 0.0.13 at
`f2a6e02dc8e2dcb05885c5133b94102ccc79b794`. Five private overrides require
compatibility tests when upgrading. The November 11 source review of upstream
runtime `a23005e8f9ed0edc6e430072f50e4698590de71f` found later redaction/shaping
changes that still require reconciliation with the fork. Baseline inspection
preservation does not establish current-upstream safety parity.

## Verification and presentation

Run harness regressions with `cd harness && go test ./...`. The retained
[integration regression](../hack/lightspeed-runtime/verify.py) exercises the real
Go harness and runtime with local scripted model/classifier and Hub endpoints,
temporary Git repositories, and no remote push or paid model:

```sh
/path/to/lightspeed-agentic-sandbox/.venv/bin/python \
  hack/lightspeed-runtime/verify.py \
  --runtime-dir /path/to/lightspeed-agentic-sandbox \
  --evidence-dir .scratch/lightspeed-verification
```

Install that runtime checkout's frozen development and ACP dependencies first.
The helper needs ports 4000 and 4001 and a fresh evidence directory. Its six cases
cover automatic execution, approval, a human answer, active cancellation,
steering/reconnect, and turn-limit handoff. It checks usage, verification, Git
results and token cleanup. Its scripted classifier approves tool results;
rejection and inspection/replay boundary regressions belong in the runtime fork.

The November 11 verification of `2291ce9` passed six local harness cases and 23
native graph/ACP cases, plus the runtime suite (2,119 passed, 13 skipped), Ruff,
format and mypy. These are historical local results, separate from current CI.
They do not establish a new browser, live-classifier or cluster run.

The [slide deck](slides/lightspeed-2026-11-11.md),
[PDF](slides/lightspeed-2026-11-11.pdf), and
[walkthrough with timed narration and provenance](slides/lightspeed-2026-11-11-demo.md)
form the presentation package. Its video records the earlier runtime `4039163`
and does not validate the later official ACP integration. Presentation notes
contain the operator integration questions and pinned source references.

Before developing another classifier demonstration, require green GitHub checks
and usable GitHub-built image artifacts. A new run must record its actual source
revisions, imported runtime, image digest, classifier decision, viewer history,
and final result. The existing recording and scripted regressions do not supply
that evidence.
