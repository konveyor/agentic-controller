#!/usr/bin/env bash
# Run the e2e test: create resources, verify the full pipeline works.
#
# The test verifies:
# 1. SkillCard becomes Ready (image resolved)
# 2. LLMProvider becomes Ready (verification Job succeeds)
# 3. Agent becomes Ready (all dependencies resolved)
# 4. AgentRun creates a Sandbox
# 5. Sandbox pod runs and produces expected output
#
# Prerequisites:
#   - Kind cluster with Agent Sandbox (hack/start-kind.sh)
#   - Controller deployed (hack/setup-e2e.sh)
#
# Environment variables:
#   E2E_TIMEOUT   Timeout for waiting (default: 180s)

set -euo pipefail

E2E_TIMEOUT="${E2E_TIMEOUT:-180s}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

echo "=== E2E Test: Full AgentRun Pipeline ==="
echo ""

# Clean up any previous test resources.
kubectl delete -f "${SCRIPT_DIR}/e2e/resources.yaml" --ignore-not-found 2>/dev/null || true
sleep 2

echo "--- Applying test resources ---"
kubectl apply -f "${SCRIPT_DIR}/e2e/resources.yaml"
echo ""

echo "--- Checking SkillCard ---"
if kubectl wait skillcard/e2e-skill --for=jsonpath='{.status.conditions[0].status}'=True --timeout="${E2E_TIMEOUT}" 2>/dev/null; then
    pass "SkillCard e2e-skill is Ready"
else
    fail "SkillCard e2e-skill did not become Ready"
fi

echo "--- Checking LLMProvider ---"
if kubectl wait llmprovider/e2e-provider --for=jsonpath='{.status.conditions[0].status}'=True --timeout="${E2E_TIMEOUT}" 2>/dev/null; then
    pass "LLMProvider e2e-provider is Ready (connectivity verified)"
else
    fail "LLMProvider e2e-provider did not become Ready"
    kubectl get llmprovider e2e-provider -o yaml | grep -A10 "status:" || true
fi

echo "--- Checking Agent ---"
if kubectl wait agent/e2e-agent --for=jsonpath='{.status.conditions[0].status}'=True --timeout="${E2E_TIMEOUT}" 2>/dev/null; then
    pass "Agent e2e-agent is Ready (all dependencies resolved)"
else
    fail "Agent e2e-agent did not become Ready"
    kubectl get agent e2e-agent -o yaml | grep -A10 "status:" || true
fi

echo "--- Checking AgentRun creates Sandbox ---"
SANDBOX=""
for i in $(seq 1 60); do
    SANDBOX=$(kubectl get agentrun e2e-run -o jsonpath='{.status.sandboxName}' 2>/dev/null)
    if [ -n "${SANDBOX}" ]; then
        break
    fi
    sleep 1
done
if [ -n "${SANDBOX}" ]; then
    pass "AgentRun created Sandbox: ${SANDBOX}"
else
    fail "AgentRun did not create a Sandbox"
    kubectl get agentrun e2e-run -o yaml | grep -A15 "status:" || true
fi

echo "--- Checking Sandbox pod ran entrypoint ---"
if [ -n "${SANDBOX}" ]; then
    # Wait for pod to have run at least once (it exits immediately).
    for i in $(seq 1 30); do
        LOGS=$(kubectl logs "${SANDBOX}" 2>/dev/null || true)
        if echo "${LOGS}" | grep -q "Agent run completed successfully"; then
            break
        fi
        sleep 1
    done

    if echo "${LOGS}" | grep -q "Agent run completed successfully"; then
        pass "Entrypoint ran successfully"
    else
        fail "Entrypoint did not produce expected output"
        echo "  Pod logs: ${LOGS}"
    fi

    # Verify expected content in the logs.
    if echo "${LOGS}" | grep -q "KONVEYOR_PARAM_SOURCE_URL"; then
        pass "Params injected as env vars"
    else
        fail "Params not found in pod logs"
    fi

    if echo "${LOGS}" | grep -q "Skills:"; then
        pass "Skills directory mounted"
    else
        fail "Skills not visible in pod logs"
    fi

    if echo "${LOGS}" | grep -q "This is an e2e test"; then
        pass "Instructions passed through"
    else
        fail "Instructions not found in pod logs"
    fi

    if echo "${LOGS}" | grep -q "You are an e2e test agent"; then
        pass "Agent prompt passed through"
    else
        fail "Agent prompt not found in pod logs"
    fi
fi

echo "--- Checking ACP Secret ---"
if kubectl get secret e2e-run-acp-key -o jsonpath='{.data.secret-key}' &>/dev/null; then
    pass "ACP Secret created with secret-key"
else
    fail "ACP Secret not found"
fi

echo ""
echo "=== Results ==="
echo "  Passed: ${PASS}"
echo "  Failed: ${FAIL}"
echo ""

if [ "${FAIL}" -gt 0 ]; then
    echo "E2E FAILED"
    echo ""
    echo "--- Debug info ---"
    kubectl get skillcard,llmprovider,agent,agentrun -o wide 2>/dev/null || true
    echo ""
    kubectl get sandbox,pods -o wide 2>/dev/null || true
    exit 1
fi

echo "E2E PASSED: Full pipeline verified."
echo ""
echo "  Secret -> LLMProvider (verified) -> SkillCard (resolved)"
echo "  -> Agent (all deps ready) -> AgentRun -> Sandbox -> Pod"
echo "  -> Params injected, skills mounted, instructions passed"
