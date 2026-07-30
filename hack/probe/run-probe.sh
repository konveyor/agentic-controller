#!/usr/bin/env bash
# Run the staging-directory execution probe against a cluster.
#
# Answers: can an agent write a script to a writable directory in its pod and
# execute it -- and is that directory outside the git worktree the harness
# commits and force-pushes?
#
# Probes two places and compares them:
#   1. a standalone pod (no CRDs, no controller) -- fast, always available
#   2. the real controller-created Sandbox for an AgentRun, if one exists
# Divergence between the two is itself a finding: it would mean the controller's
# mount construction differs from a hand-written pod.
#
# Portable across macOS and Linux: no `timeout`, no GNU-only sed/date/stat.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# If DEV_KUBECONFIG is set, target that cluster. Without it kubectl falls
# through to ~/.kube/config -- i.e. whatever cluster you happen to be pointed
# at -- and this script CREATES PODS.
#
# Deliberately conditional: running against an arbitrary cluster (a real
# OpenShift, say) is the important use, so an explicitly-set KUBECONFIG with no
# DEV_KUBECONFIG is honoured as-is. The connectivity check below prints the
# target before anything is created.
if [ -n "${DEV_KUBECONFIG:-}" ]; then
    export KUBECONFIG="${DEV_KUBECONFIG}"
fi

PROBE_POD="${PROBE_POD:-imagevolume-probe}"
PROBE_NS="${PROBE_NS:-default}"
AGENT_RUN="${AGENT_RUN:-dev-run}"
AGENT_IMG="${DEV_AGENT_IMG:-quay.io/konveyor/agentic-controller-agent:e2e}"
SKILL_IMG="${PROBE_SKILL_IMG:-quay.io/konveyor/skills:maven-migration}"
RESULTS_DIR="${DEV_RESULTS_DIR:-${REPO_ROOT}/.dev/results}"
WAIT_SECONDS="${PROBE_WAIT_SECONDS:-120}"
KEEP="${PROBE_KEEP:-0}"

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARN:\033[0m %s\n' "$*" >&2; }
err()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; }

kube() { kubectl -n "${PROBE_NS}" "$@"; }

# ------------------------------------------------------- cluster attribution
#
# A verdict without this is not portable. "Scripts execute fine" means nothing
# unless you know which runtime produced it -- CRI-O and containerd handle image
# mounts differently, so a containerd answer does not transfer to OpenShift.

