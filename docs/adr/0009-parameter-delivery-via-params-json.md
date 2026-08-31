---
adr: "0009"
title: "Parameter Delivery via params.json"
description: "Defines a structured params.json contract for delivering typed workflow and agent values to the harness."
status: proposed
date: "2026-08-05"
last_updated: "2026-08-31"
authors:
  - "David Zager"
last_reviewed: "2026-08-31"
implementation_status: amended
review_note: "params.json, typed coercion, substitution, and the workflow/agent sections are implemented. ADR 0018 supersedes its guide-scope detail."
---

# ADR 0009: Parameter Delivery via params.json

**Update (2026-08-31):** The `params.json` carrier and typed workflow/agent
sections are implemented. ADR 0018 adds uniform execution fields to
`AgentRun`, stamps workflow-stage values onto child AgentRuns, and narrows
workflow-guide substitution to `workflow.*`; the original guide-scope text
below is historical where it conflicts with ADR 0018.

## Context

The controller injects Agent parameters into the Sandbox as individual
environment variables (`KONVEYOR_PARAM_{NAME}`). The harness hard-codes
which ones it reads — today only `KONVEYOR_PARAM_MAX_TURNS`. All other
params are ambient: present in the container environment but consumed
only if a skill tells the LLM to run `echo $KONVEYOR_PARAM_FOO` via a
shell tool call.

This has several problems:

- **The harness must be updated for every new parameter.** Adding a
  param requires a corresponding code change in the harness to read
  the new env var by name.
- **Skills reference env vars via natural language.** The verify skill
  tells the LLM to "read `KONVEYOR_PARAM_MAX_FIX_ITERATIONS` from
  environment" — a shell tool call the LLM burns a turn on to learn
  something the harness already knew.
- **Env vars have quoting, escaping, and size limitations.** Complex
  values (JSON, multi-line text) are fragile as env vars.
- **No structured typing.** The `AgentParamType` field (string, number,
  boolean) is declared on the CRD but never enforced — values are
  always strings in env vars.

The Ansible Playbook Bundle pattern (passing all parameters as a single
JSON blob `_apb_plan_parameters`) and Ansible Runner (mounting
`extravars` as a JSON/YAML file) demonstrate that a file-based
structured delivery mechanism is simpler and more robust than
per-parameter env vars.

## Decision

### Delivery mechanism

The controller writes all resolved parameter values to a single JSON
file mounted at `/run/konveyor/params.json` in the Sandbox container.
Individual `KONVEYOR_PARAM_*` environment variables are removed.

### File shape

The file contains three sections:

```json
{
  "workflow": {
    "application_name": "coolstore",
    "target_framework": "quarkus"
  },
  "agent": {
    "source_url": "https://github.com/example/app",
    "dry_run": true,
    "max_fix_iterations": 5
  },
  "execution": {
    "maxTurns": 200,
    "maxCost": "10.00",
    "mode": "auto"
  }
}
```

- **`workflow`** — workflow-level params declared on AgentWorkflow,
  supplied on AgentWorkflowRun. Absent for standalone AgentRuns.
- **`agent`** — agent-level params declared on Agent, supplied on
  AgentRun. Values are type-coerced: numbers as JSON numbers, booleans
  as JSON booleans, strings as JSON strings, matching the
  `AgentParamType` declaration on the CRD.
- **`execution`** — resolved execution controls (see ADR 0011). These
  are first-class CRD fields with defined semantics, not arbitrary
  params. Separated so the harness knows where to find them without
  scanning agent params for magic names.

### Prompt substitution

All text fields that compose the agent prompt — `Agent.Spec.Prompt`,
`AgentRun.Spec.Instructions`, `AgentWorkflow.Spec.Guide`, and
`AgentWorkflow.Spec.Stages[].Instructions` — support variable
substitution using Tekton-style `$(scope.name)` syntax. The
controller performs simple string replacement before passing text to
the Sandbox. No template engine is used.

References are namespaced to avoid collisions between workflow and
agent parameters:

- `$(agent.source_url)` — agent-level params
- `$(workflow.application_name)` — workflow-level params

If both scopes declare a param with the same name, the namespaced
references are unambiguous. The controller rejects unresolved
references during reconciliation — a reference to an undeclared
parameter is a configuration error and does not produce a Sandbox.
Literal `$(` text that does not match a declared parameter passes
through unchanged.

```yaml
# Agent spec
prompt: |
  Migrate the application at $(agent.source_url) to $(agent.target_framework).
params:
  - name: source_url
    type: string
    required: true
  - name: target_framework
    type: string
    default: "quarkus"
```

```yaml
# AgentWorkflow spec
guide: |
  This workflow migrates $(workflow.application_name) to $(workflow.target_framework).
params:
  - name: application_name
    type: string
  - name: target_framework
    type: string
```

The controller performs substitution at reconciliation time. The
harness receives fully rendered text — no substitution engine is
needed in the harness. This syntax avoids collisions with `{{`
delimiters used by Helm, Jinja, and Go templates that may appear in
prompt content (e.g. when migrating applications that use these
templating systems).

### Agent-facing parameter visibility

The harness appends a "Parameters" section to the prompt so the agent
sees all values as text, regardless of whether the prompt author
referenced them in a template:

```
## Parameters

### Workflow
- application_name: coolstore
- target_framework: quarkus

### Agent
- source_url: https://github.com/example/app
- dry_run: true
```

This eliminates the pattern of skills telling the LLM to read env vars
or files. The agent sees parameter values directly in its instructions.

### Workflow-level params

AgentWorkflow declares its own params, separate from any individual
Agent's params. AgentWorkflowRun supplies values. All text fields
render from the same namespaced data context (`workflow.*` and
`agent.*`), so a workflow guide can reference agent params and vice
versa if needed. The controller passes agent-level params through to
each stage's AgentRun.

## Consequences

- Skills and agents no longer reference `KONVEYOR_PARAM_*` env vars.
  Existing skills that do must be updated.
- The harness gains a generic parameter loader, replacing hard-coded
  env var reads.
- The prompt construction pipeline gains a "Parameters" layer.
- AgentWorkflow gains a `params` field for workflow-level parameter
  declarations.
- The controller gains substitution responsibility — a mechanical
  string replacement step using Tekton-style `$(scope.name)` syntax.
- The `/run/konveyor/params.json` path becomes part of the harness
  contract. Any harness implementation must read this file.
- The file shape (workflow / agent / execution sections) is part of
  the contract. User-facing documentation must specify this for
  third-party harness implementors.
