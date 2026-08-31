# ADR Reconciliation

Reviewed against the repository implementation on **2026-08-31**. This is
the index of the current review; the ADRs remain the authoritative record of
their decisions and dated amendments.

| ADR | Review result |
| --- | --- |
| 0001 | Core CRD, workflow, git-persistence, and Agent/AgentRun decisions remain useful. The CRD list, Gateway name, params delivery, skill delivery, Hub API, and commit-authorship details are amended in the ADR or superseded by ADRs 0006, 0007, 0009, 0015, and 0016. |
| 0002 | Controller remains outside ACP and clients use the pod endpoint. The endpoint ownership and human-in-the-loop topology are revised by ADR 0008; the current entry point fronts port 4000 and Goose uses loopback port 4001. |
| 0003 | Superseded by ADR 0006; retained as historical rationale. |
| 0004 | Remains the deferred OpenShell target. ADR 0016 records the accepted current Gateway CRD and direct Agent Sandbox implementation. |
| 0006 | Remains proposed/external to this repository. The Hub addon pattern is still the intended integration boundary; the entry point now receives parameters through `params.json`, as recorded in the ADR amendment. |
| 0007 | Accepted and implemented as the minimal entry point contract. The 2026-08-27 amendment records that it does not create `.gitignore` or grounding-data commits. |
| 0008 | The tee topology is implemented and enabled by default in the entry point. Its status remains proposed pending the project’s formal acceptance process. |
| 0009 | `params.json`, typed coercion, substitution, and the workflow/agent sections are implemented. ADR 0018 supersedes its guide-scope detail. |
| 0010 | The boundary remains the intended rule, but the repository’s shipped skills still contain some execution-control and container-layout instructions. The ADR remains proposed and this gap is explicit. |
| 0011 | Execution controls, cost monitoring, native turn limits, mode translation, and termination reporting are implemented. ADR 0018 supersedes its AgentRun field-placement and exit-2 mapping. |
| 0012 | Remains a proposed client contract based on the external prototype; no contradictory client implementation is maintained in this repository. |
| 0013 | Remains a proposed platform-resolved-parameter contract based on the external prototype; the current controller remains parameter-source agnostic. |
| 0014 | Native skill discovery, always-loaded rules, and the loader metadata path are implemented; catalog relocation is recorded by ADR 0017 (catalog layout). |
| 0015 | OCI, git, inline, bundle, loader, and validation behavior is implemented. Its former “prototype held back” wording is amended to describe the shipped implementation. |
| 0016 | Accepted current state: Gateway CRD, direct Agent Sandbox creation, verification Jobs, and provider-specific credential injection. OpenShell remains deferred. |
| 0017 (ask-user) | `ask_user` MCP, ACP elicitation forwarding, fail-closed behavior, and resolution frames are implemented in the entry point. The ADR’s status remains proposed pending formal acceptance. |
| 0017 (catalog layout) | The `catalog/` layout and maintainer `skills/` split are implemented. The ADR remains proposed pending formal acceptance. |
| 0018 | Execution fields on AgentRun, workflow-stage stamping, `Succeeded` terminal condition, and guide scoping are implemented. The ADR remains proposed pending formal acceptance. |

Future reviews should update this table and, when the ADR is materially
amended, its `Last updated` field together. A review date alone is not a
substitute for an amendment when implementation has drifted.
