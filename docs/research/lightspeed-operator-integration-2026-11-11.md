# Lightspeed operator adoption: integration problems and discussion paths

Checked against public upstream on November 11, 2026, at approximately 15:20 UTC.
This is source inspection and roadmap research; no new cluster qualification was performed.

## Current revisions and decision boundary

The live GitHub commits API still reports operator `main` at
[`9973d0002c78d843ec838ee64fd3f5221a094faa`][operator-pin], committed November 10
at 10:23:54 UTC. The earlier November 10 operator pin therefore remains current.
Runtime `main` has advanced to
[`a23005e8f9ed0edc6e430072f50e4698590de71f`][runtime-pin], committed November 11
at 12:35:36 UTC. Open pull requests are identified separately below.

[ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md)
accepts runtime reuse independently of conditional operator adoption. The Go harness
continues to own Hub/repository resolution, preparation, Git push, token cleanup,
and outcome reporting. The firm operator adoption requirement is control over the
workload image and startup of its harness for each invocation. The accepted
[ADR 0016 amendment](../adr/0016-gateway-crd-as-interim-execution-interface.md)
retains Konveyor's current Gateway selection and direct Agent Sandbox execution;
OpenShell remains a separate investigation. This research changes neither decision.

**Candidate delegation boundary, for discussion:** Konveyor reconciles an
AgenticRun instead of creating its Sandbox directly; Lightspeed owns the
SandboxClaim/workload lifecycle; the selected image starts the Konveyor harness,
which retains the complete application work lifecycle. The operator's
`SandboxManager.Create` already dispatches between a SandboxClaim path and a
bare-Pod path. Its claim path creates Agent Sandbox SandboxTemplate,
SandboxWarmPool, and SandboxClaim resources. Adopting that path replaces
Konveyor's direct integration while retaining Agent Sandbox underneath
Lightspeed. [Creation dispatch][dispatch], [claim resources][claims].

## Problem 1: one shared workload image cannot cover concurrent toolchains

Java EE to Quarkus work needs a JDK and Maven; a future Go Migrator needs the Go
toolchain. These are Konveyor use cases, not claims about a Lightspeed roadmap.
Separate invocations must be able to choose and concurrently run different
images containing their toolchains, runtime, and Go harness.

The current Lightspeed `AgentSpec` selects provider/model, instructions, timeouts,
turn limit, and reasoning configuration. `AgenticRunStep` selects an Agent, and
`AgenticRunSpec` supplies request, tools, fixed workflow steps, revision feedback,
and retention. Neither API selects the workload image. The operator parses one
shared `sandbox-pod-spec` from its configuration ConfigMap, and
`PodSpecBuilder.Build` deep-copies that base and overlays environment, mounts,
identity, and execution controls. It preserves the base container image, command,
and args. [Agent API][agent-api], [run API][run-api], [configuration][config],
[builder][builder].

**Possible supported approaches, not a selected design:** an approved workload
profile/template reference per invocation, or an image selector resolved under
platform policy. Either must support heterogeneous concurrent runs. The image's
declared ENTRYPOINT may already satisfy harness startup; an explicit command
override is only necessary if the agreed startup contract requires it. A global
custom image can demonstrate a harness, but cannot establish this per-invocation
requirement. This is the concrete capability gap to resolve with Lightspeed.

## Problem 2: placement changes the complete access path

Lightspeed watches namespaced AgenticRuns, but creates input ConfigMaps,
workloads, Result resources, and Result RBAC in its configured operator
namespace. Its Agent and LLMProvider are cluster-scoped. Provider credential
Secrets are resolved in the operator namespace and mounted/referenced locally by
the workload. Watching a run in Hub's namespace does not move its workload or
Secrets there. [Watch][watch], [input and Result namespace][inputs],
[workload namespace][claims], [provider Secret lookup][provider],
[Result RBAC][result-rbac], [Agent scope][agent-api], [LLMProvider scope][provider-api].

