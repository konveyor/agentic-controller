# End-to-end tests

These tests exercise the controller against a real Kubernetes cluster, Agent
Sandbox, and the local LLEmulator. The shell scenarios are the primary E2E
coverage for agent execution and workflow reconciliation.

## Prerequisites

Install and put these tools on `PATH`:

- Go
- `kubectl`
- Kind
- Helm
- Docker or Podman

The scripts use the Kind cluster `agentic-controller-e2e` by default. Podman
on macOS runs through its VM; make sure the VM has enough memory for the Kind
control plane and Agent Sandbox.

## Create the test cluster

From the repository root:

```sh
export KIND_CLUSTER=agentic-controller-e2e
export CONTAINER_TOOL=podman   # omit this to auto-detect, or use docker

./hack/start-kind.sh
kubectl config use-context "kind-${KIND_CLUSTER}"
./hack/setup-e2e.sh
```

`start-kind.sh` creates the cluster, installs Agent Sandbox, and deploys the
mock LLM service. `setup-e2e.sh` builds and loads the controller image, the
normal E2E agent image, the E2E-only workflow stub image, and the skill images,
then deploys the controller.

The workflow stub image is built from the normal agent image using
`test/e2e/agent-stub/`. It is used only by the restart/reconcile scenario and
does not change the production agent image.

## Run the shell E2E scenarios

Full AgentRun pipeline, including Gateway, SkillCard, parameter delivery,
skill mounting, ACP readiness, and Sandbox behavior:

```sh
E2E_TIMEOUT=300s ./hack/run-e2e.sh
```

Skill delivery scenarios, including enumeration, identity, pruning,
collisions, unusable skills, and collection cleanup:

```sh
E2E_TIMEOUT=300s ./hack/run-e2e-skills.sh
```

AgentWorkflowRun restart and reconciliation. This creates two stages,
restarts the controller while stage A is active, and verifies that stage A is
not duplicated, stage B is created, and the workflow succeeds:

```sh
E2E_NAMESPACE=default \
E2E_CONTROLLER_NAMESPACE=agentic-controller-system \
E2E_TIMEOUT_SECONDS=300 \
./hack/run-e2e-workflow-reconcile.sh
```

The workflow script cleans up its resources on exit. The skill scenario deletes
its temporary namespace on exit. The full pipeline removes stale named
resources before each run and leaves the current resources available for
inspection; delete them manually when finished if needed:

```sh
kubectl delete -f hack/e2e/resources.yaml --ignore-not-found
```

## Run the Go Kind suite

The scaffolded Ginkgo suite is separate from the shell scenarios and is
enabled with the `e2e` build tag:

```sh
go test -tags=e2e ./test/e2e -v
```

It builds and loads a manager image, installs CRDs and Cert-Manager as needed,
deploys the controller, and removes what it installed during cleanup. Use a
Kind context with sufficient permissions before running it.

## Inspect a failure

Check the controller, Agent Sandbox, and test workload state:

```sh
kubectl get pods -A
kubectl get agent,agentrun,agentworkflow,agentworkflowrun -A -o wide
kubectl get sandbox -A -o wide
kubectl logs -n agentic-controller-system deployment/agentic-controller-controller-manager
```

For the workflow scenario, inspect the parent and its stage children:

```sh
kubectl -n default get agentworkflowrun e2e-workflow-run -o yaml
kubectl -n default get agentrun \
  -l konveyor.io/agentworkflowrun=e2e-workflow-run -o wide
```

If a Kind node was killed or the cluster is stale, recreate it and rerun the
setup:

```sh
kind delete cluster --name "${KIND_CLUSTER:-agentic-controller-e2e}"
./hack/start-kind.sh
./hack/setup-e2e.sh
```

## Remove the cluster

```sh
kind delete cluster --name "${KIND_CLUSTER:-agentic-controller-e2e}"
```
