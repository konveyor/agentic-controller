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
#   E2E_AGENT_IMAGE   image to build (default agent-base:e2e)
#   GOOSE_IMAGE       prebuilt goose image, skips the ~35 minute compile
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
# the kubelet ignores the image loaded into the node and pulls a tag that only
# exists locally.
#
# Deliberately not named AGENT_BASE_IMG. The Makefile exports that one, so it
# is always set in this script's environment and a ${AGENT_BASE_IMG:-...}
# default here would never be reached -- which is how CI came to build :latest
# and fail on ErrImagePull while the same commit built :e2e everywhere the
# variable was not yet exported.
E2E_AGENT_IMAGE="${E2E_AGENT_IMAGE:-quay.io/konveyor/agent-base:e2e}"
KIND_CLUSTER="${KIND_CLUSTER:-agentic-controller-e2e}"

if [ "${CONTAINER_TOOL}" = "podman" ]; then
    export KIND_EXPERIMENTAL_PROVIDER=podman
fi

echo "=== Building ${E2E_AGENT_IMAGE} with ${CONTAINER_TOOL} ==="
# On the command line, so it beats the Makefile's exported default. GOOSE_IMAGE
# is passed the same way rather than left to the environment: it reaches the
# Makefile today only because that variable is `?=`, and the day someone gives
# it a real default the cache stops being used with nothing failing to say so.
# Empty is fine, the Makefile drops the build arg entirely.
make agent-base-build \
    AGENT_BASE_IMG="${E2E_AGENT_IMAGE}" \
    CONTAINER_TOOL="${CONTAINER_TOOL}" \
    GOOSE_IMAGE="${GOOSE_IMAGE:-}"

echo "=== Loading into Kind cluster '${KIND_CLUSTER}' ==="
if [ "${CONTAINER_TOOL}" = "podman" ]; then
    TMP=$(mktemp -d)
    trap 'rm -rf "${TMP}"' EXIT
    "${CONTAINER_TOOL}" save "${E2E_AGENT_IMAGE}" -o "${TMP}/agent-base.tar"
    kind load image-archive "${TMP}/agent-base.tar" --name "${KIND_CLUSTER}"
else
    kind load docker-image "${E2E_AGENT_IMAGE}" --name "${KIND_CLUSTER}"
fi
