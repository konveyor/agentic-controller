#!/usr/bin/env bash
# Build images, load into Kind, and deploy the controller for e2e testing.
#
# Prerequisites: Kind cluster running (see hack/start-kind.sh)
#
# Environment variables:
#   KIND_CLUSTER            Cluster name (default: agentic-controller-e2e)
#   CONTAINER_TOOL          docker or podman (default: auto-detect)
#   IMG                     Controller image (default: quay.io/konveyor/agentic-controller:e2e)
#   CONTROLLER_AGENT_IMG    Agent image (default: quay.io/konveyor/agentic-controller-agent:e2e)

set -euo pipefail

KIND_CLUSTER="${KIND_CLUSTER:-agentic-controller-e2e}"
IMG="${IMG:-quay.io/konveyor/agentic-controller:e2e}"
CONTROLLER_AGENT_IMG="${CONTROLLER_AGENT_IMG:-quay.io/konveyor/agentic-controller-agent:e2e}"

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

if [ "${CONTAINER_TOOL}" = "podman" ]; then
    export KIND_EXPERIMENTAL_PROVIDER=podman
fi

echo "=== Building images ==="

echo "Building controller image: ${IMG}"
make docker-build IMG="${IMG}" CONTAINER_TOOL="${CONTAINER_TOOL}"

echo "Building controller-agent image: ${CONTROLLER_AGENT_IMG}"
make controller-agent-build CONTROLLER_AGENT_IMG="${CONTROLLER_AGENT_IMG}" CONTAINER_TOOL="${CONTAINER_TOOL}"
# Also tag as :latest for the LLMProvider verification Job default.
${CONTAINER_TOOL} tag "${CONTROLLER_AGENT_IMG}" "quay.io/konveyor/agentic-controller-agent:latest"

echo ""
echo "=== Loading images into Kind cluster '${KIND_CLUSTER}' ==="
if [ "${CONTAINER_TOOL}" = "podman" ]; then
    # Podman stores images in its VM; Kind can't access them directly.
    # Save to tarball and load via Kind.
    TMPDIR=$(mktemp -d)
    trap "rm -rf ${TMPDIR}" EXIT

    echo "Saving ${IMG} to tarball..."
    ${CONTAINER_TOOL} save "${IMG}" -o "${TMPDIR}/controller.tar"
    kind load image-archive "${TMPDIR}/controller.tar" --name "${KIND_CLUSTER}"

    echo "Saving ${CONTROLLER_AGENT_IMG} (+ :latest) to tarball..."
    ${CONTAINER_TOOL} save "${CONTROLLER_AGENT_IMG}" "quay.io/konveyor/agentic-controller-agent:latest" -o "${TMPDIR}/agent.tar"
    kind load image-archive "${TMPDIR}/agent.tar" --name "${KIND_CLUSTER}"
else
    kind load docker-image "${IMG}" --name "${KIND_CLUSTER}"
    kind load docker-image "${CONTROLLER_AGENT_IMG}" --name "${KIND_CLUSTER}"
    ${CONTAINER_TOOL} tag "${CONTROLLER_AGENT_IMG}" "quay.io/konveyor/agentic-controller-agent:latest"
    kind load docker-image "quay.io/konveyor/agentic-controller-agent:latest" --name "${KIND_CLUSTER}"
fi

echo ""
echo "=== Deploying controller ==="
make install
make deploy IMG="${IMG}"

echo ""
echo "=== Waiting for controller ==="
kubectl wait deployment/agentic-controller-controller-manager \
    --namespace agentic-controller-system \
    --for=condition=Available \
    --timeout=120s

echo ""
echo "=== Deploying sample resources ==="
kubectl apply -k config/samples/

echo ""
echo "=== Controller deployed ==="
kubectl get pods -n agentic-controller-system
echo ""
kubectl get skillcards
echo ""
kubectl get skillcollections