Credential delivery already exists: `spec.tools.requiredSecrets` supports file
mounts and environment bindings; the environment case currently reads the
Secret's `token` key. Source comments conflict: `SecretRequirement` explicitly
says operator namespace, while the `ToolsSpec.RequiredSecrets` comment says
AgenticRun namespace. The builder creates local Secret references, so the
workload's actual namespace determines which Secret is used. This needs a
documented integration contract, not a claim that credential support is absent.
[Secret API/comments][secrets], [mount implementation][secret-mounts].

**Adoption requirement:** the harness must reach Hub with its scoped token,
resolve application and Git data, reach Git and its build dependencies, access
the selected model and MCP endpoints, and revoke the Hub token normally. The
viewer must reach the harness's ACP endpoint under the intended authentication,
TLS, routing, and network policies. Hub HTTP authorization and Kubernetes
namespace/Secret authorization are separate paths.

**Possible approaches:** colocate execution with Hub through a supported
placement contract, or use supported centralized execution with explicit input,
Secret, network, trust, and ACP routing arrangements. Agree who provisions,
authorizes, binds, and cleans up each credential. The current claim template
uses `networkPolicyManagement: Unmanaged`; that delegates policy management and
does not prove Hub or other traffic is allowed. [Template][claims]. These are
deployment and adapter questions until evidence requires an operator change.

## Problem 3: startup is a batch input/output adapter

The current operator does **not** start an HTTP server and POST
`/v1/agent/run`. `PodSpecBuilder.Build` mounts a read-only ConfigMap at `/input`
and starts a batch process. Files are `query`, `output-schema`, `context`,
`result-template`, and optional `system-prompt`. It also supplies provider/model,
execution-limit, and other configuration through environment and mounts.
`buildResultTemplate` supplies the Result kind, name, namespace, labels, owner
reference, and spec. [Batch builder][builder], [input construction][inputs],
[Result template][result-template]. The runtime image starts
`python3.12 -m lightspeed_agentic.batch` via catatonit; its `main` reads input,
runs the agent, publishes the Result, and exits. [Image startup][runtime-startup],
[batch entrypoint][runtime-batch].

**Candidate harness adapter:** read the batch envelope, translate supported
inputs and configuration into the common Konveyor harness contract, prepare the
workspace, launch the reusable runtime/session, finish required verification,
push, token cleanup, and core reporting, then publish the supplied Result and
exit. This can potentially fit existing batch facilities once image selection
is solved. It is not a demonstrated adapter or a requirement for Lightspeed to
implement Konveyor's application behavior.

Result publishing has an existing Kubernetes identity path. The operator creates
a per-step Role/RoleBinding: create permission for that Result kind and
name-scoped get/patch/update on its resource and status subresource. The current
runtime creates the Result object, tolerates AlreadyExists, and replaces status
with a resourceVersion using the Kubernetes client and in-cluster credentials.
The operator spec still describes `oc create`/`oc patch`; the current code and
runtime show that the API object/status contract matters, not a requirement to
invoke `oc`. [RBAC][result-rbac], [publisher][runtime-publisher],
[runtime publishing contract][runtime-publish-spec].

A valid Result needs `Completed=True` and step-specific content. Analysis
normally requires options unless `actionRequired=False`; execution requires
`actionsTaken`; verification requires checks. Analysis-only runs support a
Minimal schema and custom components, but those still have an analysis-shaped
contract. The integration must agree a truthful mapping for Konveyor outcomes,
verification evidence, commit, and usage rather than treating arbitrary JSON or
a process exit as sufficient. [Result validation][result-validation],
[Minimal schema][minimal-schema], [run shape][run-api].

## Problem 4: prompt completion and workload completion are different events

The harness can need work after a prompt completes: limit-triggered continuation
in the same session, its own verification, final Git push, core outcome reporting,
and normal Hub-token revocation. The existing harness explicitly reserves a
handoff prompt after a limit, verifies eligible successful work before the final
push, and attempts its final push with a fresh bounded context. Token revocation
is deferred, with the documented successful intermediate-workflow exception.
[Handoff][harness-handoff], [verification][harness-verification],
[push][harness-push], [revocation][harness-revocation].

