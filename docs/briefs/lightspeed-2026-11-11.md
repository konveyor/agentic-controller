# Konveyor–Lightspeed discussion — November 11, 2026

## Purpose and accepted direction

Show the runtime prototype, get feedback on reusable contributions, and discuss requirements for
optional Lightspeed operator adoption. [Accepted ADR 0021][adr] separates runtime reuse, execution
backend adoption, and later resource migration. This brief records preparation; it announces no
implementation PR or upstream agreement.

Konveyor's prototype reuses Lightspeed's DeepAgents runtime while its Go harness owns Hub
application resolution, repository/instruction preparation, push-credential isolation, verification,
Git push, token revocation, and outcomes. The prototype retains Konveyor's controller, Gateway
selection, and direct Agent Sandbox execution. It does not deploy Lightspeed's operator. The
controller remains a stateless reconciler and does not interpret Hub data or participate in ACP
sessions.

The [presentation outline](../slides/lightspeed-2026-11-11-outline.md) puts runtime integration and
feedback first, targeting eight to ten minutes. Operator topics follow a deliberate stopping point
for continued discussion or later review. The [demo guide](../slides/lightspeed-2026-11-11-demo.md)
covers the planned live run.

## Runtime contribution for review

- **Core sessions:** keep graph/checkpoint state and model clients alive across prompts in one
  process. Approval/questions use callbacks, normalized events expose inspected output, steering
  enters at a safe model boundary, and cancellation stops active work and shell child processes.
- **Optional ACP adapter:** maps callbacks/events onto the transport used by Konveyor's
  harness/UI. Removing it leaves core sessions and the batch entrypoint usable. Transport
  compatibility should not determine the core session interface.
- **Harness integration:** preserves Hub/Git preparation and finalization, interaction controls,
  and turn-limit handoff through that adapter.

Same-session handoff differs from viewer reconnect, a later workflow stage, or recovery after
process loss. Sessions/replay last only for the runtime process. With inspection enabled, untrusted
tool results reach model context and viewers only after inspection approves release. Rejection and
technical failure retain failure behavior; this is not a general guarantee against harmful actions.
The trusted human-answer bridge admits no arbitrary viewer-provided executables; remote MCP tools
retain Lightspeed's admission path. See [session core][session], [adapter contract][acp], and
[integration description][prototype].

Published review branches and immutable discussion revisions:

- [Konveyor harness/integration][controller-branch] — [`73a44d5`][controller-pin].
- [Lightspeed reusable sessions/ACP][runtime-branch] — [`ecbd76a`][runtime-pin], based on upstream
  [`5ea23f3`][runtime-base].
- [Tackle UI compatibility][ui-branch] — [`aa4f9b9`][ui-pin].

Code review precedes implementation PR submission. Contribution boundaries, upstream API choices,
and timing remain open for discussion.

## Demonstrated evidence and planned live work

The [prototype report][prototype] and [pinned verification record][evidence] distinguish:

- Six local cases using the actual harness/runtime/graph/tools/inspection/ACP: automatic
  execution, approval, questions, cancellation, steering/reconnect, and turn-limit handoff, driven
  by a scripted HTTP model.
- Kind AgentRun/Agent Sandbox checks: automatic and approval runs succeeded with verification; a
  turn-limited run pushed a handoff through a second prompt in the same session and reported
  `LimitReached`.
- The November 9 browser recording: actual Tackle UI/Hub created the run, answered a question,
  approved tools, sent steering during tool execution, displayed cumulative tokens,
  verified/pushed a commit, and revoked the Hub token. Independent pod inspection confirmed the
  modified runtime.

Model/classifier responses and Git repositories were local fixtures; UI/Hub login was disabled.
These checks establish integration behavior, not live-model reasoning, classifier quality,
production authentication, monetary accounting, restart recovery, or operator adoption. Browser
cancellation/handoff were not demonstrated; separate integration cases cover them. The recorded
harness image predates the commit that publishes its documentation/helpers; the evidence records
both revisions.

