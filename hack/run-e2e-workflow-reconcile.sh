#!/usr/bin/env bash
# Verify AgentWorkflowRun reconciliation across a controller restart.
#
# This reuses the existing deterministic E2E resources and agent stub. It
# restarts the controller after stage A has been created, then verifies that:
#   1. stage A still has exactly one AgentRun;
#   2. the recorded stage resumes instead of creating a duplicate child; and
#   3. stage B is created and the workflow run completes successfully.
#
# Prerequisites:
#   - A Kind cluster with Agent Sandbox and the controller deployed
#     (hack/start-kind.sh and hack/setup-e2e.sh)
#   - kubectl pointed at that cluster
#
# Environment variables:
#   E2E_TIMEOUT_SECONDS  Bounded wait for each assertion (default: 180)
#   E2E_DEPLOYMENT       Controller Deployment (default: agentic-controller-controller-manager)
#   E2E_NAMESPACE        Namespace (default: current kubectl namespace or default)
#   E2E_CONTROLLER_NAMESPACE Namespace containing the controller (default: agentic-controller-system)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_TIMEOUT_SECONDS="${E2E_TIMEOUT_SECONDS:-180}"
E2E_DEPLOYMENT="${E2E_DEPLOYMENT:-agentic-controller-controller-manager}"
E2E_NAMESPACE="${E2E_NAMESPACE:-$(kubectl config view --minify -o jsonpath='{..namespace}' 2>/dev/null || true)}"
E2E_NAMESPACE="${E2E_NAMESPACE:-default}"
E2E_CONTROLLER_NAMESPACE="${E2E_CONTROLLER_NAMESPACE:-agentic-controller-system}"
RUN_NAME="e2e-workflow-run"

fail() {
    echo "FAIL: $*" >&2
    kubectl -n "${E2E_NAMESPACE}" get agentworkflowrun "${RUN_NAME}" -o yaml 2>/dev/null || true
    kubectl -n "${E2E_NAMESPACE}" get agentrun -l "konveyor.io/agentworkflowrun=${RUN_NAME}" -o wide 2>/dev/null || true
    exit 1
}

cleanup() {
    kubectl -n "${E2E_NAMESPACE}" delete -f "${SCRIPT_DIR}/e2e/workflow-resources.yaml" \
        --ignore-not-found >/dev/null 2>&1 || true
    kubectl -n "${E2E_NAMESPACE}" delete -f "${SCRIPT_DIR}/e2e/resources.yaml" \
        --ignore-not-found >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "=== E2E: AgentWorkflowRun restart/reconcile ==="
echo "Namespace: ${E2E_NAMESPACE}"
echo "Controller: ${E2E_DEPLOYMENT}"
echo "Controller namespace: ${E2E_CONTROLLER_NAMESPACE}"

cleanup
kubectl -n "${E2E_NAMESPACE}" apply -f "${SCRIPT_DIR}/e2e/resources.yaml"

kubectl -n "${E2E_NAMESPACE}" wait gateways.konveyor.io/e2e-gateway \
    --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
    --timeout="${E2E_TIMEOUT_SECONDS}s" >/dev/null || fail "Gateway did not become Ready"
kubectl -n "${E2E_NAMESPACE}" wait agent/e2e-agent \
    --for=jsonpath='{.status.conditions[0].status}'=True \
    --timeout="${E2E_TIMEOUT_SECONDS}s" >/dev/null || fail "Agent did not become Ready"

kubectl -n "${E2E_NAMESPACE}" apply -f "${SCRIPT_DIR}/e2e/workflow-resources.yaml"
kubectl -n "${E2E_NAMESPACE}" wait agentworkflow/e2e-workflow \
    --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
    --timeout="${E2E_TIMEOUT_SECONDS}s" >/dev/null || fail "Workflow did not become Ready"

echo "Waiting for stage A AgentRun to be recorded..."
stage_a_name=""
for _ in $(seq 1 "${E2E_TIMEOUT_SECONDS}"); do
    stage_a_name=$(kubectl -n "${E2E_NAMESPACE}" get agentworkflowrun "${RUN_NAME}" \
        -o jsonpath='{.status.stages[0].agentRunName}' 2>/dev/null || true)
    if [ -n "${stage_a_name}" ]; then
        break
    fi
    sleep 1
done
[ -n "${stage_a_name}" ] || fail "stage A AgentRun was not recorded"

stage_a_phase=$(kubectl -n "${E2E_NAMESPACE}" get agentworkflowrun "${RUN_NAME}" \
    -o jsonpath='{.status.stages[0].phase}' 2>/dev/null || true)
case "${stage_a_phase}" in
    Pending|Running) ;;
    *) fail "stage A phase was invalid or empty: ${stage_a_phase:-<empty>}" ;;
