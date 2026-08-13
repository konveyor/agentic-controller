# Getting Started

This guide walks through deploying the agentic controller, creating a
Gateway with LLM credentials, defining an Agent, and creating an
AgentRun to trigger execution.

## Prerequisites

- Kubernetes 1.33+ (ImageVolume GA) or OpenShift 4.20+
- [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox)
  v0.5.x installed in the cluster
- `kubectl` and `helm` configured to talk to the cluster
- LLM provider credentials (e.g. GCP Vertex AI, OpenAI, Anthropic,
  AWS Bedrock)

## 1. Install Agent Sandbox

The controller creates Agent Sandbox CRs to run agent workloads.
Agent Sandbox must be installed before the controller can execute
AgentRuns.

```bash
AGENT_SANDBOX_TAG=v0.5.0

# Clone and install via Helm
git clone --depth 1 --branch $AGENT_SANDBOX_TAG \
  https://github.com/kubernetes-sigs/agent-sandbox.git /tmp/agent-sandbox

helm install agent-sandbox /tmp/agent-sandbox/helm/ \
  --namespace agent-sandbox-system \
  --create-namespace \
  --set image.tag=$AGENT_SANDBOX_TAG

# Wait for the controller to be ready
kubectl wait deployment/agent-sandbox-controller \
  --namespace agent-sandbox-system \
  --for=condition=Available \
  --timeout=120s
```