A **new live-model Coolstore planning demo is planned, not completed**. Use [Coolstore
`mta-test`][coolstore-branch], pinned during preparation to [`32c1722`][coolstore-pin], with results
on a separate branch. Environment access and model configuration remain pending. Explain the
application and selected Agent/model/runtime; show questions, approval and steering; inspect the
committed plan and runtime evidence. Planning does not demonstrate a working migration or all
runtime controls. The demo guide supplies the rehearsal/verification sequence.

## Accounting and monetary limits

The runtime emits cumulative observed tokens for agent, subagent, summarization, and shaping calls.
Consumers replace snapshots; absent values remain absent and measured zero remains zero. Isolated
inspection classifier calls are excluded. The fork supplies no monetary cost and rejects `maxCost`
before execution. These observations are not a billing total. [Contract][acp],
[limitations][prototype].

Complete runtime-provided usage/cost reporting and monetary limits are follow-on work. Konveyor
consumes runtime reporting, with timely updates and cancellation for budget-triggered same-session
handoff; it should not maintain a parallel cost estimator. This sequences work beyond the prototype
without removing ADR 0021's full-adoption requirement to preserve `maxCost` behavior.

## Optional operator adoption

November 10 source review used [`9973d000`][operator-pin]. The firm requirement is **image selection
per invocation and startup of that image's Konveyor harness**, including concurrent runs with
different toolchains. Current Agent/AgenticRun APIs have no workload image selector. One shared
`sandbox-pod-spec` supplies the base image/command/args, which the builder preserves. A global
custom image is possible; honoring its ENTRYPOINT may satisfy startup. An explicit command override
is not yet a separate requirement. Per-invocation image selection remains an actual gap. [Agent
API][operator-agent], [run API][operator-run], [configuration][operator-config],
[builder][operator-builder].

Agree on the complete contract before choosing placement or prescribing a solution:

- **Placement/scope:** watching a namespaced AgenticRun does not place its workload there.
  Workloads/inputs use the operator namespace. Agent/LLMProvider are cluster-scoped; runs,
  approvals, Results, inputs, and credential Secrets are namespaced. Foreign-run owner references
  remain a source-inspected hazard, not a new live failure. [Placement][operator-placement],
  [watch][operator-watch], [approval ownership][operator-approval], [scope][operator-scope].
- **Inputs/credentials:** provider Secrets resolve in the operator namespace and are mounted into
  workloads. RequiredSecrets already supports env/file bindings; credential delivery is not
  absent. Agree provisioning, authorization, binding, and cleanup for Hub/model credentials and
  invocation inputs. Current docs conflict on required-Secret namespace; actual workload
  placement/local Secret references determine behavior. [Provider lookup][operator-provider],
  [Secret API and conflicting comments][operator-secrets], [mounts][operator-mounts].
- **Access/identity:** Hub HTTP reachability with a scoped token differs from Kubernetes
  namespace/Secret permissions. Qualify Hub, Git, model/MCP, CA, and viewer-to-workload ACP access
  under intended policies and per-step identity. Templates use `networkPolicyManagement:
  Unmanaged`; this does not prove traffic is allowed by destination/cluster policies.
  [Template][operator-template], [identity][operator-identity].
- **Lifecycle:** preserve input delivery, Result publication/outcome translation,
  cancellation/deadlines, ownership, and cleanup through finalization. Pod success requires a
  valid completed Result; publication failure is already visible. Agree how successful work/push
  plus failed publication is reported. Global suspension hard-stops workloads; native per-run
  cancellation was pending at this pin. No native operator ACP contract is established; that does
  not prove a custom harness endpoint cannot work. [Completion][operator-completion],
  [implemented/planned stop behavior][operator-stop].

The SandboxClaim path already uses Agent Sandbox. Adoption could replace our **direct integration**
while retaining Agent Sandbox beneath Lightspeed. Its separate bare-Pod mode does not qualify
Konveyor's chosen execution path. [Mode dispatch][operator-sandbox], [Sandbox
resources][operator-template].

## Questions for joint design

1. Is the reusable session core useful upstream? What callback/event interface fits Lightspeed's
   consumers, and should ACP remain optional?