Lightspeed's normal batch completion can accommodate a process that keeps
running through this finalization. It waits for PodSucceeded, validates the
completed Result, records outcome, then releases the sandbox. Missing/invalid
Result fails the step. A nonzero process exit follows sandbox failure handling;
an agent-level failure instead uses a completed failed Result and process exit
0. If Git push succeeds but Result publication fails, the invocation must expose
that integration failure while preserving the completed work and core outcome,
as ADR 0021 requires. [Operator completion][completion],
[Result validation][result-validation], [runtime exit handling][runtime-exit].

**Deadlines:** the operator already calculates a Pod active deadline from five
minutes startup + resolved step timeout + one minute grace. Its timeout loop
also checks startup and running-container deadlines, with approximately
one-minute polling. Defaults are ten minutes for analysis/execution and thirty
minutes for verification. These are whole-process constraints from the custom
harness's perspective; a long prompt can consume the time needed for build
verification and publication. Agree separate runtime budget and bounded
finalization allowance, or another supported budget composition, rather than
assuming the runtime timeout alone is enough. [Deadline constants][deadlines],
[calculation][deadline-calculation], [polling interval][timeout-polling],
[Pod deadline][dispatch], [timeout enforcement][timeouts], [defaults][limits].

**Cancellation and emergency stop must retain distinct semantics.** A viewer's
ACP cancellation can stop the active prompt while the live harness still pushes
partial committed work and revokes its token. Deleting the workload immediately
can prevent these final steps. A security/emergency stop may deliberately
require that immediate teardown; preserving a graceful completion path does not
mean delaying an emergency stop. The integration needs explicit semantics for
each action and a cleanup path for abrupt loss. [Viewer cancellation][harness-handoff],
[final push][harness-push], [revocation][harness-revocation].

