#!/usr/bin/env bash
# Shared cluster helpers for the e2e setup scripts. Source, do not execute.
#
# The e2e runners (hack/run-e2e*.sh) are already cluster-agnostic: they only
# talk to the Kubernetes API. The setup path was tied to Kind by a single
# primitive — getting a locally built image onto the node, which no in-cluster
# registry does for us. This file is that primitive, behind a hook, so the same
# setup works against an existing cluster.
#
#   CLUSTER_TOOL      kind (default) | minikube | none
#   CLUSTER_NAME      cluster name, or minikube profile (default: $KIND_CLUSTER)
#   LOAD_IMAGE_HOOK   script taking image refs; overrides CLUSTER_TOOL entirely
#
# `none` means a cluster this script did not create and cannot push images into
# by itself: it skips creation and requires LOAD_IMAGE_HOOK.
#
# Kind with docker is the CI path and emits exactly the commands it did before.

cluster_tool() { echo "${CLUSTER_TOOL:-kind}"; }
cluster_name() { echo "${CLUSTER_NAME:-${KIND_CLUSTER:-agentic-controller-e2e}}"; }

# cluster_load_images <image>...
# Loads locally built images onto the cluster's nodes. Images passed in one
# call are batched the way that tool prefers — for podman, a single archive,
# which is why the call sites group related images rather than looping.
cluster_load_images() {
    [ "$#" -gt 0 ] || return 0
    local name tool
    name="$(cluster_name)"
    tool="$(cluster_tool)"

    if [ -n "${LOAD_IMAGE_HOOK:-}" ]; then
        "${LOAD_IMAGE_HOOK}" "$@"
        return
    fi

    case "${tool}" in
    kind)
        if [ "${CONTAINER_TOOL:-docker}" = "podman" ]; then
            # Podman keeps images in its own VM, which Kind cannot read.
            local tmp
            tmp=$(mktemp -d)
            echo "Saving $* to tarball..."
            ${CONTAINER_TOOL} save "$@" -o "${tmp}/images.tar"
            kind load image-archive "${tmp}/images.tar" --name "${name}"
            rm -rf "${tmp}"
        else
            local img
            for img in "$@"; do
                kind load docker-image "${img}" --name "${name}"
            done
        fi
        ;;
    minikube)
        # minikube image load reads the host daemon for docker and accepts an
        # archive otherwise, so the podman split is the same shape as Kind's.
        if [ "${CONTAINER_TOOL:-docker}" = "podman" ]; then
            local tmp img
            tmp=$(mktemp -d)
            for img in "$@"; do
                ${CONTAINER_TOOL} save "${img}" -o "${tmp}/image.tar"
                minikube image load "${tmp}/image.tar" -p "${name}"
            done
            rm -rf "${tmp}"
        else
            local img
            for img in "$@"; do
                minikube image load "${img}" -p "${name}"
            done
        fi
        ;;
    none)
        echo "ERROR: CLUSTER_TOOL=none needs LOAD_IMAGE_HOOK to load: $*" >&2
        return 1
        ;;
    *)
        echo "ERROR: unknown CLUSTER_TOOL '${tool}' (kind|minikube|none)" >&2
        return 1
        ;;
    esac
}

# cluster_exists — true if the named cluster is already there. Only Kind and
# minikube can be asked; for anything else the current context is the answer.
cluster_exists() {
    local name tool
    name="$(cluster_name)"
    tool="$(cluster_tool)"
    case "${tool}" in
    kind)     kind get clusters 2>/dev/null | grep -qx "${name}" ;;
    minikube) minikube status -p "${name}" &>/dev/null ;;
    *)        kubectl cluster-info &>/dev/null ;;
    esac
}