collect_cluster_facts() {
    log "Cluster attribution"

    # Show what we are about to create pods on, and stop if it is unreachable.
    # Proceeding would otherwise bury the real cause under a wall of
    # "connection refused" from every subsequent kubectl call.
    local ctx endpoint
    ctx="$(kubectl config current-context 2>/dev/null || echo unknown)"
    endpoint="$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null || true)"
    printf '  context:     %s\n' "${ctx}"
    printf '  endpoint:    %s\n' "${endpoint:-unknown}"
    printf '  kubeconfig:  %s\n' "${KUBECONFIG:-$HOME/.kube/config}"

    if ! kubectl get --raw /version >/dev/null 2>&1; then
        err "cannot reach the cluster at ${endpoint:-<unknown>} (context '${ctx}')."
        err "This script creates pods -- refusing to continue against an unreachable cluster."
        err "Point KUBECONFIG (or DEV_KUBECONFIG) at the cluster you want to probe."
        exit 1
    fi

    local server
    server="$(kubectl version -o json 2>/dev/null \
        | tr -d ' \n' | sed -n 's/.*"serverVersion".*"gitVersion":"\([^"]*\)".*/\1/p' || true)"
    printf '  kubernetes:  %s\n' "${server:-unknown}"
    printf 'RESULT cluster_k8s=%s\n' "${server:-unknown}" >> "${RAW_OUT}"

    local runtimes
    runtimes="$(kubectl get nodes \
        -o jsonpath='{range .items[*]}{.metadata.name}{"="}{.status.nodeInfo.containerRuntimeVersion}{"\n"}{end}' \
        2>/dev/null || true)"
    if [ -z "${runtimes}" ]; then
        warn "could not read node runtime versions"
    fi
    while IFS= read -r line; do
        [ -n "${line}" ] || continue
        printf '  runtime:     %s\n' "${line}"
        printf 'RESULT cluster_runtime=%s\n' "${line#*=}" >> "${RAW_OUT}"

        case "${line#*=}" in
            docker://*)
                warn "node runs the docker runtime (cri-dockerd), which does not implement"
                warn "ImageVolume at all -- the skill-mount rows will be unavailable."
                ;;
            cri-o://*)
                # CRI-O added ImageVolume support in 1.31.
                local ver="${line#*cri-o://}"
                case "${ver}" in
                    1.2*|1.30.*) warn "cri-o ${ver} predates ImageVolume support (needs >= 1.31)" ;;
                esac
                ;;
            containerd://1.*)
                warn "containerd 1.x does not implement ImageVolume (needs >= 2.0)"
                ;;
        esac
    done <<EOF
${runtimes}
EOF
}

# ImageVolume is a feature gate. If it is off, the API server prunes the `image`
# field from the volume, and pod creation fails with "must specify a volume
# type" -- which looks like a malformed manifest rather than a disabled feature.
# Check the kubelet, since that is the component that has to honour it.
check_imagevolume_gate() {
    local node metrics state
    node="$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
    [ -n "${node}" ] || { warn "no nodes found; skipping feature-gate check"; return 0; }

    metrics="$(kubectl get --raw "/api/v1/nodes/${node}/proxy/metrics" 2>/dev/null || true)"
    if [ -z "${metrics}" ]; then
        warn "could not read kubelet metrics; feature-gate state unknown"
        return 0
    fi

    state="$(printf '%s\n' "${metrics}" \
        | grep '^kubernetes_feature_enabled{name="ImageVolume"' | head -1 || true)"
    if [ -z "${state}" ]; then
        printf '  ImageVolume: not reported by kubelet (likely GA or removed gate)\n'
        return 0
    fi

    printf '  ImageVolume: %s\n' "${state}"
    printf 'RESULT cluster_imagevolume_gate=%s\n' "${state}" >> "${RAW_OUT}"
    case "${state}" in
        *"} 0") warn "ImageVolume feature gate is DISABLED on the kubelet -- skill mounts will fail" ;;
    esac
}

# --------------------------------------------------------------- probe pod

dump_failure_context() {
    local pod=$1
    err "probe pod '${pod}' did not become ready; dumping context"
    kube describe pod "${pod}" 2>&1 | sed 's/^/    /' || true
    printf '\n  --- recent events ---\n'
    kube get events --sort-by=.lastTimestamp 2>&1 | tail -25 | sed 's/^/    /' || true
    printf '\n  --- security context / SCC ---\n'
    kube get pod "${pod}" -o jsonpath='{.spec.securityContext}{"\n"}' 2>/dev/null | sed 's/^/    /' || true
    kube get pod "${pod}" -o jsonpath='{.metadata.annotations.openshift\.io/scc}{"\n"}' 2>/dev/null | sed 's/^/    /' || true
}

start_probe_pod() {
    log "Creating standalone probe pod '${PROBE_POD}'"
    kube delete pod "${PROBE_POD}" --ignore-not-found --wait=true >/dev/null 2>&1 || true

    # Pipe through sed rather than sed -i: BSD and GNU disagree on -i's argument.
    sed -e "s|quay.io/konveyor/agentic-controller-agent:e2e|${AGENT_IMG}|" \
        -e "s|quay.io/konveyor/skills:maven-migration|${SKILL_IMG}|" \
        "${SCRIPT_DIR}/probe-pod.yaml" \
        | kube apply -f - >/dev/null

    log "Waiting for probe pod (up to ${WAIT_SECONDS}s)"
    if ! kube wait --for=condition=Ready "pod/${PROBE_POD}" --timeout="${WAIT_SECONDS}s" >/dev/null 2>&1; then
        dump_failure_context "${PROBE_POD}"
        return 1
    fi
}

