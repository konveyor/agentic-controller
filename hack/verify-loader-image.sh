#!/usr/bin/env bash
# The loader and the enumeration Job run the controller's own image, passed as
# SKILL_LOADER_IMAGE. Kustomize `images:` transformers only rewrite image
# fields, so an overlay that retags the controller leaves the env pointing at
# whatever the layer below set, and the init container then fails to pull with
# ErrImagePull and no explanation. Every overlay repeats the replacement; this
# checks they all took.
set -euo pipefail

KUSTOMIZE="${KUSTOMIZE:-bin/kustomize}"
OVERLAYS="${OVERLAYS:-config/default hack/e2e}"

status=0
for overlay in ${OVERLAYS}; do
    # The manager Deployment has one container, so the first image: under the
    # controller-manager Deployment is it. kustomize sorts image: before
    # name: manager, so keying off the container name would read the wrong line.
    rendered="$(${KUSTOMIZE} build "${overlay}")"
    image="$(printf '%s' "${rendered}" \
        | awk '/kind: Deployment/{d=1} d && /^ *image: /{print $2; exit}')"
    env_image="$(printf '%s' "${rendered}" \
        | awk '/name: SKILL_LOADER_IMAGE$/{getline; print $2; exit}')"

    if [ -z "${image}" ] || [ -z "${env_image}" ]; then
        echo "FAIL ${overlay}: could not read manager image (${image:-empty}) or SKILL_LOADER_IMAGE (${env_image:-empty})" >&2
        status=1
        continue
    fi
    if [ "${image}" != "${env_image}" ]; then
        echo "FAIL ${overlay}: SKILL_LOADER_IMAGE is ${env_image} but the manager runs ${image}." >&2
        echo "     Add the replacements block from config/default/kustomization.yaml to this overlay." >&2
        status=1
        continue
    fi
    echo "ok   ${overlay}: ${image}"
done
exit "${status}"
