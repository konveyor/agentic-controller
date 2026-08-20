#!/usr/bin/env bash
# Run the e2e test: create resources, verify the full pipeline works.
#
# The test verifies:
# 1. SkillCard becomes Ready (image resolved)
# 2. Gateway becomes Ready (verification Job succeeds)
# 3. Agent becomes Ready (all dependencies resolved)
# 4. AgentRun creates a Sandbox
# 5. AgentRun reports Running once the pod runs, keeps ACPReady=False
#    until the pod's ACP port accepts, then ACPReady=True — and the first
#    dial after ACPReady succeeds
# 6. Sandbox pod runs and produces expected output
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
SKIP=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }
skip() { echo "  SKIP: $1"; SKIP=$((SKIP + 1)); }

echo "=== E2E Test: Full AgentRun Pipeline ==="
echo ""

# Clean up any previous test resources.
kubectl delete -f "${SCRIPT_DIR}/e2e/resources.yaml" --ignore-not-found 2>/dev/null || true
# The sandbox pod is named after the run; make sure a previous run's pod is
# really gone before the new Sandbox creates its namesake, or the checks
# below would read the old pod (still answering on :4000 while it drains).
kubectl wait --for=delete pod/e2e-run --timeout=90s 2>/dev/null || true
sleep 2

# In-cluster dialer for the ACP checks below: an exec target that is
# Ready BEFORE the AgentRun exists, so the "first dial" really is the first
# packet after the phase change (no pod scheduling in between). It reuses
# the already-loaded e2e agent image (has curl-minimal) — no registry pull.
DIALER="acp-dialer"
DIALER_IMAGE="${CONTROLLER_AGENT_IMG:-quay.io/konveyor/agentic-controller-agent:e2e}"
NS="$(kubectl config view --minify -o jsonpath='{..namespace}' 2>/dev/null)"
NS="${NS:-default}"
kubectl delete pod "${DIALER}" --ignore-not-found --now --wait=true 2>/dev/null || true
kubectl run "${DIALER}" --restart=Never --image="${DIALER_IMAGE}" \
    --image-pull-policy=IfNotPresent --command -- sleep infinity >/dev/null
if ! kubectl wait pod/"${DIALER}" --for=condition=Ready --timeout="${E2E_TIMEOUT}" >/dev/null 2>&1; then
    echo "ERROR: dialer pod ${DIALER} (${DIALER_IMAGE}) did not become Ready" >&2
    kubectl describe pod "${DIALER}" | tail -20 || true
    exit 1
fi

# dial HOST PORT -> prints "<http_code> rc=<curl exit>" from inside the
# cluster; an exec failure prints "exec-failed" so it is never mistaken
# for a dial result. curl rc 7 = connection refused, 6 = DNS, 28 = timeout.
dial() {
    kubectl exec "${DIALER}" -- sh -c \
        "curl -s -o /dev/null -w '%{http_code}' --max-time 3 http://$1:$2/; echo \" rc=\$?\"" 2>/dev/null \
        || echo "exec-failed"
}

echo "--- Applying test resources ---"
kubectl apply -f "${SCRIPT_DIR}/e2e/resources.yaml"
echo ""

echo "--- Checking SkillCard ---"
if kubectl wait skillcard/e2e-skill --for=jsonpath='{.status.conditions[0].status}'=True --timeout="${E2E_TIMEOUT}" 2>/dev/null; then
    pass "SkillCard e2e-skill is Ready"
else
    fail "SkillCard e2e-skill did not become Ready"
fi

echo "--- Checking Gateway ---"
if kubectl wait gateways.konveyor.io/e2e-gateway --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True --timeout="${E2E_TIMEOUT}" 2>/dev/null; then
    pass "Gateway e2e-gateway is Ready (connectivity verified)"
else
    fail "Gateway e2e-gateway did not become Ready"
    kubectl get gateways.konveyor.io e2e-gateway -o yaml | grep -A10 "status:" || true
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