# Deliver the probe over stdin. Nothing is baked into any image, so iterating on
# probe.sh needs no rebuild -- and this works identically against the standalone
# pod and a real Sandbox.
run_probe_in() {
    local pod=$1 container=${2:-} label=$3
    local args=(exec -i "${pod}")
    [ -n "${container}" ] && args+=(-c "${container}")
    args+=(-- sh -s)

    log "Probing ${label} (pod/${pod})"
    if ! kube "${args[@]}" < "${SCRIPT_DIR}/probe.sh" > "${RESULTS_DIR}/${label}.txt" 2>&1; then
        warn "probe exec failed against ${label}; partial output retained"
    fi
    sed "s/^RESULT /RESULT ${label}./" "${RESULTS_DIR}/${label}.txt" >> "${RAW_OUT}" 2>/dev/null || true
}

summarize() {
    local label=$1 file="${RESULTS_DIR}/$1.txt"
    [ -f "${file}" ] || return 0
    printf '\n  %s\n' "${label}"
    grep -E '^RESULT (verdict|remediation|staging_best|staging_exec_any|cluster_)' "${file}" 2>/dev/null \
        | sed 's/^RESULT /    /' || true
    printf '    --- per-directory ---\n'
    grep -E '^RESULT stage_.*_(noexec|exec|in_worktree)=' "${file}" 2>/dev/null \
        | sed 's/^RESULT /    /' || true
    printf '    --- skill mount (informational) ---\n'
    grep -E '^RESULT skill_.*_(noexec|ro|readable)=' "${file}" 2>/dev/null \
        | sed 's/^RESULT /    /' || true
}

main() {
    mkdir -p "${RESULTS_DIR}"
    RAW_OUT="${RESULTS_DIR}/probe-raw.txt"
    : > "${RAW_OUT}"

    collect_cluster_facts
    check_imagevolume_gate

    if start_probe_pod; then
        run_probe_in "${PROBE_POD}" "" standalone
    else
        err "standalone probe unavailable"
    fi

    # The real thing: whatever the controller actually built.
    local sandbox
    sandbox="$(kube get agentrun "${AGENT_RUN}" -o jsonpath='{.status.sandboxName}' 2>/dev/null || true)"
    if [ -n "${sandbox}" ] && kube get pod "${sandbox}" >/dev/null 2>&1; then
        run_probe_in "${sandbox}" agent sandbox
    else
        log "No Sandbox pod for AgentRun '${AGENT_RUN}' -- skipping the in-system probe."
        log "  (create an AgentRun, then re-run with AGENT_RUN=<name>, to compare"
        log "   the standalone result against a real controller-created pod)"
    fi

    printf '\n'
    log "Results"
    summarize standalone
    summarize sandbox

    # Disagreement means the controller's pod shape differs from the hand-written
    # one -- worth knowing before trusting either number.
    if [ -f "${RESULTS_DIR}/standalone.txt" ] && [ -f "${RESULTS_DIR}/sandbox.txt" ]; then
        local a b
        a="$(grep '^RESULT verdict=' "${RESULTS_DIR}/standalone.txt" 2>/dev/null || true)"
        b="$(grep '^RESULT verdict=' "${RESULTS_DIR}/sandbox.txt" 2>/dev/null || true)"
        printf '\n'
        if [ -n "${a}" ] && [ "${a}" = "${b}" ]; then
            log "Standalone and Sandbox verdicts AGREE: ${a#RESULT verdict=}"
        else
            warn "Standalone and Sandbox verdicts DIFFER:"
            warn "  standalone: ${a#RESULT verdict=}"
            warn "  sandbox:    ${b#RESULT verdict=}"
            warn "The controller's mount construction differs from the hand-written pod."
        fi
    fi

    printf '\n  raw output: %s\n' "${RAW_OUT}"

    if [ "${KEEP}" != "1" ]; then
        kube delete pod "${PROBE_POD}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    else
        log "Keeping probe pod (PROBE_KEEP=1): kubectl -n ${PROBE_NS} exec -it ${PROBE_POD} -- sh"
    fi
}

main "$@"
