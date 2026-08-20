#!/usr/bin/env bash
# Build the agent image and load it into the Kind cluster.
#
# Separate from setup-e2e.sh because only the rule e2e needs it and it is
# roughly a gigabyte, so the main e2e job should not pay for it.
#
# The container tool is detected the same way the other scripts detect it, and
# that choice decides both how the image is loaded and which Kind provider is
# addressed. Building with one tool and loading with the other fails with
# "no nodes found for cluster", because Kind looks for the cluster in the
# store belonging to whichever provider is configured.
#
# Environment:
#   CONTAINER_TOOL    docker or podman (default: auto-detect)
#   AGENT_BASE_IMG    image to build (default agent-base:e2e)
#   KIND_CLUSTER      cluster to load into (default agentic-controller-e2e)

set -euo pipefail

if [ -z "${CONTAINER_TOOL:-}" ]; then
    if command -v podman >/dev/null 2>&1; then
        CONTAINER_TOOL=podman
    else
        CONTAINER_TOOL=docker
    fi
fi

# Not :latest. Kubernetes defaults imagePullPolicy to Always for that tag, so
# the kubelet would ignore the image loaded into the node.
AGENT_BASE_IMG="${AGENT_BASE_IMG:-quay.io/konveyor/agent-base:e2e}"
KIND_CLUSTER="${KIND_CLUSTER:-agentic-controller-e2e}"

if [ "${CONTAINER_TOOL}" = "podman" ]; then
    export KIND_EXPERIMENTAL_PROVIDER=podman
fi

echo "=== Building ${AGENT_BASE_IMG} with ${CONTAINER_TOOL} ==="
make agent-base-build AGENT_BASE_IMG="${AGENT_BASE_IMG}" CONTAINER_TOOL="${CONTAINER_TOOL}"

echo "=== Loading into Kind cluster '${KIND_CLUSTER}' ==="
if [ "${CONTAINER_TOOL}" = "podman" ]; then
    TMP=$(mktemp -d)
    trap 'rm -rf "${TMP}"' EXIT
    "${CONTAINER_TOOL}" save "${AGENT_BASE_IMG}" -o "${TMP}/agent-base.tar"
    kind load image-archive "${TMP}/agent-base.tar" --name "${KIND_CLUSTER}"
else
    kind load docker-image "${AGENT_BASE_IMG}" --name "${KIND_CLUSTER}"
fi
