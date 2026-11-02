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
#   LOAD_IMAGE_HOOK   script taking image refs; implies CLUSTER_TOOL=none
#
# `none` means a cluster this script did not create and cannot push images into
# by itself: it skips creation and requires LOAD_IMAGE_HOOK.
#
# Setting LOAD_IMAGE_HOOK therefore also tells the rest of the stack to keep its
# hands off the cluster — no Kind creation, and the current kubectl context is
# what "does it exist" means. A hook plus a cluster this script creates is not a
# combination we have a use for, and defaulting to kind there would quietly
# create a local cluster under someone pushing images to a remote one.
#
# Kind with docker is the CI path and emits exactly the commands it did before.

cluster_tool() {
    if [ -n "${LOAD_IMAGE_HOOK:-}" ]; then
        echo none
        return
    fi
    echo "${CLUSTER_TOOL:-kind}"
}
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
            # The archive is gigabytes, so it goes whether the load works or
            # not; errexit would skip a plain rm on the line after.
            local tmp rc=0
            tmp=$(mktemp -d)
            echo "Saving $* to tarball..."
            ${CONTAINER_TOOL} save "$@" -o "${tmp}/images.tar" &&
                kind load image-archive "${tmp}/images.tar" --name "${name}" || rc=$?
            rm -rf "${tmp}"
            return "${rc}"
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
            local tmp img rc=0
            tmp=$(mktemp -d)
            for img in "$@"; do
                ${CONTAINER_TOOL} save "${img}" -o "${tmp}/image.tar" &&
                    minikube image load "${tmp}/image.tar" -p "${name}" || { rc=$?; break; }
            done
            rm -rf "${tmp}"
            return "${rc}"
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
#
# Asking the tool about a named cluster says nothing about where kubectl points
# — see cluster_require_context, which every mutating script calls first.
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

# cluster_context — the kubectl context the named cluster is reached through.
# Empty when the current context is the only answer there is: a hook, or
# CLUSTER_TOOL=none, where we have no way to name the cluster ourselves.
cluster_context() {
    local name tool
    name="$(cluster_name)"
    tool="$(cluster_tool)"
    case "${tool}" in
    kind)     echo "kind-${name}" ;;
    minikube) echo "${name}" ;;
    *)        echo "" ;;
    esac
}

# cluster_require_context — fail unless kubectl points at the cluster the image
# loads target. The two halves of setup address the cluster differently: loads
# name it (`--name`, `-p`), while kubectl and Helm follow the current context.
# `kind create` and `minikube start` set that context, so the usual path agrees
# — but against a cluster that already exists, nothing had been checking, and
# the failure is silent: CRDs applied and Agent Sandbox upgraded in whatever
# cluster happened to be selected, while the images went somewhere else.
cluster_require_context() {
    local want current
    want="$(cluster_context)"
    [ -n "${want}" ] || return 0
    current="$(kubectl config current-context 2>/dev/null || true)"
    if [ "${current}" != "${want}" ]; then
        echo "ERROR: images load into '$(cluster_name)' (context '${want}'), but kubectl is on '${current:-<none>}'." >&2
        echo "       Everything else here would be applied to the wrong cluster. Select it first:" >&2
        echo "           kubectl config use-context ${want}" >&2
        return 1
    fi
}
