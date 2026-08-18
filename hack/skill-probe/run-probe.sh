#!/usr/bin/env bash
# Re-answer the skill packaging and delivery questions on any cluster.
#
# Builds the skill bundle image and a minimal image carrying the harness
# binary, loads both into the node's image store, and runs four pods that
# reproduce the shape the AgentRun controller generates:
#
#   mixed-sources   all three source kinds at once — must succeed, and every
#                   skill must land at /opt/skills/<name>/SKILL.md
#   bad-skill       a skill with no frontmatter — must fail at init, and the
#                   agent container must never start
#   name-collision  two sources declaring the same frontmatter name — must
#                   fail at init naming both origins
#   home-boundary   whether an init container's $HOME write reaches the agent
#                   container — it must not, which is why the loader cannot
#                   own the ~/.agents/skills link
#
# The cluster needs the ImageVolume feature gate (beta in 1.34). Measured
# against minikube with CRI-O 1.35.0 and Kubernetes v1.34.0:
#
#   minikube start -p agentic-dev --container-runtime=cri-o \
#     --kubernetes-version=v1.34.0 --driver=podman --feature-gates=ImageVolume=true
#
# Environment:
#   KUBE_CONTEXT     kubectl context (default: agentic-dev)
#   CONTAINER_TOOL   podman or docker (default: podman)
#   PLATFORM         image platform (default: linux/arm64)

set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-agentic-dev}"
CONTAINER_TOOL="${CONTAINER_TOOL:-podman}"
PLATFORM="${PLATFORM:-linux/arm64}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

kc() { kubectl --context "${KUBE_CONTEXT}" "$@"; }

echo "==> Building the skill bundle image"
"${CONTAINER_TOOL}" build --platform "${PLATFORM}" -q \
    -t localhost/konveyor-skills:dev \
    -f "${REPO_ROOT}/skills/Containerfile" "${REPO_ROOT}/skills" >/dev/null

echo "==> Building an image carrying the skill loader"
# A stand-in for the controller's image, which is what runs the loader in a
# real pod. This probe only needs the binary.
GOARCH="${PLATFORM##*/}"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH}" \
    go build -o "${WORK}/skill-loader-linux" ./cmd/skill-loader/)

cat >"${WORK}/Containerfile" <<'EOF'
FROM registry.access.redhat.com/ubi10/ubi-minimal:latest
COPY skill-loader-linux /usr/local/bin/skill-loader
RUN mkdir -p /opt/skills /opt/skills-src
ENTRYPOINT ["skill-loader"]
EOF
"${CONTAINER_TOOL}" build --platform "${PLATFORM}" -q \
    -t localhost/harness-test:dev -f "${WORK}/Containerfile" "${WORK}" >/dev/null

echo "==> Loading both images into the node image store"
# minikube cannot read podman's local store directly, so go via an archive.
for img in konveyor-skills harness-test; do
    # podman needs telling; docker has no --format and writes this anyway.
    save_format=()
    if [ "${CONTAINER_TOOL}" = "podman" ]; then
        save_format=(--format docker-archive)
    fi
    "${CONTAINER_TOOL}" save "${save_format[@]}" \
        -o "${WORK}/${img}.tar" "localhost/${img}:dev"
    minikube image load -p "${KUBE_CONTEXT}" "${WORK}/${img}.tar"
done

# wait_for_pod <name> <expected phase>
wait_for_pod() {
    local name="$1" want="$2" phase=""
    for _ in $(seq 1 40); do
        phase="$(kc get pod "${name}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
        if [ "${phase}" = "Succeeded" ] || [ "${phase}" = "Failed" ]; then break; fi
        sleep 3
    done
    echo "--- ${name}: phase=${phase} (want ${want})"
    # A pod that never started has no logs, and under `set -e` with pipefail
    # that would kill the script before it printed which probe failed.
    kc logs "${name}" -c skill-loader 2>&1 | sed 's/^/    /' || true
    if [ "${phase}" != "${want}" ]; then
        echo "FAIL: ${name} reached ${phase}, want ${want}" >&2
        return 1
    fi
}

echo
echo "==> 1/4 mixed sources: image bundle + inline ConfigMap + git clone"
kc delete -f "${REPO_ROOT}/hack/skill-probe/mixed-sources.yaml" --ignore-not-found >/dev/null 2>&1
kc apply -f "${REPO_ROOT}/hack/skill-probe/mixed-sources.yaml" >/dev/null
wait_for_pod skill-loader-probe Succeeded
echo "    --- what the agent container saw ---"
kc logs skill-loader-probe -c agent 2>&1 | sed 's/^/    /' || true

echo
echo "==> 2/4 a skill with no frontmatter must fail the pod at init"
kc delete -f "${REPO_ROOT}/hack/skill-probe/bad-skill.yaml" --ignore-not-found >/dev/null 2>&1
kc apply -f "${REPO_ROOT}/hack/skill-probe/bad-skill.yaml" >/dev/null
wait_for_pod skill-loader-badskill Failed

echo
echo "==> 3/4 two sources declaring the same skill name must fail at init"
kc delete -f "${REPO_ROOT}/hack/skill-probe/name-collision.yaml" --ignore-not-found >/dev/null 2>&1
kc apply -f "${REPO_ROOT}/hack/skill-probe/name-collision.yaml" >/dev/null
wait_for_pod skill-loader-collision Failed

echo
echo "==> 4/4 an init container's \$HOME must not reach the agent container"
kc delete -f "${REPO_ROOT}/hack/skill-probe/home-boundary.yaml" --ignore-not-found >/dev/null 2>&1
kc apply -f "${REPO_ROOT}/hack/skill-probe/home-boundary.yaml" >/dev/null
for _ in $(seq 1 40); do
    phase="$(kc get pod home-boundary-probe -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    if [ "${phase}" = "Succeeded" ] || [ "${phase}" = "Failed" ]; then break; fi
    sleep 3
done
echo "--- home-boundary-probe: phase=${phase}"
kc logs home-boundary-probe -c init 2>&1 | sed 's/^/    init:  /' || true
kc logs home-boundary-probe -c agent 2>&1 | sed 's/^/    agent: /' || true
if kc logs home-boundary-probe -c agent 2>&1 | grep -q "No such file"; then
    echo "    OK: the link did not cross the container boundary"
else
    echo "FAIL: the agent container saw the init container's \$HOME" >&2
    exit 1
fi

echo
echo "All four probes behaved as the ADR describes."