At current operator `main`, global suspension is implemented and requests
zero-grace deletion of active managed workloads and temporary-access cleanup;
confirmation is control-plane evidence, not proof of process death on an
unreachable node. Per-run cancellation remains deferred in merged code.
[Implemented/planned distinction][stop-spec], [zero-grace deletion][stop-code].
Current open [PR #588][cancel-pr], head
`a643aa57d60e15a8e255552281e3d9bfc576de6a`, proposes one-way `spec.cancelled`,
`Stopping`, hard-stop confirmed cleanup, and terminal
`Failed/CancelledByUser` or `CancellationFailed`. It is unmerged as checked;
earlier [PR #573][old-cancel-pr] closed unmerged on November 11. This active
design is a useful discussion input, not shipped per-run cancellation support
or a graceful harness-finalization protocol.

## OpenShell: ask about the plan before choosing the adapter

No explicit public Lightspeed OpenShell adoption decision or dated commitment
was found. Live GitHub issue/PR and code searches for `OpenShell` returned zero
matches in `openshift/lightspeed-agentic-operator`,
`openshift/lightspeed-agentic-sandbox`, and `openshift/ols`; an OpenShell
issue/PR search for `Lightspeed` also returned zero. This is a bounded search
result, not evidence that no private discussion or future plan exists. The
question for Lightspeed remains: **Are you adopting OpenShell, on what decision
and timeline, and which workload contract should Konveyor target?**

Conditional implications are concrete. Current OpenShell Kubernetes driver
documentation, pinned to
[`7d9303690f7de689bddb487efde77365c02f8555`][openshell-pin] on November 11,
still describes Agent Sandbox resources. Its gateway/driver adds workload
launch supervision and policy-mediated networking; workload ServiceAccount
token automount is disabled. Its namespace modes include shared, managed, and
operator-provisioned namespaces. [Namespace and runtime model][openshell-model],
[credential/relay model][openshell-credentials].

**Inference for joint design:** if Lightspeed adopts that architecture, qualify
custom image/harness launch, Hub/Git/model/MCP policy, namespace and Secret
delivery, Result publication identity, ACP exposure, and lifecycle reporting
through the supported Lightspeed/OpenShell interface. Mediated egress does not
mean these services are permanently unreachable; disabled token automount does
not rule out a deliberately supported publication identity. These require an
agreed contract. Agent Sandbox would not automatically disappear underneath it.

## Discussion prompts

1. What supported selection mechanism lets two invocations run distinct
   toolchain images and start their Go harnesses concurrently?
2. Where should the workload run, and who owns Hub/provider inputs, scoped
   credentials, trust, egress, ACP access, and cleanup?
3. Can our harness satisfy the existing batch envelope and Result contract;
   what outcome mapping and extension points should we use?
4. How should runtime budgets, same-session handoff, verification, push,
   reporting, and token revocation fit inside workload deadlines?
5. Which UI action means graceful prompt cancellation, and which means a
   confirmed workload hard stop? What happens when publication or finalization
   is interrupted?
6. Is an OpenShell transition planned, is there a committed timeframe, and
   would adopting today's operator contract create avoidable integration work?

[operator-pin]: https://github.com/openshift/lightspeed-agentic-operator/commit/9973d0002c78d843ec838ee64fd3f5221a094faa
[runtime-pin]: https://github.com/openshift/lightspeed-agentic-sandbox/commit/a23005e8f9ed0edc6e430072f50e4698590de71f
[agent-api]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/agent_types.go#L99-L166
[run-api]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/agenticrun_types.go#L261-L376
[config]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/pkg/configuration/config.go#L156-L197
[builder]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/podspec_builder.go#L58-L227
[dispatch]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_manager.go#L195-L276
[claims]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_manager.go#L619-L721
[watch]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/reconciler.go#L416-L437
[inputs]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/input_configmap.go#L118-L167
[result-template]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/input_configmap.go#L170-L214
[provider]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/resolve.go#L62-L81
[provider-api]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/llmprovider_types.go#L303-L334
[secrets]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/tools_types.go#L87-L147
[secret-mounts]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/podspec_builder.go#L481-L515
[result-rbac]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/rbac.go#L510-L566
[runtime-startup]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/Containerfile#L128-L134
[runtime-batch]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/batch.py#L138-L158
[runtime-publisher]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/publish_results/publish.py#L150-L211
[runtime-publish-spec]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/.ai/spec/what/run-api.md#L67-L87
[runtime-exit]: https://github.com/openshift/lightspeed-agentic-sandbox/blob/a23005e8f9ed0edc6e430072f50e4698590de71f/src/lightspeed_agentic/batch.py#L245-L279
[result-validation]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/pod_handler.go#L542-L597
[minimal-schema]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/schemas.go#L164-L201
[completion]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/pod_handler.go#L253-L324
[deadlines]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_agent.go#L16-L20
[deadline-calculation]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_agent.go#L171-L172
[timeout-polling]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/timeout_handler.go#L20-L45
[timeouts]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/timeout_handler.go#L122-L185
[limits]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/limits.go#L7-L53
[stop-spec]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/.ai/spec/what/sandbox-execution.md#L117-L119
[stop-code]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/suspension_cleanup.go#L885-L968
[cancel-pr]: https://github.com/openshift/lightspeed-agentic-operator/pull/588
[old-cancel-pr]: https://github.com/openshift/lightspeed-agentic-operator/pull/573
[harness-handoff]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/cmd/migration-harness/main.go#L596-L665
[harness-verification]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/cmd/migration-harness/main.go#L708-L731
[harness-push]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/cmd/migration-harness/main.go#L816-L839
[harness-revocation]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/harness/cmd/migration-harness/main.go#L153-L177
[openshell-pin]: https://github.com/NVIDIA/OpenShell/commit/7d9303690f7de689bddb487efde77365c02f8555
[openshell-model]: https://github.com/NVIDIA/OpenShell/blob/7d9303690f7de689bddb487efde77365c02f8555/crates/openshell-driver-kubernetes/README.md#L18-L108
[openshell-credentials]: https://github.com/NVIDIA/OpenShell/blob/7d9303690f7de689bddb487efde77365c02f8555/crates/openshell-driver-kubernetes/README.md#L355-L375
