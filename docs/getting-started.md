# Getting Started

This guide walks through deploying the agentic controller, creating a
Gateway with LLM credentials, defining an Agent, and creating an
AgentRun to trigger execution.

## Prerequisites

- Kubernetes 1.33+ or OpenShift 4.20+ (the controller mounts skills via
  the ImageVolume feature — a beta gate that is **off by default** on
  Kubernetes 1.33–1.34, so enable `ImageVolume` there; it is on by
  default from 1.35 and GA in 1.36)
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
AGENT_SANDBOX_TAG=v0.5.5

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

> **Note:** The clone + `helm install` path above and the upstream
> release manifest (`kubectl apply -f .../<tag>/sandbox-with-extensions.yaml`)
> are **alternative** install methods — use one, not both. Mixing them
> makes helm and `kubectl apply` fight over the same cluster-scoped CRDs,
> and backing out means deleting those CRDs (taking every Sandbox on the
> cluster with them). Note also that the release assets were renamed at
> v0.5.2 (`manifest.yaml` → `sandbox.yaml`), so pin a v0.5.2+ tag if you
> follow the release-manifest path.

> **Future:** [OpenShell](https://github.com/NVIDIA/OpenShell) will
> replace the direct Agent Sandbox dependency. When integrated, the
> controller will provision sandboxes through the OpenShell gateway
> API instead of creating Sandbox CRs directly. See
> [ADR 0004](adr/0004-openshell-as-execution-interface.md).

## 2. Deploy the controller and default skills

The default image `quay.io/konveyor/agentic-controller:latest` is public
and rebuilt on every merge to `main`, so you can deploy straight away
with no build step:

```bash
# Deploy CRDs, RBAC, and the controller manager
make deploy
```

To build and push your own image instead (e.g. to test local changes),
set `IMG` to a registry you can push to:

```bash
export IMG=quay.io/<your-org>/agentic-controller:dev
make docker-build docker-push IMG=$IMG
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
  --from-file=GOOGLE_APPLICATION_CREDENTIALS_JSON="$HOME/.config/gcloud/application_default_credentials.json" \
  --from-literal=GCP_PROJECT_ID="$(gcloud config get-value project)" \
  --from-literal=GCP_LOCATION=global
```

The whole Secret is exposed to the agent via `envFrom`, so `GCP_PROJECT_ID`
and `GCP_LOCATION` ride along with the credentials file. goose's Vertex
provider **requires** `GCP_PROJECT_ID` (there is no default and the run
fails at first token without it). `GCP_LOCATION` is optional — goose
defaults to `us-central1` — but `global` matches the sample Gateway
endpoint.

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

`AWS_REGION` is what goose actually uses to reach Bedrock — the harness
derives the Bedrock endpoint from it and ignores the Gateway `endpoint`,
which only feeds the controller's connectivity check. Keep the Gateway
`endpoint` in the same region as `AWS_REGION` so that check stays
meaningful. The model's `us.` prefix is a **cross-region inference
profile** spanning the US regions (us-east-1/us-east-2/us-west-2), so
switching between US regions needs no model change — only moving to
another geo (`eu.`, `apac.`) requires a new prefix.

### Option E: xAI (Grok)

```bash
kubectl create secret generic grok-credentials \
  --from-literal=api-key="<your-xai-api-key>"

kubectl apply -f config/samples/gateway_xai.yaml
```

Verify the Gateway is ready:

```bash
kubectl get gateways.konveyor.io
```

> **Note:** Use the fully-qualified `gateways.konveyor.io` rather than the
> bare `gateways`. On any cluster with the Gateway API CRDs installed
> (OpenShift 4.19+ does this by default), `gateways` resolves to
> `gateways.gateway.networking.k8s.io` instead, so `kubectl get gateways`
> would report no resources right after you applied your Gateway.

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
selects a Gateway, carries task-specific instructions, and sets the
environment the entry point needs. The controller validates the
configuration, creates an Agent Sandbox, and tracks the run to
completion.

> **Prerequisite — Konveyor Hub.** The `agent-java` image resolves the
> repository to migrate and its git credentials from a Konveyor Hub,
> keyed by `APP_ID`. Before running, install Hub — `hack/install-konveyor.sh`
> installs the tackle2-operator with auth disabled — and register the
> application you want to migrate, noting its `APP_ID`. The sample
> AgentRun's `spec.env` points at the in-cluster Hub service with
> `APP_ID: "1"`; edit `HUB_BASE_URL`, `APP_ID`, and `TARGET_BRANCH` to
> match your Hub and application. Hub-free standalone runs are not
> supported yet
> ([#122](https://github.com/konveyor/agentic-controller/issues/122)).

Apply the example AgentRun:

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
| `gateway_xai.yaml` | Gateway | xAI (Grok) |
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
kubectl delete gateways.konveyor.io --all
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
- `kubectl get gateways.konveyor.io` — all referenced gateways must exist
- `kubectl get skillcards` — all referenced skills must be resolved

**AgentRun stuck in `Pending`**

The controller is waiting for dependencies. Check:
- The referenced Agent is `Ready`
- The selected Gateway is in the Agent's gateway list
- Agent Sandbox is installed and healthy (step 1)
- Controller logs: `kubectl logs -n agentic-controller-system deploy/agentic-controller-controller-manager`
