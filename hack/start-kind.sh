#!/usr/bin/env bash
# Start a Kind cluster and install Agent Sandbox for e2e testing.
#
# Environment variables:
#   KIND_CLUSTER        Cluster name (default: agentic-controller-e2e)
#   KIND_IMAGE          Node image (default: Kind's default for the installed version)
#   AGENT_SANDBOX_TAG   Agent Sandbox version (default: v0.5.0)
#   CONTAINER_TOOL      Container runtime: docker or podman (default: auto-detect)

set -euo pipefail

KIND_CLUSTER="${KIND_CLUSTER:-agentic-controller-e2e}"
AGENT_SANDBOX_TAG="${AGENT_SANDBOX_TAG:-v0.5.0}"

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

helm install agent-sandbox "${SANDBOX_DIR}/helm/" \
    --namespace agent-sandbox-system \
    --create-namespace \
    --set image.tag="${AGENT_SANDBOX_TAG}" \
    --kubeconfig "$(kind get kubeconfig-path --name "${KIND_CLUSTER}" 2>/dev/null || echo "${HOME}/.kube/config")" \
    2>&1 || {
        # If already installed, upgrade instead.
        echo "Helm install failed, attempting upgrade..."
        helm upgrade agent-sandbox "${SANDBOX_DIR}/helm/" \
            --namespace agent-sandbox-system \
            --set image.tag="${AGENT_SANDBOX_TAG}" \
            2>&1
    }

echo ""
echo "=== Waiting for Agent Sandbox controller ==="
kubectl wait deployment/agent-sandbox-controller \
    --namespace agent-sandbox-system \
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