esac
echo "Stage A recorded as ${stage_a_name} (phase=${stage_a_phase})"

echo "Restarting controller while stage A is active..."
kubectl -n "${E2E_CONTROLLER_NAMESPACE}" rollout restart "deployment/${E2E_DEPLOYMENT}"
kubectl -n "${E2E_CONTROLLER_NAMESPACE}" rollout status "deployment/${E2E_DEPLOYMENT}" \
    --timeout="${E2E_TIMEOUT_SECONDS}s" >/dev/null || fail "Controller restart did not complete"

echo "Verifying reconciliation resumed without duplicate stage A..."
stage_b_name=""
last_state=""
for _ in $(seq 1 "${E2E_TIMEOUT_SECONDS}"); do
    stage_a_count=$(kubectl -n "${E2E_NAMESPACE}" get agentrun \
        -l "konveyor.io/agentworkflowrun=${RUN_NAME},konveyor.io/stage=stage-a" \
        --no-headers 2>/dev/null | awk 'NF { count++ } END { print count + 0 }')
    [ "${stage_a_count}" = "1" ] || fail "expected exactly one stage A AgentRun, found ${stage_a_count}"

    stage_a_phase=$(kubectl -n "${E2E_NAMESPACE}" get agentworkflowrun "${RUN_NAME}" \
        -o jsonpath='{.status.stages[0].phase}' 2>/dev/null || true)
    stage_b_name=$(kubectl -n "${E2E_NAMESPACE}" get agentworkflowrun "${RUN_NAME}" \
        -o jsonpath='{.status.stages[1].agentRunName}' 2>/dev/null || true)
    stage_b_phase=$(kubectl -n "${E2E_NAMESPACE}" get agentworkflowrun "${RUN_NAME}" \
        -o jsonpath='{.status.stages[1].phase}' 2>/dev/null || true)
    workflow_phase=$(kubectl -n "${E2E_NAMESPACE}" get agentworkflowrun "${RUN_NAME}" \
        -o jsonpath='{.status.phase}' 2>/dev/null || true)
    state="workflow=${workflow_phase:-<empty>} stageA=${stage_a_phase:-<empty>} stageB=${stage_b_phase:-<empty>} stageAChildren=${stage_a_count}"
    if [ "${state}" != "${last_state}" ]; then
        echo "  ${state}"
        last_state="${state}"
    fi
    if [ -n "${stage_b_name}" ] && [ "${workflow_phase}" = "Succeeded" ]; then
        break
    fi
    case "${workflow_phase}" in
        Failed) fail "workflow run failed after controller restart" ;;
    esac
    sleep 1
done

[ "${stage_a_count:-0}" = "1" ] || fail "stage A was duplicated"
[ -n "${stage_b_name}" ] || fail "stage B AgentRun was not created"
[ "${workflow_phase:-}" = "Succeeded" ] || fail "workflow did not succeed: ${workflow_phase:-<empty>}"

stage_b_count=$(kubectl -n "${E2E_NAMESPACE}" get agentrun \
    -l "konveyor.io/agentworkflowrun=${RUN_NAME},konveyor.io/stage=stage-b" \
    --no-headers 2>/dev/null | awk 'NF { count++ } END { print count + 0 }')
[ "${stage_b_count}" = "1" ] || fail "expected exactly one stage B AgentRun, found ${stage_b_count}"

echo "PASS: controller restart preserved one stage A child, created one stage B child, and completed the workflow."
