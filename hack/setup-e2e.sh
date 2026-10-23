#!/usr/bin/env bash
# Build images, load them into the cluster, and deploy the controller for e2e.
#
# Prerequisites: a cluster running (see hack/start-kind.sh)
#
# Environment variables:
#   KIND_CLUSTER            Cluster name (default: agentic-controller-e2e)
#   CLUSTER_TOOL            kind (default), minikube, or none — how images
#                           reach the node; see hack/lib/cluster.sh
#   CLUSTER_NAME            Cluster/profile name (default: $KIND_CLUSTER)
#   LOAD_IMAGE_HOOK         Script taking image refs, for any other cluster
#   CONTAINER_TOOL          docker or podman (default: auto-detect)
#   IMG                     Controller image (default: quay.io/konveyor/agentic-controller:e2e)
#   CONTROLLER_AGENT_IMG    Agent image (default: quay.io/konveyor/agentic-controller-agent:e2e)
#   E2E_AGENT_STUB_IMG      Workflow-test image (default: quay.io/konveyor/agentic-controller-agent:e2e-stub)

set -euo pipefail

# shellcheck source=hack/lib/cluster.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/cluster.sh"

KIND_CLUSTER="${KIND_CLUSTER:-agentic-controller-e2e}"
IMG="${IMG:-quay.io/konveyor/agentic-controller:e2e}"
CONTROLLER_AGENT_IMG="${CONTROLLER_AGENT_IMG:-quay.io/konveyor/agentic-controller-agent:e2e}"
E2E_AGENT_STUB_IMG="${E2E_AGENT_STUB_IMG:-quay.io/konveyor/agentic-controller-agent:e2e-stub}"

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

echo "=== Source: $(git rev-parse --short HEAD) ($(git rev-parse --abbrev-ref HEAD)) ==="
echo ""
echo "=== Building images ==="

echo "Building controller image: ${IMG}"
make docker-build IMG="${IMG}" CONTAINER_TOOL="${CONTAINER_TOOL}"

echo "Building controller-agent image: ${CONTROLLER_AGENT_IMG}"
make controller-agent-build CONTROLLER_AGENT_IMG="${CONTROLLER_AGENT_IMG}" CONTAINER_TOOL="${CONTAINER_TOOL}"
# Also tag as :latest for the Gateway verification Job default.
${CONTAINER_TOOL} tag "${CONTROLLER_AGENT_IMG}" "quay.io/konveyor/agentic-controller-agent:latest"

echo "Building E2E agent stub image: ${E2E_AGENT_STUB_IMG}"
${CONTAINER_TOOL} build \
    --build-arg BASE_IMAGE="${CONTROLLER_AGENT_IMG}" \
    -t "${E2E_AGENT_STUB_IMG}" \
    -f test/e2e/agent-stub/Containerfile \
    test/e2e/agent-stub/

echo ""
echo "=== Loading images into cluster '$(cluster_name)' ==="
# The :latest alias is tagged before loading either way, so both tool paths
# put the same set of refs on the node.
${CONTAINER_TOOL} tag "${CONTROLLER_AGENT_IMG}" "quay.io/konveyor/agentic-controller-agent:latest"
cluster_load_images "${IMG}"
cluster_load_images "${CONTROLLER_AGENT_IMG}" \
    "quay.io/konveyor/agentic-controller-agent:latest" \
    "${E2E_AGENT_STUB_IMG}"

echo ""
echo "=== Building and loading skill images ==="
# Not :latest. Kubernetes defaults imagePullPolicy to Always for that tag, so
# the kubelet ignores the image loaded into the node and tries to pull a bundle
# that only exists locally.
SKILL_BUNDLE_IMG="quay.io/konveyor/skills:e2e"
# CONTAINER_TOOL has to be passed: the Makefile defaults it to docker, while
# this script auto-detects and picks podman where both exist, and the bundle
# would then be built into one tool's store and saved from the other's.
make skill-build SKILL_IMAGE="${SKILL_BUNDLE_IMG}" CONTAINER_TOOL="${CONTAINER_TOOL}"
# Build a FROM-scratch OCI image for each skill and load into Kind.
# skillctl builds to its own OCI store; we need container images for Kind.
# Use the skill content directly in a simple container image.
for dir in catalog/examples/*/; do
    name=$(basename "${dir}")
    skill_img="quay.io/konveyor/skills:${name}"
    echo "Building skill image: ${skill_img}"
    # Create a temporary Containerfile that copies the skill content.
    tmp_ctx=$(mktemp -d)
    cp -r "${dir}"* "${tmp_ctx}/"
    cat > "${tmp_ctx}/Containerfile" <<'SKILLEOF'
FROM scratch
COPY . /
SKILLEOF
    ${CONTAINER_TOOL} build -t "${skill_img}" -f "${tmp_ctx}/Containerfile" "${tmp_ctx}"
    rm -rf "${tmp_ctx}"
done

# Load the skill images. One call per image, matching the per-image archives
# the podman path used before.
cluster_load_images "${SKILL_BUNDLE_IMG}"
for dir in catalog/examples/*/; do
    name=$(basename "${dir}")
    cluster_load_images "quay.io/konveyor/skills:${name}"
done

echo ""
echo "=== Deploying controller ==="
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
make install
make kustomize
bin/kustomize build "${SCRIPT_DIR}/e2e" | kubectl apply -f -
# On a reused cluster the Deployment is unchanged (same image tag), so the
# running manager would keep the previous binary; restart so the freshly
# loaded image is what gets tested.
kubectl rollout restart deployment/agentic-controller-controller-manager \
    --namespace agentic-controller-system

echo ""
echo "=== Waiting for controller ==="
kubectl rollout status deployment/agentic-controller-controller-manager \
    --namespace agentic-controller-system \
    --timeout=120s

echo ""
echo "=== Deploying default content ==="
# The SkillCards/SkillCollections (and the migration Agents + workflow) live in
# config/defaults/, not config/samples/ (which now holds only Gateways that need
# real credentials). Apply the defaults so the assertions below see the catalog.
kubectl apply -k config/defaults/

echo ""
echo "=== Controller deployed ==="
kubectl get pods -n agentic-controller-system
echo ""
kubectl get skillcards
echo ""
kubectl get skillcollections
