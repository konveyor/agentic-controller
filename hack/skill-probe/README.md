# Skill delivery probes

Three scripts, in increasing order of what they need. Together they exercise
every claim ADR 0014 and ADR 0015 make that a unit test cannot reach.

| script | needs | answers |
|---|---|---|
| `run-probe.sh` | a cluster with the ImageVolume gate | pod shape: does the loader assemble, and does it fail the pod when it should |
| `run-collection-probe.sh` | the above, plus a deployed controller | reconciler behaviour: enumeration, pruning, ownership, the #153 collision |
| `run-rule-probe.sh` | the above, plus Hub and a real gateway | does an always-loaded rule actually change what a live model does |

Everything below envtest lives here because envtest has no kubelet, so nothing
about ImageVolumes, init containers or `$HOME` isolation is reachable from
`make test`.

Each run creates its own namespace and deletes it on exit. That is not tidiness:
an enumeration probe once passed against a hand-made ServiceAccount left in
`default` from an earlier session, while the code that should have created it
did not exist. A run in a namespace nothing else has touched is the only one
whose result means what it says.

## Cluster

The ImageVolume feature gate is beta in 1.34 and must be on. Measured against
minikube with CRI-O 1.35.0 and Kubernetes v1.34.0:

```sh
minikube start -p agentic-dev --container-runtime=cri-o \
  --kubernetes-version=v1.34.0 --driver=podman --feature-gates=ImageVolume=true
```

## run-probe.sh

No controller needed. Builds its own images and drives raw pods shaped the way
the AgentRun controller shapes them.

```sh
hack/skill-probe/run-probe.sh
```

## run-collection-probe.sh

Needs the controller. The loader and the enumeration Job both run the
controller's own image, which kustomize sets as `SKILL_LOADER_IMAGE` alongside
the manager's, so there is nothing extra to configure.

```sh
make deploy IMG=<controller image>
hack/skill-probe/run-collection-probe.sh
```

## run-rule-probe.sh

Needs a Hub the harness can resolve an application from, and a Gateway with a
real key. It costs a couple of model calls.

Hub does not need the agent-side integration; the harness only calls existing
APIs (`Application.Get`, an identity search, analysis insights, token revoke).
Stock `quay.io/konveyor/tackle2-hub` works with auth off, which is the cheapest
way to run this. Two things are not obvious:

- Hub loads a tenant at startup and needs the `tackle.konveyor.io` CRDs even
  with auth disabled. Apply them from `tackle2-operator/helm/templates/crds/`.
- Its ServiceAccount needs to read `identityproviders`, or it exits at boot.

With `AUTH_ENABLED=false` no token is needed, so `HUB_TOKEN` can be left unset.
Create an application pointing at a repository you do not mind a run touching,
and note its id.

```sh
AGENT_IMAGE=<agent image> \
SKILL_IMAGE=<skill bundle image> \
LLM_API_KEY=<a real key> \
HUB_BASE_URL=http://tackle-hub.konveyor-hub.svc:8080 \
APP_ID=1 \
  hack/skill-probe/run-rule-probe.sh
```

The harness pushes commits to `TARGET_BRANCH` when a stage ends. Point the Hub
application at a fork if you would rather that went somewhere harmless. The
probe's task does not modify files, so in practice it produces no commits and
the push is skipped.