2. What usage/cost reporting covers inspection and timely budget enforcement?
3. What supported contract allows invocation image selection and harness startup? What deployment
   arrangement provides complete Hub/provider/input/credential/ACP access?
4. How do prompt completion, handoff, Git finalization, Result publication, deadlines,
   cancellation, and emergency suspension compose?
5. Later, how should reusable capabilities and independent workflows converge? Current Lightspeed
   Agent model/prompt configuration and fixed inline workflow steps do not replace all Konveyor
   Agent/AgentWorkflow behavior. Resource migration remains separate from runtime reuse and
   initial operator execution.

[adr]: https://github.com/konveyor/agentic-controller/blob/ec36e644cdc8983d82263edba3a1172355da3ddc/docs/adr/0021-shared-runtime-and-conditional-lightspeed-execution.md
[controller-branch]: https://github.com/djzager/agentic-controller/tree/feat/lightspeed-runtime-integration
[controller-pin]: https://github.com/djzager/agentic-controller/tree/73a44d59f9e4cf6644ab4fc48f42211a7eff2548
[runtime-branch]: https://github.com/djzager/lightspeed-agentic-sandbox/tree/feat/reusable-sessions-acp
[runtime-pin]: https://github.com/djzager/lightspeed-agentic-sandbox/tree/ecbd76a613ed43e580c2481fac4cb1f2781edb67
[runtime-base]: https://github.com/openshift/lightspeed-agentic-sandbox/commit/5ea23f34ad2ad5fc01fb6264fb9304116664dd3d
[ui-branch]: https://github.com/djzager/tackle2-ui/tree/fix/lightspeed-acp-ui
[ui-pin]: https://github.com/djzager/tackle2-ui/tree/aa4f9b93b98d029ba2652e8b645b53018dd1de1c
[session]: https://github.com/djzager/lightspeed-agentic-sandbox/blob/ecbd76a613ed43e580c2481fac4cb1f2781edb67/src/lightspeed_agentic/session.py
[acp]: https://github.com/djzager/lightspeed-agentic-sandbox/blob/ecbd76a613ed43e580c2481fac4cb1f2781edb67/src/lightspeed_agentic/acp/README.md
[prototype]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/docs/lightspeed-runtime-prototype.md
[evidence]: https://github.com/djzager/agentic-controller/blob/73a44d59f9e4cf6644ab4fc48f42211a7eff2548/hack/lightspeed-runtime/reference.json
[coolstore-branch]: https://github.com/konveyor-ecosystem/coolstore/tree/mta-test
[coolstore-pin]: https://github.com/konveyor-ecosystem/coolstore/tree/32c1722efdf883f5f400170adb770a46a9b58116
[operator-pin]: https://github.com/openshift/lightspeed-agentic-operator/commit/9973d0002c78d843ec838ee64fd3f5221a094faa
[operator-agent]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/agent_types.go#L99-L154
[operator-run]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/agenticrun_types.go#L263-L390
[operator-config]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/pkg/configuration/config.go#L185-L195
[operator-builder]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/podspec_builder.go#L58-L227
[operator-placement]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_manager.go#L197-L246
[operator-watch]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/reconciler.go#L416-L437
[operator-approval]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/approval.go#L86-L108
[operator-scope]: https://github.com/openshift/lightspeed-agentic-operator/tree/9973d0002c78d843ec838ee64fd3f5221a094faa/config/crd/bases
[operator-provider]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/resolve.go#L62-L81
[operator-secrets]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/api/v1alpha1/tools_types.go#L87-L147
[operator-mounts]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/podspec_builder.go#L481-L524
[operator-template]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_manager.go#L601-L723
[operator-identity]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_manager.go#L300-L358
[operator-completion]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/pod_handler.go#L253-L324
[operator-stop]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/.ai/spec/what/run-lifecycle.md#L117-L119
[operator-sandbox]: https://github.com/openshift/lightspeed-agentic-operator/blob/9973d0002c78d843ec838ee64fd3f5221a094faa/controller/agenticrun/sandbox_manager.go#L249-L276
