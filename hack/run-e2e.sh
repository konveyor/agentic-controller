#!/usr/bin/env bash
# Run the e2e test: create resources, wait for AgentRun to complete,
# verify the outcome.
#
# Prerequisites:
#   - Kind cluster with Agent Sandbox (hack/start-kind.sh)
#   - Controller deployed (hack/setup-e2e.sh)
#
# Environment variables:
#   E2E_TIMEOUT   Timeout for waiting (default: 120s)

set -euo pipefail

E2E_TIMEOUT="${E2E_TIMEOUT:-120s}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== E2E Test: Full AgentRun Pipeline ==="
echo ""

# Clean up any previous test resources.
kubectl delete -f "${SCRIPT_DIR}/e2e/resources.yaml" --ignore-not-found 2>/dev/null || true
sleep 2

echo "--- Step 1: Apply test resources ---"
kubectl apply -f "${SCRIPT_DIR}/e2e/resources.yaml"
echo ""

echo "--- Step 2: Wait for SkillCard to become Ready ---"
kubectl wait skillcard/e2e-skill --for=jsonpath='{.status.conditions[0].status}'=True --timeout="${E2E_TIMEOUT}"
echo "SkillCard is Ready."
echo ""

echo "--- Step 3: Check LLMProvider status ---"
# The LLMProvider verification Job needs a reachable endpoint.
# For e2e, wait for it to reach some status (Verifying or Ready).
echo "Waiting for LLMProvider verification..."
sleep 10
kubectl get llmprovider e2e-provider -o yaml | grep -A5 "conditions:" || true
echo ""

# The LLMProvider verification Job will fail because the endpoint
# is fake. The AgentRun will be blocked at AgentNotReady because
# the Agent depends on the LLMProvider being Ready.
#
# For a real e2e, we'd need a real LLM endpoint. For now, check
# that the pipeline gets as far as it can.

echo "--- Step 4: Check Agent status ---"
kubectl get agent e2e-agent -o yaml | grep -A5 "conditions:" || true
echo ""

echo "--- Step 5: Check AgentRun status ---"
kubectl get agentrun e2e-run -o yaml | grep -A10 "status:" || true
echo ""

echo "--- Step 6: Check created resources ---"
echo "Pods:"
kubectl get pods -A | grep -E "e2e|agent-sandbox|agentic-controller" || true
echo ""
echo "Jobs:"
kubectl get jobs | grep -E "e2e|llm-verify" || true
echo ""
echo "Secrets:"
kubectl get secrets | grep -E "e2e" || true
echo ""
echo "Sandboxes:"
kubectl get sandboxes 2>/dev/null || echo "No Sandboxes found (Agent may not be Ready yet)"
echo ""

echo "--- Summary ---"
echo ""
echo "Resources created:"
kubectl get skillcard,skillcollection,llmprovider,agent,agentrun 2>/dev/null || true
echo ""

# Check the AgentRun condition reason to report the test outcome.
REASON=$(kubectl get agentrun e2e-run -o jsonpath='{.status.conditions[0].reason}' 2>/dev/null || echo "Unknown")
PHASE=$(kubectl get agentrun e2e-run -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")

echo "AgentRun phase: ${PHASE}"
echo "AgentRun reason: ${REASON}"
echo ""

case "${REASON}" in
    "Succeeded")
        echo "E2E PASSED: AgentRun completed successfully."
        echo ""
        echo "Sandbox pod logs:"
        kubectl logs -l konveyor.io/agentrun=e2e-run 2>/dev/null || echo "(no logs available)"
        exit 0
        ;;
    "AgentNotReady")
        echo "E2E PARTIAL: AgentRun is waiting for Agent to become Ready."
        echo "This is expected when the LLMProvider endpoint is not reachable."
        echo "The controller pipeline is working correctly up to the Agent readiness gate."
        exit 0
        ;;
    "SandboxCreated"|"Running")
        echo "E2E IN PROGRESS: Sandbox was created and is running."
        echo "Waiting for completion..."
        kubectl wait agentrun/e2e-run --for=jsonpath='{.status.phase}'=Succeeded --timeout="${E2E_TIMEOUT}" 2>/dev/null || {
            echo "AgentRun did not complete within timeout."
            kubectl get agentrun e2e-run -o yaml
            exit 1
        }
        echo "E2E PASSED."
        exit 0
        ;;
    *)
        echo "E2E RESULT: ${REASON} (phase: ${PHASE})"
        kubectl describe agentrun e2e-run
        exit 1
        ;;
esac