> **Future:** [OpenShell](https://github.com/NVIDIA/OpenShell) will
> replace the direct Agent Sandbox dependency. When integrated, the
> controller will provision sandboxes through the OpenShell gateway
> API instead of creating Sandbox CRs directly. See
> [ADR 0004](adr/0004-openshell-as-execution-interface.md).

## 2. Deploy the controller and default skills

Build and push the controller image, then deploy with kustomize:

```bash
# Build the controller image (adjust IMG for your registry)
export IMG=quay.io/konveyor/agentic-controller:latest
make docker-build docker-push IMG=$IMG

# Deploy CRDs, RBAC, and the controller manager
make deploy IMG=$IMG
```

This creates the `agentic-controller-system` namespace and deploys
the controller. Verify it's running:

```bash
kubectl get pods -n agentic-controller-system
```

Deploy the default SkillCard and SkillCollection resources (these
are not included in `make deploy` to avoid name-prefix conflicts):

```bash
kubectl apply -k config/samples/
```

To install only the CRDs without deploying the controller (e.g. for
local development with `make run`):

```bash
make install
```

Alternatively, generate a single consolidated YAML containing CRDs
and the controller deployment — useful when you don't want to build
from source:

```bash
make build-installer IMG=$IMG
kubectl apply -f dist/install.yaml
```

## 3. Create a Gateway

A Gateway represents a single LLM provider/model combination with
credentials. Each Gateway serves exactly one model.

> **Note:** Gateway replaces the former `LLMProvider` CRD. If you
> have existing `LLMProvider` resources, they must be recreated as
> Gateways — one Gateway per provider/model combination.

The Secret commands below use `--from-literal` for brevity, which
records the key value in your shell history. For anything beyond a
throwaway test cluster, prefer `--from-file` (reading the value from a
protected file) or an external secret manager.

### Option A: GCP Vertex AI

Create a Secret with your GCP application default credentials:

```bash
gcloud auth application-default login

kubectl create secret generic vertex-credentials \
  --from-file=GOOGLE_APPLICATION_CREDENTIALS_JSON="$HOME/.config/gcloud/application_default_credentials.json"
```

Apply the Gateway:

```bash
kubectl apply -f config/samples/gateway_vertex_ai.yaml
```

### Option B: OpenAI

```bash
kubectl create secret generic openai-credentials \
  --from-literal=api-key="<your-openai-api-key>"

kubectl apply -f config/samples/gateway_openai.yaml
```

### Option C: Anthropic

```bash
kubectl create secret generic anthropic-credentials \
  --from-literal=api-key="<your-anthropic-api-key>"

kubectl apply -f config/samples/gateway_anthropic.yaml
```

### Option D: AWS Bedrock

```bash
kubectl create secret generic bedrock-credentials \
  --from-literal=AWS_ACCESS_KEY_ID="<your-access-key-id>" \
  --from-literal=AWS_SECRET_ACCESS_KEY="<your-secret-access-key>" \
  --from-literal=AWS_REGION="us-east-1"

kubectl apply -f config/samples/gateway_aws_bedrock.yaml
```

The `AWS_REGION` above must match the region in the Gateway's
`endpoint` and the region prefix in its `model` name (both `us-east-1`
in the sample). To use a different region, edit `endpoint`,
`AWS_REGION`, and the model's inference-profile prefix together in
`config/samples/gateway_aws_bedrock.yaml`.

Verify the Gateway is ready:

```bash
kubectl get gateways
```

The `Verified` column shows whether the controller confirmed
connectivity to the endpoint.

## 4. Create an Agent

An Agent is a template that declares what is available for execution:
a container image, gateways, skills, a prompt, and typed parameters.
Creating an Agent does not execute anything.

> **Note:** The example `agent_example.yaml` and `agentrun_example.yaml`
> reference the Vertex AI Gateway (`gcp-vertex-ai`) from Option A. If
> you created a different Gateway (Options B–D), update the
> `spec.gateways[].ref` in the Agent and the `spec.gateway` in the
> AgentRun to match your Gateway's name before applying them.

Verify the default SkillCards were deployed (from step 2):

```bash
kubectl get skillcards
```

Apply the example Agent:

```bash
kubectl apply -f config/samples/agent_example.yaml
```

Check that the Agent is ready (referenced Gateways and SkillCards
must exist and be healthy):

```bash
kubectl get agents
```

## 5. Create an AgentRun

An AgentRun triggers execution of an Agent. It references an Agent,
selects a Gateway, supplies parameter values, and carries
task-specific instructions. The controller validates the
configuration, creates an Agent Sandbox, and tracks the run to
completion.

Apply the example AgentRun (edit it first to set your source
repository URL):

```bash
kubectl apply -f config/samples/agentrun_example.yaml
```

Watch the run:

```bash
kubectl get agentruns -w
```

Once the phase moves to `Running`, the Sandbox pod is live. View
agent logs:

```bash
# Get the sandbox pod name from the AgentRun status
SANDBOX=$(kubectl get agentrun migration-run-001 -o jsonpath='{.status.sandboxName}')
kubectl logs -f $SANDBOX
```

The AgentRun spec is **immutable** — to change values, delete the
AgentRun and create a new one.

## 6. Workflows (optional)

For multi-stage work (e.g. plan, execute, verify), use
AgentWorkflow and AgentWorkflowRun. See
`hack/harness-test/workflow-resources.yaml` for a complete example
that migrates a Java EE application to Quarkus using three stages.

## Sample manifests

All sample CRs are in `config/samples/`:

| File | Kind | Description |
|------|------|-------------|
| `gateway_vertex_ai.yaml` | Gateway | GCP Vertex AI with Claude |
| `gateway_openai.yaml` | Gateway | OpenAI GPT-4o |
| `gateway_anthropic.yaml` | Gateway | Anthropic direct API |
| `gateway_aws_bedrock.yaml` | Gateway | AWS Bedrock |
| `agent_example.yaml` | Agent | Java migration agent |
| `agentrun_example.yaml` | AgentRun | Triggers the migration agent |
| `skillcard_*.yaml` | SkillCard | Migration skills (applied via `kubectl apply -k config/samples/`) |
| `skillcollection_*.yaml` | SkillCollection | Grouped skills (applied via `kubectl apply -k config/samples/`) |

## Local development

Run the controller locally against a cluster (CRDs must be
installed):

```bash
make install   # Install CRDs
make run       # Run the controller from your host
```

## End-to-end testing with Kind

The project includes scripts for running the full stack in a Kind
cluster:

```bash
make e2e-setup    # Create Kind cluster + deploy Agent Sandbox + controller
make e2e-run      # Run e2e tests
make e2e-cleanup  # Tear down the Kind cluster
```

## Cleanup

`kubectl delete --all` acts on the current namespace — set your
context (or add `-n <namespace>`) so you don't remove resources you
meant to keep:

```bash
# Delete runs, agents, and gateways in the current namespace
kubectl delete agentruns --all
kubectl delete agents --all
kubectl delete gateways --all
```

> **Warning:** `make undeploy` and `make uninstall` delete the CRDs,
> which are cluster-scoped. Deleting a CRD removes **every** custom
> resource of that type across **all** namespaces — not just the ones
> from this guide. Run these only if you intend to tear down the
> controller entirely.

```bash
# Undeploy the controller
make undeploy

# Or just uninstall CRDs
make uninstall
```

## Future: operator integration

The deployment method described here (kustomize / `dist/install.yaml`)
is a stopgap. The planned path is OLM-managed operator packaging,
which will provide catalog integration, upgrade lifecycle, and
dependency resolution for Agent Sandbox. The sample CRs in
`config/samples/` are structured to be compatible with OLM bundle
conventions (one resource per file, no templated placeholders).

## Troubleshooting

**Gateway shows `Verified: false`**

The controller could not reach the endpoint. Check:
- The endpoint URL is correct
- The credential Secret exists and has the right keys
- Network policies allow egress from the controller namespace

**Agent shows `Ready: False`**

The Agent references Gateways or SkillCards that don't exist or
aren't ready. Check:
- `kubectl get gateways` — all referenced gateways must exist
- `kubectl get skillcards` — all referenced skills must be resolved

**AgentRun stuck in `Pending`**

The controller is waiting for dependencies. Check:
- The referenced Agent is `Ready`
- The selected Gateway is in the Agent's gateway list
- Agent Sandbox is installed and healthy (step 1)
- Controller logs: `kubectl logs -n agentic-controller-system deploy/agentic-controller-controller-manager`
