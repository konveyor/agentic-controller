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
echo "=== Building and loading skill images ==="
make skill-build
# Build a FROM-scratch OCI image for each skill and load into Kind.
# skillctl builds to its own OCI store; we need container images for Kind.
# Use the skill content directly in a simple container image.
for dir in skills/examples/*/; do
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

# Load skill images into Kind.
if [ "${CONTAINER_TOOL}" = "podman" ]; then
    SKILL_TMP=$(mktemp -d)
    for dir in skills/examples/*/; do
        name=$(basename "${dir}")
        skill_img="quay.io/konveyor/skills:${name}"
        echo "Saving ${skill_img} to tarball..."
        ${CONTAINER_TOOL} save "${skill_img}" -o "${SKILL_TMP}/${name}.tar"
        kind load image-archive "${SKILL_TMP}/${name}.tar" --name "${KIND_CLUSTER}"
    done
    rm -rf "${SKILL_TMP}"
else
    for dir in skills/examples/*/; do
        name=$(basename "${dir}")
        kind load docker-image "quay.io/konveyor/skills:${name}" --name "${KIND_CLUSTER}"
    done
fi

echo ""
echo "=== Deploying controller ==="
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
make install
make kustomize
bin/kustomize build "${SCRIPT_DIR}/e2e" | kubectl apply -f -

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
