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
  v1.0.x installed in the cluster
- [Konveyor Hub](https://github.com/konveyor/tackle2-hub) installed in
  the cluster (step 2 covers this). Hub serves the agentic API the UI
  uses and resolves the repositories agents migrate; Hub-free runs are
  not supported yet
  ([#122](https://github.com/konveyor/agentic-controller/issues/122))
- `kubectl` and `helm` configured to talk to the cluster
- LLM provider credentials (e.g. GCP Vertex AI, OpenAI, Anthropic,
  AWS Bedrock)

> **Everything agentic lives in the Hub namespace.** Hub reads Agents,
> AgentRuns, Gateways, SkillCards and the other `konveyor.io` resources
> from its **own** namespace and nowhere else — there is no setting to
> point it elsewhere. Every `kubectl apply` and `kubectl create secret`
> in this guide therefore targets that namespace, `konveyor-tackle` by
> default, via `$KONVEYOR_NS`. Applying the Gateways or defaults into a
> different namespace (or leaving them in whatever your context happens
> to be) is the single most common way to end up with a UI that shows an
> error on every agentic page; see
> [Troubleshooting](#troubleshooting).

## 1. Install Agent Sandbox

The controller creates Agent Sandbox CRs to run agent workloads.
Agent Sandbox must be installed before the controller can execute
AgentRuns.

```bash
AGENT_SANDBOX_TAG=v1.0.0

# Clone and install via Helm
git clone --depth 1 --branch $AGENT_SANDBOX_TAG \
  https://github.com/kubernetes-sigs/agent-sandbox.git /tmp/agent-sandbox

# The chart renders its own Namespace by default; disable it with
# namespace.create=false so it does not collide with --create-namespace.
helm install agent-sandbox /tmp/agent-sandbox/helm/ \
  --namespace agent-sandbox-system \
  --create-namespace \
  --set namespace.create=false \
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

> **Upgrading an existing cluster:** Agent Sandbox v1.0.0 removes the
> legacy `v1alpha1` API and its conversion webhooks, so you **cannot**
> jump straight to it from v0.4.x / early v0.5.x. Follow the upstream
> [v1.0.0 API migration guide](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.0/docs/api-migration-guide.md)
> in order: (1) upgrade to a v0.5.x release; (2) run the
> [v0.5 storage migration](https://github.com/kubernetes-sigs/agent-sandbox/blob/v0.5.2/docs/api-migration-guide.md)
> to move all resources to `v1beta1` and prune legacy `storedVersions`;
> (3) verify every agent-sandbox CRD reports only `["v1beta1"]` in
> `status.storedVersions` (the apiserver rejects the upgrade otherwise);
> (4) upgrade to v1.0.0. Because Helm does **not** upgrade CRDs in a
> chart's `crds/` directory, apply the v1beta1 CRDs
> (`kubectl apply -f helm/crds/`) *before* `helm upgrade` — otherwise the
> chart removes the conversion-webhook Service while the old CRDs still
> reference it. Fresh installs (the command above) are unaffected.

> **Future:** [OpenShell](https://github.com/NVIDIA/OpenShell) will
> replace the direct Agent Sandbox dependency. When integrated, the
> controller will provision sandboxes through the OpenShell gateway
> API instead of creating Sandbox CRs directly. See
> [ADR 0004](adr/0004-openshell-as-execution-interface.md).

## 2. Install Konveyor Hub

Hub is where the agentic resources live and what the UI talks to. Install
it before creating any Gateway or Agent so there is a namespace to put
them in. `hack/install-konveyor.sh` installs the tackle2-operator via its
Helm chart with auth disabled, waits for Hub, and grants Hub's
ServiceAccount access to the `konveyor.io` resources in its namespace
(`config/hub-rbac/`):

```bash
export KONVEYOR_NS=konveyor-tackle   # the script's default; change both together
make konveyor-install
```

Every command below uses `-n "$KONVEYOR_NS"`. If you prefer, set it as
your context default instead and drop the flag:

```bash
kubectl config set-context --current --namespace="$KONVEYOR_NS"
```

Already have a Hub? Skip the install but still grant the RBAC, replacing
`konveyor-tackle` with your Hub's namespace in both places:

```bash
kubectl kustomize config/hub-rbac/ \
  | sed "s/konveyor-tackle/$KONVEYOR_NS/g" \
  | kubectl apply -f -
```

## 3. Deploy the controller and default resources

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

Deploy the default domain resources — the SkillCards and SkillCollection
that make up the skill catalog, plus the Agents and the
`java-ee-to-quarkus` AgentWorkflow the UI presents for users to run
(these are not included in `make deploy` to avoid name-prefix mangling
the cross-references between them). This is the same content the operator
installs on enable. They must land in the Hub namespace (see step 2) or
the UI cannot see them:

```bash
kubectl apply -k config/defaults/ -n "$KONVEYOR_NS"
```

The shipped Agents declare no gateway, so they install and become Ready
without a provider configured — you name a gateway when you run one (step
4 creates one). Inspect what was installed:

```bash
kubectl get skillcards,skillcollections,agents,agentworkflows -n "$KONVEYOR_NS"
```

> **Samples vs. defaults.** `config/defaults/` holds the curated content
> above — installed automatically by the operator. `config/samples/` holds
> illustrative CRs you copy and edit (the Gateway samples in step 4, plus a
> standalone example Agent and AgentRun); nothing there is auto-installed.

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

## 4. Create a Gateway

A Gateway represents a single LLM provider/model combination with
credentials. Each Gateway serves exactly one model. The Gateway and the
Secret it references must both be in the Hub namespace — the controller
resolves `credentialRef` within the Gateway's own namespace, and Hub only
lists Gateways from its own.

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

kubectl create secret generic vertex-credentials -n "$KONVEYOR_NS" \
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
kubectl apply -f config/samples/gateway_vertex_ai.yaml -n "$KONVEYOR_NS"
```

### Option B: OpenAI

```bash
kubectl create secret generic openai-credentials -n "$KONVEYOR_NS" \
  --from-literal=api-key="<your-openai-api-key>"

kubectl apply -f config/samples/gateway_openai.yaml -n "$KONVEYOR_NS"
```

### Option C: Anthropic

```bash
kubectl create secret generic anthropic-credentials -n "$KONVEYOR_NS" \
  --from-literal=api-key="<your-anthropic-api-key>"

kubectl apply -f config/samples/gateway_anthropic.yaml -n "$KONVEYOR_NS"
```

### Option D: AWS Bedrock

```bash
kubectl create secret generic bedrock-credentials -n "$KONVEYOR_NS" \
  --from-literal=AWS_ACCESS_KEY_ID="<your-access-key-id>" \
  --from-literal=AWS_SECRET_ACCESS_KEY="<your-secret-access-key>" \
  --from-literal=AWS_REGION="us-east-1"

kubectl apply -f config/samples/gateway_aws_bedrock.yaml -n "$KONVEYOR_NS"
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
kubectl create secret generic grok-credentials -n "$KONVEYOR_NS" \
  --from-literal=api-key="<your-xai-api-key>"

kubectl apply -f config/samples/gateway_xai.yaml -n "$KONVEYOR_NS"
```

Verify the Gateway is ready:

```bash
kubectl get gateways.konveyor.io -n "$KONVEYOR_NS"
```

> **Note:** Use the fully-qualified `gateways.konveyor.io` rather than the
> bare `gateways`. On any cluster with the Gateway API CRDs installed
> (OpenShift 4.19+ does this by default), `gateways` resolves to
> `gateways.gateway.networking.k8s.io` instead, so `kubectl get gateways`
> would report no resources right after you applied your Gateway.

The `Verified` column shows whether the controller confirmed
connectivity to the endpoint.

## 5. Create an Agent

An Agent is a template that declares what is available for execution:
a container image, gateways, skills, a prompt, and typed parameters.
Creating an Agent does not execute anything.

> **Note:** The example `agent_example.yaml` and `agentrun_example.yaml`
> reference the Vertex AI Gateway (`gcp-vertex-ai`) from Option A. If
> you created a different Gateway (Options B–D), update the
> `spec.gateways[].ref` in the Agent and the `spec.gateway` in the
> AgentRun to match your Gateway's name before applying them.

Verify the default SkillCards were deployed (from step 3):

```bash
kubectl get skillcards -n "$KONVEYOR_NS"
```

Apply the example Agent:

```bash
kubectl apply -f config/samples/agent_example.yaml -n "$KONVEYOR_NS"
```

Check that the Agent is ready (referenced Gateways and SkillCards
must exist and be healthy):

```bash
kubectl get agents -n "$KONVEYOR_NS"
```

## 6. Create an AgentRun

An AgentRun triggers execution of an Agent. It references an Agent,
selects a Gateway, carries task-specific instructions, and sets the
environment the entry point needs. The controller validates the
configuration, creates an Agent Sandbox, and tracks the run to
completion.

> **Hub application.** The `agent-java` image resolves the repository to
> migrate and its git credentials from the Hub installed in step 2, keyed
> by `APP_ID`. Register the application you want to migrate in Hub and
> note its `APP_ID`. The sample AgentRun's `spec.env` points at the
> in-cluster Hub service with `APP_ID: "1"`; edit `HUB_BASE_URL`,
> `APP_ID`, and `TARGET_BRANCH` to match your Hub and application.

Apply the example AgentRun:

```bash
kubectl apply -f config/samples/agentrun_example.yaml -n "$KONVEYOR_NS"
```

Watch the run:

```bash
kubectl get agentruns -n "$KONVEYOR_NS" -w
```

Once the phase moves to `Running`, the Sandbox pod is live. View
agent logs:

```bash
# Get the sandbox pod name from the AgentRun status
SANDBOX=$(kubectl get agentrun migration-run-001 -n "$KONVEYOR_NS" -o jsonpath='{.status.sandboxName}')
kubectl logs -f $SANDBOX -n "$KONVEYOR_NS"
```

The AgentRun spec is **immutable** — to change values, delete the
AgentRun and create a new one.

## 7. Workflows (optional)

For multi-stage work (e.g. plan, execute, verify), use
AgentWorkflow and AgentWorkflowRun. See
`hack/harness-test/workflow-resources.yaml` for a complete example
that migrates a Java EE application to Quarkus using three stages.

## Sample and default manifests

**Samples** (`config/samples/`) are illustrative CRs you copy and edit —
nothing here is auto-installed:

| File | Kind | Description |
|------|------|-------------|
| `gateway_vertex_ai.yaml` | Gateway | GCP Vertex AI with Claude |
| `gateway_openai.yaml` | Gateway | OpenAI GPT-4o |
| `gateway_anthropic.yaml` | Gateway | Anthropic direct API |
| `gateway_aws_bedrock.yaml` | Gateway | AWS Bedrock |
| `gateway_xai.yaml` | Gateway | xAI (Grok) |
| `agent_example.yaml` | Agent | Standalone Java migration agent |
| `agentrun_example.yaml` | AgentRun | Triggers the example agent |

**Defaults** (`config/defaults/`) are the curated content the operator
installs on enable — the skill catalog plus the Agents and AgentWorkflow
the UI runs. Apply them with `kubectl apply -k config/defaults/`:

| File | Kind | Description |
|------|------|-------------|
| `skillcard_*.yaml` | SkillCard | Migration-stage skills (plan, execute, verify, javaee-to-quarkus, house-rules) |
| `skillcollection_java_migration.yaml` | SkillCollection | Grouped migration skills |
| `agent_migration_*.yaml` | Agent | Plan, execute, and verify stage agents (no gateway — named at run time) |
| `agentworkflow_javaee_to_quarkus.yaml` | AgentWorkflow | Three-stage Java EE → Quarkus workflow |

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

`kubectl delete --all` acts on one namespace — keep the `-n` so you
don't remove resources you meant to keep:

```bash
# Delete runs, agents, and gateways in the Hub namespace
kubectl delete agentruns --all -n "$KONVEYOR_NS"
kubectl delete agents --all -n "$KONVEYOR_NS"
kubectl delete gateways.konveyor.io --all -n "$KONVEYOR_NS"
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

**UI agentic pages (Agent runs, Agents) show an error or never load; Hub returns 500**

The UI calls Hub's `/hub/agentic/*` endpoints and Hub reads the
`konveyor.io` resources from its own namespace. A Hub log line like

```
agents.konveyor.io is forbidden: User "system:serviceaccount:konveyor-tackle:tackle-hub"
cannot list resource "agents" in API group "konveyor.io" in the namespace "konveyor-tackle"
```

means one or both of:

- Hub's ServiceAccount has no Role on `konveyor.io` resources in its
  namespace. Apply `config/hub-rbac/` there (step 2). If the Role exists
  but in a *different* namespace, it does nothing — the namespace in the
  error is the one that matters.
- The Agents, Gateways, and credential Secrets were created in another
  namespace (often whatever the kubectl context defaulted to). Hub cannot
  see them from `konveyor-tackle`. Recreate them in the Hub namespace with
  the `-n "$KONVEYOR_NS"` commands above; the cluster-scoped controller
  reconciles them wherever they are.

Confirm with `kubectl auth can-i list agents.konveyor.io
--as=system:serviceaccount:konveyor-tackle:tackle-hub -n konveyor-tackle`
and `kubectl get agents.konveyor.io -n konveyor-tackle`.

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
