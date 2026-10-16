#!/usr/bin/env bash
# Start a Kind cluster and install Agent Sandbox for e2e testing.
#
# Environment variables:
#   KIND_CLUSTER        Cluster name (default: agentic-controller-e2e)
#   KIND_IMAGE          Node image (default: Kind's default for the installed version)
#   AGENT_SANDBOX_TAG   Agent Sandbox version (default: the version pinned in go.mod)
#   CONTAINER_TOOL      Container runtime: docker or podman (default: auto-detect)

set -euo pipefail

KIND_CLUSTER="${KIND_CLUSTER:-agentic-controller-e2e}"
# Default the Agent Sandbox git tag from the version pinned in go.mod so the
# deployed CRDs match the API the controller compiles against (mirrors the
# go.mod-derived CRD path in internal/controller/suite_test.go). Override with
# the env var to test against a different release.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AGENT_SANDBOX_TAG="${AGENT_SANDBOX_TAG:-$(go -C "${REPO_ROOT}" list -m -f '{{.Version}}' sigs.k8s.io/agent-sandbox)}"

# Auto-detect container runtime.
if [ -z "${CONTAINER_TOOL:-}" ]; then
    if command -v podman &>/dev/null; then
        CONTAINER_TOOL=podman
    elif command -v docker &>/dev/null; then
        CONTAINER_TOOL=docker
    else
        echo "ERROR: neither podman nor docker found" >&2
        exit 1
    fi
fi

echo "=== Configuration ==="
echo "Cluster:        ${KIND_CLUSTER}"
echo "Container tool: ${CONTAINER_TOOL}"
echo "Sandbox tag:    ${AGENT_SANDBOX_TAG}"
echo ""

# Set Kind provider for Podman.
if [ "${CONTAINER_TOOL}" = "podman" ]; then
    export KIND_EXPERIMENTAL_PROVIDER=podman
fi

# Check if cluster already exists.
if kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER}"; then
    echo "Kind cluster '${KIND_CLUSTER}' already exists. Skipping creation."
else
    echo "Creating Kind cluster '${KIND_CLUSTER}'..."
    kind_args=(create cluster --name "${KIND_CLUSTER}" --wait 5m)
    if [ -n "${KIND_IMAGE:-}" ]; then
        kind_args+=(--image "${KIND_IMAGE}")
    fi
    kind "${kind_args[@]}"
fi

echo ""
echo "=== Installing Agent Sandbox ${AGENT_SANDBOX_TAG} ==="

# Clone Agent Sandbox and install via Helm.
SANDBOX_DIR=$(mktemp -d)
trap "rm -rf ${SANDBOX_DIR}" EXIT

git clone --depth 1 --branch "${AGENT_SANDBOX_TAG}" \
    https://github.com/kubernetes-sigs/agent-sandbox.git "${SANDBOX_DIR}" 2>&1

if helm status agent-sandbox --namespace agent-sandbox-system &>/dev/null; then
    # Existing release: upgrade. Helm never upgrades the CRDs in a chart's
    # crds/ directory, so apply them first. This is load-bearing for the
    # v0.5.x -> v1.0.0 jump — the v1 chart drops the conversion-webhook
    # Service, so the v1beta1-only CRDs (which remove the conversion config
    # that references it) must land before the upgrade or the old CRDs point
    # at a Service that no longer exists. See the upstream API migration
    # guide: https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.0/docs/api-migration-guide.md
    # (Assumes storedVersions is already v1beta1, true for any v0.5.x-created
    # cluster; a pre-v0.5.0 cluster needs the storage migration in that guide
    # first — recreate the Kind cluster instead.)
    echo "Agent Sandbox already installed; applying CRDs then upgrading to ${AGENT_SANDBOX_TAG}..."
    kubectl apply -f "${SANDBOX_DIR}/helm/crds/"
    # namespace.create=false for the same reason as the install path below:
    # the namespace was created out-of-band by --create-namespace, so it
    # carries no Helm ownership metadata and the chart's own Namespace
    # resource cannot adopt it ("invalid ownership metadata").
    helm upgrade agent-sandbox "${SANDBOX_DIR}/helm/" \
        --namespace agent-sandbox-system \
        --set namespace.create=false \
        --set image.tag="${AGENT_SANDBOX_TAG}"
else
    # Let Helm create the namespace (--create-namespace) and disable the
    # chart's own Namespace resource (namespace.create=false). The chart
    # renders a Namespace by default, so keeping both would make Helm create
    # the namespace out-of-band and then collide with the chart's own
    # Namespace object ("namespaces agent-sandbox-system already exists").
    helm install agent-sandbox "${SANDBOX_DIR}/helm/" \
        --namespace agent-sandbox-system \
        --create-namespace \
        --set namespace.create=false \
        --set image.tag="${AGENT_SANDBOX_TAG}"
fi

echo ""
echo "=== Waiting for Agent Sandbox controller ==="
kubectl wait deployment/agent-sandbox-controller \
    --namespace agent-sandbox-system \
    --for=condition=Available \
    --timeout=120s

echo ""
echo "=== Installing LLEmulator (mock LLM server) ==="
LLEMULATOR_DIR=$(mktemp -d)
# Pinned rather than tracking main, so an unrelated emulator change cannot
# break this repo's CI. Bump it deliberately.
LLEMULATOR_REF="${LLEMULATOR_REF:-8590822766430d999a48020570e312f415d4c7da}"
git init -q "${LLEMULATOR_DIR}"
git -C "${LLEMULATOR_DIR}" remote add origin https://github.com/fabianvf/llemulator.git
git -C "${LLEMULATOR_DIR}" fetch -q --depth 1 origin "${LLEMULATOR_REF}"
git -C "${LLEMULATOR_DIR}" checkout -q FETCH_HEAD

# Build the llemulator image and load into Kind.
LLEM_IMG="docker.io/library/openai-emulator:e2e"
${CONTAINER_TOOL} build -t "${LLEM_IMG}" "${LLEMULATOR_DIR}"
if [ "${CONTAINER_TOOL}" = "podman" ]; then
    LLEM_TMP=$(mktemp -d)
    ${CONTAINER_TOOL} save "${LLEM_IMG}" -o "${LLEM_TMP}/llemulator.tar"
    kind load image-archive "${LLEM_TMP}/llemulator.tar" --name "${KIND_CLUSTER}"
    rm -rf "${LLEM_TMP}"
else
    kind load docker-image "${LLEM_IMG}" --name "${KIND_CLUSTER}"
fi

# Deploy llemulator using our own manifest (imagePullPolicy: Never).
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
kubectl apply -f "${SCRIPT_DIR}/e2e/llemulator.yaml"
rm -rf "${LLEMULATOR_DIR}"

echo "Waiting for llemulator..."
kubectl wait deployment/openai-emulator \
    --for=condition=Available \
    --timeout=120s

echo ""
echo "=== Cluster ready ==="
kubectl get nodes
echo ""
kubectl get pods -A
echo ""
echo "Kind cluster '${KIND_CLUSTER}' is ready with Agent Sandbox ${AGENT_SANDBOX_TAG}."
echo "To use: export KUBECONFIG=\$(kind get kubeconfig --name ${KIND_CLUSTER})"