echo "--- Checking Phase vs ACPReady (the readiness contract) ---"
# The stub binds :4000 only STUB_ACP_DELAY_SECONDS after start (see
# e2e/resources.yaml). Phase must say Running as soon as the pod runs,
# ACPReady must stay False until the port accepts, and once it is True a
# single dial — no retry — must succeed.
if [ -n "${SANDBOX}" ]; then
    acp_ready() { kubectl get agentrun e2e-run -o jsonpath='{.status.conditions[?(@.type=="ACPReady")].status}' 2>/dev/null; }
    acp_reason() { kubectl get agentrun e2e-run -o jsonpath='{.status.conditions[?(@.type=="ACPReady")].reason}' 2>/dev/null; }
    run_phase() { kubectl get agentrun e2e-run -o jsonpath='{.status.phase}' 2>/dev/null; }

    # Pre-listen witness: the pod is Running (process executing) but
    # nothing accepts on :4000 yet. Dialed by pod IP so no DNS negative
    # cache is seeded for the Service name that the positive check
    # resolves later.
    kubectl wait pod/"${SANDBOX}" --for=jsonpath='{.status.phase}'=Running --timeout=60s >/dev/null 2>&1 || true
    POD_IP=$(kubectl get pod "${SANDBOX}" -o jsonpath='{.status.podIP}' 2>/dev/null || true)
    PRE=$(dial "${POD_IP:-127.0.0.1}" 4000)
    PHASE=$(run_phase); ACP=$(acp_ready); ACPR=$(acp_reason)
    case "${PRE}" in
        *"rc=7"*)
            if [ "${PHASE}" = "Running" ] && [ "${ACP}" = "False" ]; then
                pass "Pod running, :4000 closed -> phase=Running, ACPReady=False (${ACPR})"
            else
                fail "Pod running, :4000 closed -> phase=${PHASE:-<unset>}, ACPReady=${ACP:-<unset>} (${ACPR:-<no reason>}) (pod ${POD_IP})"
            fi
            ;;
        *"rc=0"*)
            skip "pre-listen window not observable (stub already bound :4000 before we looked; phase=${PHASE:-<unset>} ACPReady=${ACP:-<unset>})"
            ;;
        *)
            skip "pre-listen window inconclusive (podIP=${POD_IP:-<none>}, dial=${PRE})"
            ;;
    esac

    # ACPReady gate: wait for True, but bail early if the run ends first.
    ACP=""
    for i in $(seq 1 "${E2E_TIMEOUT%s}"); do
        ACP=$(acp_ready); PHASE=$(run_phase)
        [ "${ACP}" = "True" ] && break
        case "${PHASE}" in Succeeded|Failed) break ;; esac
        sleep 1
    done
    if [ "${ACP}" = "True" ]; then
        pass "ACPReady turned True ($(acp_reason)) with phase=${PHASE}"
        POD_READY=$(kubectl get pod "${SANDBOX}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
        SBX_READY=$(kubectl get sandbox "${SANDBOX}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
        if [ "${POD_READY}" = "True" ] && [ "${SBX_READY}" = "True" ]; then
            pass "ACPReady=True coincides with pod Ready=True and Sandbox Ready=True"
        else
            fail "ACPReady=True reported with pod Ready=${POD_READY:-?} Sandbox Ready=${SBX_READY:-?}"
        fi
        # Timing-independent witness of the probe: the pod's Ready
        # transition must not precede the stub's own "listening" line.
        LISTEN_TS=$(kubectl logs "${SANDBOX}" --timestamps 2>/dev/null | grep -m1 "ACP: listening" | cut -d' ' -f1)
        READY_TS=$(kubectl get pod "${SANDBOX}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].lastTransitionTime}' 2>/dev/null)
        if [ -n "${LISTEN_TS}" ] && [ -n "${READY_TS}" ] && [ "${READY_TS}" \> "${LISTEN_TS%%.*}" ]; then
            pass "pod Ready (${READY_TS}) followed the stub binding :4000 (${LISTEN_TS%%.*}Z)"
        elif [ -n "${LISTEN_TS}" ] && [ -n "${READY_TS}" ]; then
            fail "pod Ready (${READY_TS}) did not follow the stub binding :4000 (${LISTEN_TS})"
        else
            skip "could not read listen/ready timestamps (listen='${LISTEN_TS}', ready='${READY_TS}')"
        fi
        # First dial, exactly once, against what readiness guarantees: the
        # pod's ACP port.
        POD_IP=$(kubectl get pod "${SANDBOX}" -o jsonpath='{.status.podIP}' 2>/dev/null)
        FIRST=$(dial "${POD_IP}" 4000)
        if [ "${FIRST%% *}" = "200" ]; then
            pass "First dial of the pod ACP port (${POD_IP}:4000) after ACPReady accepted (HTTP 200)"
        else
            fail "First dial of the pod ACP port (${POD_IP}:4000) after ACPReady did not accept (${FIRST})"
        fi
        # The name clients actually use. Sandbox Ready guarantees the
        # headless Service exists; its A record additionally depends on
        # EndpointSlice -> CoreDNS propagation, which nothing in the
        # ACPReady chain waits for — so allow a short bounded window here.
        SVC="${SANDBOX}.${NS}.svc"
        VIA_SVC=""
        for i in $(seq 1 10); do
            VIA_SVC=$(dial "${SVC}" 4000)
            [ "${VIA_SVC%% *}" = "200" ] && break
            sleep 1
        done
        if [ "${VIA_SVC%% *}" = "200" ]; then
            pass "${SVC}:4000 accepts after ACPReady (HTTP 200 within ${i}s)"
        else
            fail "${SVC}:4000 did not accept within 10s of ACPReady (${VIA_SVC})"
        fi
    else
        fail "ACPReady did not turn True (ACPReady=${ACP:-<unset>} $(acp_reason), phase=${PHASE:-<unset>})"
        kubectl get agentrun e2e-run -o yaml | grep -A25 "status:" || true
        kubectl get pod "${SANDBOX}" -o yaml | grep -A20 "conditions:" || true
    fi
fi

echo "--- Checking Sandbox pod ran entrypoint ---"
if [ -n "${SANDBOX}" ]; then
    # Wait for the entrypoint's banner (printed before it binds :4000).
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
        # The agent container never runs if an init container fails, and its
        # logs are the only place that says why. Without this the run reports
        # a wall of missing-output failures and nothing about the cause.
        for init in $(kubectl get pod "${SANDBOX}" \
            -o jsonpath='{range .status.initContainerStatuses[*]}{.name}{"\n"}{end}' 2>/dev/null); do
            state=$(kubectl get pod "${SANDBOX}" -o jsonpath="{.status.initContainerStatuses[?(@.name=='${init}')].state}" 2>/dev/null)
            echo "  init ${init}: ${state}"
            kubectl logs "${SANDBOX}" -c "${init}" 2>&1 | sed 's/^/    /' || true
        done
    fi

    # Verify expected content in the logs.
    if echo "${LOGS}" | grep -q "KONVEYOR_PARAM_SOURCE_URL"; then
        pass "Params injected as env vars"
    else
        fail "Params not found in pod logs"
    fi

    # The stub prints "Skills:" followed by an ls, so grepping the label alone
    # passes with an empty directory. Name the skill the SkillCard resolves to,
    # which is its frontmatter name rather than the card name.
    if echo "${LOGS}" | grep -qE "Skills:.*maven-migration"; then
        pass "Skills directory mounted"
    else
        fail "Skills not visible in pod logs (want maven-migration under Skills:)"
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

kubectl delete pod "${DIALER}" --ignore-not-found --wait=false >/dev/null 2>&1 || true

echo ""
echo "=== Results ==="
echo "  Passed: ${PASS}"
echo "  Failed: ${FAIL}"
echo "  Skipped: ${SKIP}"
echo ""

if [ "${FAIL}" -gt 0 ]; then
    echo "E2E FAILED"
    echo ""
    echo "--- Debug info ---"
    kubectl get skillcard,gateways.konveyor.io,agent,agentrun -o wide 2>/dev/null || true
    echo ""
    kubectl get sandbox,pods -o wide 2>/dev/null || true
    exit 1
fi

echo "E2E PASSED: Full pipeline verified."
echo ""
echo "  Secret -> Gateway (verified) -> SkillCard (resolved)"
echo "  -> Agent (all deps ready) -> AgentRun -> Sandbox -> Pod"
echo "  -> Params injected, skills mounted, instructions passed"
