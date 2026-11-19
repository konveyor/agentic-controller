# Hub permissions for AgentLearning proposals and review

Research date: 2026-11-18. Source inspection pins upstream `konveyor/tackle2-hub`
to [`fabeb1e3aa1fddaf394ee0857038a354b055f4df`](https://github.com/konveyor/tackle2-hub/commit/fabeb1e3aa1fddaf394ee0857038a354b055f4df),
the observed `main` revision. No Hub implementation changes or integration tests
were performed. Recommendations below support proposed
[ADR 0024](../adr/0024-agent-learnings.md), not implemented guarantees.

## Finding

Hub already has the identity and resource-permission framework needed to add
learning operations. It does **not** already have a learning approval permission
or a generic per-application approval ACL. Reusing authentication is sufficient
for identity; learning resource registration, grants, handlers, audit attribution,
and an explicit authorization-scope decision remain work.

The key distinction is **applicability** versus **authority**: a learning selector
describes which workloads can consume knowledge; Hub's current resource/method
scope describes which operations a caller may perform. Neither contains the
other. The code implementing scope matching accepts only a resource name and
HTTP method. [Scope matching](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/scope.go#L14-L40),
[required middleware](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/auth.go#L1514-L1542).

## What is implemented

- **Roles contain scope strings.** Current `Role.Scopes` is a JSON string array;
  there is no separate current `Permission` model to extend. Local users and Hub
  service accounts have role associations; IdP identities have explicit scopes.
  [Current models](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/migration/v24/model/core.go#L215-L266).
- **Permission names combine registered resources and methods.** `Required(name)`
  registers a resource and authorizes using the request's HTTP method. Domain
  seeding builds known scopes from registered names and the methods `decrypt`,
  `delete`, `get`, `patch`, `post`, and `put`; `approve` is not a supported method
  in this generated catalog. Dotted resources are explicit names, not an implied
  parent-child grant hierarchy. [Middleware](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/auth.go#L1514-L1542),
  [scope methods and matching](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/scope.go#L5-L40),
  [scope generation](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/domain.go#L189-L242).
- **Authentication sources converge on scopes.** Local users' scopes are derived
  from assigned roles. Federated OIDC extracts the access token's `scope` claim
  and expands `+role.name` references; LDAP group mapping selects Hub roles.
  Therefore new learning permissions need the appropriate local-role assignment,
  OIDC claim/role mapping, or LDAP role mapping as well as endpoint middleware.
  [Local/service-account role scopes](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/cache/cache.go#L731-L787),
  [OIDC identity and scopes](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/idp.go#L375-L425),
  [LDAP role mapping](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/ldap.go#L369-L407).
- **Existing agent resources establish the naming pattern.** Routes use
  `agentic.agents`, `agentic.agentruns`, `agentic.agentruns.acp`, and separate
  workflow names. Architect and migrator have broad run permissions; project
  manager is largely read-only for agent resources. No learning resource is
  registered or seeded here. [Agent routes](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/agentic.go#L40-L109),
  [seeded roles](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/seed/roles.yaml).
- **Defaults and custom roles need different rollout handling.** Seeded admin
  uses resource/method wildcards, expanded against registered resources. New
  registered resources therefore enter admin's seeded grants. Other seeded roles
  need explicit additions. Role create/update rejects unknown scope strings;
  domain seeding removes unregistered strings from user-created roles. Register
  permissions before configuring grants, and test upgrades/reloads. Existing
  role APIs can express a custom reviewer role using registered exact scopes.
  [Admin seed](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/seed/roles.yaml#L1-L9),
  [seed expansion](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/domain.go#L218-L243),
  [pruning](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/domain.go#L375-L409),
  [role API validation](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/auth.go#L942-L1045).
- **Application routes do not supply a reusable object-level authority check.**
  Application endpoints use `Required("applications")`; Get loads the requested
  ID, and List applies query filters. The inspected middleware/handlers do not
  evaluate a user-to-application or user-to-archetype ACL. This finding is limited
  to these source paths; it is not a claim about an external deployment policy.
  [Application routes and Get](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/application.go#L35-L137),
  [application List](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/application.go#L140-L250).

## Changes to propose for Hub

### Endpoint and workload-metadata baseline

The reviewed REST routes are under `/hub/agentic/`. AgentRun and WorkflowRun
create handlers bind a Kubernetes-shaped request, add the Hub token Secret and
`HUB_BASE_URL`, and create the CR in Hub's namespace. They do not resolve an
application association or stamp trusted application/archetype labels. The
generic `injectLabels` helper sets only `konveyor.io/managed`; run list handlers
list the namespace without an application-label filter. This differs from the
application-label/filtering target recorded in ADR 0006, and must be reconciled
against the actual Hub version used for a spike. It is not evidence that an
unmerged integration branch or another deployment lacks that work.
[Route constants](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/shared/api/pkg.go#L60-L79),
[AgentRun list/create](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/agentic.go#L695-L790),
[WorkflowRun list/create](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/agentic.go#L1066-L1161),
[label injection](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/agentic.go#L1221-L1230).

Hub needs trusted metadata projection on **both** create paths before learning
selection, including a defined representation for multiple archetypes. Validate
the source application, reserve the projected label keys, and reject or replace
forged labels while keeping them consistent with runtime application identity.
This is a proposed narrow create-time exception for selection metadata; it does
not move git credential or runtime grounding resolution out of the harness.
Client bindings/API documentation and review UI are also new work. Scope
registration is discoverable through `/auth/scopes`, and the existing role API
accepts scope strings, but UI support and new learning views have not been
inspected or demonstrated. [Scope/role endpoints](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/auth.go#L69-L82),
[role API shape](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/shared/api/core.go#L193-L205).

The controller repo's current Hub Role lists seven existing agentic resources
and Secrets, without AgentLearning or any approval subresource. Extend that
Role and the operator's corresponding deployment permissions after choosing the
protected approval write path; do not infer Kubernetes access merely from a
new Hub role grant. [Current Hub Kubernetes Role](https://github.com/konveyor/agentic-controller/blob/30a946674f91931b16c4e9bdb8c159e2656f7adb/config/hub-rbac/role.yaml#L1-L43).

### Permission and identity changes

1. **Add explicit learning permission resources and curated endpoints.** For
   example, use `agentic.learnings:get` for view/list and
   `agentic.learnings:post` for proposal creation, with separately registered
   `agentic.learnings.reviews:post` for accept/reject and
   `agentic.learnings.revocations:post` for revocation. These names are illustrative
   and unimplemented. A replacement is a new proposal and requires a new review;
   neither ordinary POST creation nor arbitrary PUT/PATCH should accept it.
   Separate nouns let the existing middleware distinguish operations that all
   happen to use POST without changing Hub's method vocabulary.
2. **Decide who receives the new grants.** One compatible initial policy is
   instance-wide learning review, with seeded admin authorization and an
   explicitly assigned custom reviewer role. Giving architect/migrator proposal
   or review grants is a product-policy decision, not something implied by their
   existing run permissions. Per-application/archetype reviewer authority would
   require an additional authorization model and handler checks, including the
   meaning of selectors that match future applications. That scope decision
   remains open; selectors themselves must not establish authority.
3. **Build trusted approval attribution.** The request context already exposes
   authenticated `CurrentSubject` and `CurrentUser`. The handler should derive
   reviewer identity server-side and bind the decision to the learning UID/digest.
   Resolve subject type for human-required approval: Hub distinguishes local
   users and federated identities from Hub service accounts and OAuth clients.
   A subject name, valid scope, or UI connection alone does not prove a personal
   human action. Define whether human-owned API keys are acceptable; machine
   identities must not masquerade as human reviewers. [Identity accessors](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/base.go#L120-L163),
   [subject kinds](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/cache/subject.go#L3-L92).
4. **Keep Hub tokens and Kubernetes identities separate.** Hub API-key tokens
   inherit the scopes of their bound subject; Hub service accounts themselves
   receive roles. A Kubernetes ServiceAccount/RoleBinding controls access to the
   Kubernetes API, not Hub endpoint access. Existing harness token creation uses
   the Hub subject `agentic.harness`; any proposal-through-Hub integration needs
   an explicit Hub grant and trusted run provenance. A direct-to-Kubernetes
   proposal path instead needs the planned Kubernetes identity and admission
   checks. Neither path should receive review permission. [API-key scope binding](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/cache/cache.go#L662-L725),
   [harness token creation](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/api/agentic.go#L1232-L1260).
5. **Specify permission freshness.** Existing JWT request authorization reads
   the token's scope claim; the inspected token validator checks validity and
   claims rather than re-evaluating current role grants. Inference: removing a
   role need not immediately remove a previously issued JWT's authority. State
   the supported token/refresh window or add a live authorization check if review
   permission removal must take effect immediately. API-key scopes have a
   different cache/binding path and need separate tests. [JWT scopes and validation](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/builtin.go#L302-L425),
   [API-key scope binding](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/cache/cache.go#L662-L725).

## Evidence required before claiming the Hub path works

Test local users, OIDC claims/role expansion, LDAP mapping, custom-role upgrades,
and permission removal with existing tokens. Test a proposer denied review,
forged reviewer fields ignored/rejected, replacement requiring new acceptance,
and direct Kubernetes writes denied independently of Hub. Human-required approval
also needs an explicit no-auth policy: Hub's no-auth provider grants `*:*`, uses
`admin.noauth`, and has no authenticated subject, so that mode cannot establish
the promised reviewer identity. [No-auth implementation](https://github.com/konveyor/tackle2-hub/blob/fabeb1e3aa1fddaf394ee0857038a354b055f4df/internal/auth/noauth.go#L7-L40).

The ADR should describe **new learning-specific Hub permissions**, and keep
application/archetype authority open until its policy is chosen. The existing
accounts/authentication system is reusable; its current resource-level scope
checks do not implement per-application approval authority.
