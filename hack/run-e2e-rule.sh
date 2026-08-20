#!/usr/bin/env bash
# Does an always-loaded rule's content actually reach the model?
#
# Every other check in this repo proves a rule was delivered to the pod. This
# one follows it one hop further, onto the wire: the emulator records the exact
# request body goose sent, and the assertion is made against those bytes.
#
# That is the whole cross-image contract in one claim. The rule's text starts
# as a SkillCard, is staged by the controller, assembled into
# /opt/skills/<frontmatter-name>/SKILL.md by the skill-loader out of the
# controller's image, written into .konveyor-skills.json under rules[], read
# back by the harness out of the agent's image, composed by prompt.Build and
# handed to goose over ACP. If any link is wrong the marker cannot appear.
#
#   rule attached     the marker appears, under the ## Rules heading
#   rule detached     it does not, so the marker came from the rule and not
#                     from the task
#
# Both runs are expected to succeed. That matters: if the control's expected
# outcome were a failed run, it would pass for free on any broken cluster.
#
# Prerequisites: hack/start-kind.sh, hack/setup-e2e.sh and
# hack/install-konveyor.sh, plus an agent-base image built and loaded under the
# tag AGENT_IMAGE names below -- which is not the Makefile's default:
#
#   make agent-base-build AGENT_BASE_IMG=quay.io/konveyor/agent-base:e2e
#   kind load docker-image quay.io/konveyor/agent-base:e2e --name agentic-controller-e2e
#
# Environment:
#   E2E_TIMEOUT   how long to wait on any one condition (default 300s)
#   AGENT_IMAGE     image carrying the harness (default agent-base:e2e)
#   EMULATOR_IMAGE  llemulator image (default openai-emulator:e2e)
#   APP_REPO        repository the Hub application points at (default coolstore)
#   KONVEYOR_NS     namespace Konveyor is installed in (default konveyor-tackle)
#   NS            namespace to run in (default: a fresh one per run)

set -euo pipefail

E2E_TIMEOUT="${E2E_TIMEOUT:-300s}"
DEADLINE="${E2E_TIMEOUT%s}"
AGENT_IMAGE="${AGENT_IMAGE:-quay.io/konveyor/agent-base:e2e}"
# Built and loaded by hack/start-kind.sh from a pinned llemulator ref.
EMULATOR_IMAGE="${EMULATOR_IMAGE:-docker.io/library/openai-emulator:e2e}"
# The application the harness resolves and clones. Nothing is pushed: the run
# changes no files, so HEAD stays at the base commit and the push is skipped.
APP_REPO="${APP_REPO:-https://github.com/konveyor-ecosystem/coolstore}"
# Hub is installed by hack/install-konveyor.sh, from tackle2-operator's own
# Helm chart. Nothing about its deployment is defined in this repo.
KONVEYOR_NS="${KONVEYOR_NS:-konveyor-tackle}"
# Through the UI rather than straight at tackle-hub. The operator denies all
# ingress to its namespace except to the UI, which proxies the Hub API under
# /hub, so this is the way in from anywhere else. Going at the Hub Service
# directly works only where nothing enforces NetworkPolicy, which is why the
# probe this replaces passed on minikube and would not have here.
HUB_BASE_URL="http://tackle-ui.${KONVEYOR_NS}.svc:8080/hub"
NS="${NS:-rule-e2e-$$}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Regenerated per invocation, so a line left in a container log by an earlier
# run can never satisfy this one.
MARKER="RULE-MARKER-$$-${RANDOM}"
RUN_ON="run-on-$$-${RANDOM}"
RUN_OFF="run-off-$$-${RANDOM}"
# The script is keyed by bearer token, and this is the token goose will send,
# because it is what the Gateway credential holds.
TOKEN="rule-e2e-token-$$"
# Hub requires application names to be unique, and it outlives the run's
# namespace, so a fixed name makes the second run on a cluster a 409.
APP_NAME="rule-e2e-app-$$-${RANDOM}"

PASS=0
FAIL=0
pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }
kc() { kubectl -n "${NS}" "$@"; }

kubectl create namespace "${NS}" >/dev/null
trap 'kubectl delete namespace "${NS}" --wait=false >/dev/null 2>&1 || true' EXIT

echo "=== E2E Test: A Rule Reaches The Model ==="
echo "  namespace ${NS}"
echo ""

await() {
    local until=$(( SECONDS + DEADLINE ))
    until "$@"; do
        [ "${SECONDS}" -lt "${until}" ] || return 1
        sleep 3
    done
}

ready()        { [ "$(kc get "$1" "$2" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = True ]; }
available()    { kc get deploy "$1" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null | grep -q True; }
phase()        { kc get agentrun "$1" -o jsonpath='{.status.phase}' 2>/dev/null; }
finished()     { case "$(phase "$1")" in Succeeded|Failed) return 0;; *) return 1;; esac; }
run_pod()      { kc get agentrun "$1" -o jsonpath='{.status.sandboxName}' 2>/dev/null; }

# POST from inside the cluster. The emulator image is alpine, so busybox wget
# is already there and exits non-zero on a non-2xx, which makes a rejected
# script fail here rather than three steps later.
emul_post() {
    kc exec -i deploy/openai-emulator-debug -- sh -c \
      "wget -q -O- --header='Authorization: Bearer ${TOKEN}' \
            --header='Content-Type: application/json' \
            --post-data=\"\$(cat)\" http://127.0.0.1:8080$1"
}
hub_post() {
    kc exec -i deploy/openai-emulator-debug -- sh -c \
      "wget -q -O- --header='Content-Type: application/json' \
            --post-data=\"\$(cat)\" ${HUB_BASE_URL}$1"
}
hub_get() {
    kc exec -i deploy/openai-emulator-debug -- \
      wget -q -O- "${HUB_BASE_URL}$1"
}

# One always-matching, unlimited rule. Every model call succeeds, so the run
# completes normally and the phase stays a precondition rather than becoming
# the assertion. times: -1 is load-bearing: a rule that omits it is treated as
# exhausted and skipped, and every request would 500.
# The first rule stands in for a model that obeys the rule: it answers with the
# shell call the rule demands, but only when the rule's marker is actually in
# the prompt. With the rule detached the marker is absent, nothing matches it,
# and the catch-all answers instead. So the tool call is caused by the rule
# having arrived, which is the claim.
#
# It is keyed on <turn-context> as well, because goose separately asks the
# model to name the session and that request embeds the user message. A
# tool-call rule fires once, so the title request would otherwise consume it.
#
# times: -1 on the catch-all is load-bearing: a rule that omits times is
# treated as exhausted and skipped, and every later request would 500.
script_json() {
    cat <<JSON
{"reset": true, "models": ["gpt-4o"],
 "rules": [
   {"pattern": "(?s)<turn-context>.*${MARKER}",
    "tool_calls": [{"id": "call_rule_1", "type": "function",
                    "function": {"name": "shell",
                                 "arguments": "{\"command\": \"echo ${MARKER}\"}"}}]},
   {"response": "Acknowledged. Nothing to change.", "times": -1}]}
JSON
}

echo "--- Deploying the emulator ---"
sed "s|EMULATOR_IMAGE_PLACEHOLDER|${EMULATOR_IMAGE}|" "${SCRIPT_DIR}/e2e/rule/emulator.yaml" | kc apply -f - >/dev/null

for d in openai-emulator-debug; do
    await available "${d}" || { fail "deployment ${d} never became Available"; kc describe deploy "${d}" | tail -20; }
done

echo "--- Seeding Hub ---"
# Hub answers GET before its migrations have finished, and a create fails with
# "no such table: APPLICATIONTAG" until they do, so readiness is measured by
# the operation actually needed rather than by the Deployment's condition.
#
# The first id in the reply is the application's; later ones belong to nested
# objects like its bucket, so take the first rather than matching greedily.
create_app() {
    APP_ID=$(hub_post /applications <<JSON | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2
{"name": "${APP_NAME}",
 "repository": {"kind": "git", "url": "${APP_REPO}", "branch": "main"}}
JSON
    )
    [ -n "${APP_ID}" ]
}

if await create_app && hub_get "/applications/${APP_ID}" >/dev/null 2>&1; then
    pass "Hub is serving and application ${APP_ID} reads back"
else
    fail "Hub preflight failed, so nothing below can mean anything"
    kubectl -n "${KONVEYOR_NS}" logs deployment/tackle-hub --tail=30 || true
    exit 1
fi

echo "--- Scripting the emulator ---"
script_json | emul_post /_emulator/script >/dev/null && pass "script loaded" || fail "emulator rejected the script"

echo "--- Creating the gateway, cards and agents ---"
kc apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Secret
metadata: {name: rule-e2e-creds}
stringData: {API_KEY: "${TOKEN}"}
---
apiVersion: konveyor.io/v1alpha1
kind: Gateway
metadata: {name: rule-e2e-gw}
spec:
  provider: openai
  # Port 80, and no /v1: the Gateway endpoint is a base URL.
  endpoint: http://openai-emulator-debug.${NS}.svc
  # Not a gpt-5 or o-series name. Those switch goose to the Responses API,
  # whose payload has input rather than messages, and this test reads messages.
  model: {name: gpt-4o, contextWindow: 128000}
  credentialRef: {secretName: rule-e2e-creds, key: API_KEY}
---
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata: {name: rule-e2e-rule}
spec:
  type: rule
  inline: |
    ---
    name: rule-e2e-house-rules
    description: house rules that apply to every task in this repository
    ---

    # House rules

    Before doing anything else, you MUST run this exact shell command:

        echo ${MARKER}
---
# Attached to both agents, so the control differs by the rule alone rather
# than by having no skills at all.
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata: {name: rule-e2e-companion}
spec:
  type: skill
  inline: |
    ---
    name: rule-e2e-companion
    description: a skill that is present in both runs and says nothing about the marker
    ---

    # Companion

    Nothing to do here.
---
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata: {name: rule-e2e-on}
spec:
  image: ${AGENT_IMAGE}
  prompt: "You are a migration agent."
  gateways: [{ref: rule-e2e-gw}]
  skillCards: [{ref: rule-e2e-rule}, {ref: rule-e2e-companion}]
---
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata: {name: rule-e2e-off}
spec:
  image: ${AGENT_IMAGE}
  prompt: "You are a migration agent."
  gateways: [{ref: rule-e2e-gw}]
  skillCards: [{ref: rule-e2e-companion}]
EOF

await ready gateway rule-e2e-gw || fail "gateway never became Ready"
await ready agent rule-e2e-on  || fail "agent rule-e2e-on never became Ready"
await ready agent rule-e2e-off || fail "agent rule-e2e-off never became Ready"

# start_run <agentrun name> <agent> <run id>
start_run() {
    # Re-scripted immediately before each run: an emulator restart between
    # setup and here would empty the session silently, and reset makes the
    # repeat replace rather than append.
    script_json | emul_post /_emulator/script >/dev/null
    kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: AgentRun
metadata: {name: $1}
spec:
  agentRef: $2
  gateway: rule-e2e-gw
  instructions: "Reply with one short sentence. Run marker: $3"
  env:
  - {name: HUB_BASE_URL,  value: "${HUB_BASE_URL}"}
  - {name: APP_ID,        value: "${APP_ID}"}
  - {name: TARGET_BRANCH, value: "rule-e2e-target"}
EOF
}

echo "--- Running with the rule attached, and without ---"
start_run rule-on  rule-e2e-on  "${RUN_ON}"
start_run rule-off rule-e2e-off "${RUN_OFF}"
await finished rule-on  || fail "run rule-on never finished"
await finished rule-off || fail "run rule-off never finished"

# Recorded request bodies, one per line. Newlines are stripped first: a body
# larger than the CRI's 16KB line limit is split across lines, and Go's encoder
# never puts a raw newline inside one, so removing them is lossless.
recorded_bodies() {
    kc logs deployment/openai-emulator-debug --tail=-1 2>/dev/null \
      | tr -d '\n' \
      | awk '{ n = split($0, p, /\[DEBUG\] Body: /)
               for (i = 2; i <= n; i++) { sub(/\[DEBUG\] Request:.*/, "", p[i]); print p[i] } }'
}

# The user message of the turn belonging to run $1. goose also asks the model
# to name the session, and that request embeds the user message, so it carries
# the run id too. It has no rules layer, so select on the turn by requiring the
# turn-context wrapper that only the real turn has.
user_message_for() {
    recorded_bodies | jq -r --arg id "$1" '
        select(type == "object" and has("messages"))
        | [ .messages[] | select(.role == "user") | .content | select(type == "string") ] as $u
        | select(any($u[]; contains($id)))
        | select(any($u[]; contains("<turn-context>")))
        | $u[]' 2>/dev/null
}

shape_report() {
    recorded_bodies | jq -r '
        if type == "object" then
          "keys=\(keys | join(",")) roles=\([.messages[]?.role] | join(",")) content=\([.messages[]?.content | type] | join(","))"
        else "unparseable" end' 2>/dev/null | sort -u
}

# assert_marker <run id> <present|absent> <label>
assert_marker() {
    # `|| true`, because jq exits non-zero on a body it cannot parse and
    # pipefail would then kill the script here, before the branch below that
    # exists to report exactly that.
    local body; body="$(user_message_for "$1" || true)"
    if [ -z "${body}" ]; then
        fail "$3: no turn carrying $1 reached the model endpoint"
        echo "    recorded bodies: $(recorded_bodies | wc -l | tr -d ' ')"
        shape_report | sed 's/^/    /'
        return
    fi

    local rules_ln marker_ln
    rules_ln=$(printf '%s\n' "${body}" | grep -n '^## Rules$' | head -1 | cut -d: -f1 || true)
    marker_ln=$(printf '%s\n' "${body}" | grep -n "${MARKER}" | head -1 | cut -d: -f1 || true)

    case "$2" in
    present)
        if [ -z "${marker_ln}" ]; then
            fail "$3: the rule body never reached the prompt goose sent"
        elif [ -n "${rules_ln}" ] && [ "${marker_ln}" -gt "${rules_ln}" ]; then
            pass "$3: the rule arrived under ## Rules in the prompt goose sent"
        else
            fail "$3: marker present but not under the ## Rules layer"
        fi
        ;;
    absent)
        if [ -n "${marker_ln}" ]; then
            fail "$3: control is invalid, the marker appeared with the rule detached"
        else
            pass "$3: no marker, so the marker above was caused by the rule"
        fi
        ;;
    esac
}

echo ""
echo "--- Preconditions ---"
[ "$(phase rule-on)" = Succeeded ] && pass "run rule-on Succeeded" || fail "run rule-on is $(phase rule-on)"
[ "$(phase rule-off)" = Succeeded ] && pass "run rule-off Succeeded" || fail "run rule-off is $(phase rule-off)"
restarts=$(kc get pod -l app=openai-emulator-debug -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}' 2>/dev/null || true)
[ "${restarts:-0}" = 0 ] && pass "emulator never restarted, so the script stayed loaded" \
                         || fail "emulator restarted ${restarts} times, so the script was silently emptied"

echo "--- The claim ---"
assert_marker "${RUN_ON}"  present "with the rule"
assert_marker "${RUN_OFF}" absent  "without the rule"

if kc logs "$(run_pod rule-on)" -c agent 2>/dev/null | grep -q "always-loaded rules:.*rule-e2e-house-rules"; then
    pass "the harness reported the rule as always-loaded"
else
    fail "the harness never reported the rule, so it did not read it back from the manifest"
fi

# The rule asked for a command. Did one actually get called? The harness logs
# each tool call's title, which for a shell call is the command, so this is the
# marker arriving back out through goose and ACP rather than only going in.
tool_call_logged() { kc logs "$(run_pod "$1")" -c agent 2>/dev/null | grep -q "tool:.*${MARKER}"; }

if tool_call_logged rule-on; then
    pass "with the rule: the agent was asked to run the command the rule demanded"
else
    fail "with the rule: no tool call carrying the marker reached the harness"
    kc logs "$(run_pod rule-on)" -c agent 2>/dev/null | grep -E "tool:|error" | tail -5 | sed 's/^/    /' || true
fi
if tool_call_logged rule-off; then
    fail "control is invalid: a tool call carrying the marker happened without the rule"
else
    pass "without the rule: no tool call, so the rule caused it"
fi

# Informational, not an assertion: which tools goose offered the model. The
# emulator can only return a tool call naming one of these.
echo ""
echo "--- Tools goose advertised (informational) ---"
recorded_bodies | jq -r 'select(type == "object") | [.tools[]?.function.name] | join(", ")' 2>/dev/null \
    | grep -v '^$' | sort -u | sed 's/^/    /' || echo "    (none)"

echo ""
echo "=== Results ==="
echo "  Passed: ${PASS}"
echo "  Failed: ${FAIL}"
if [ "${FAIL}" -gt 0 ]; then
    echo ""
    echo "--- Debug info ---"
    kc get agentruns,agents,skillcards,gateways -o wide 2>/dev/null || true
    for r in rule-on rule-off; do
        pod="$(run_pod "${r}")"
        [ -n "${pod}" ] || continue
        echo "--- ${r} (${pod}) phase=$(phase "${r}")"
        # Events, not just logs: a container that never started has no logs,
        # and the reason it did not (ErrImagePull, scheduling) lives here.
        kc describe pod "${pod}" 2>/dev/null | sed -n '/^Events:/,$p' | sed 's/^/    /' || true
        kc logs "${pod}" -c skill-loader 2>&1 | tail -15 | sed 's/^/    /' || true
        kc logs "${pod}" -c agent 2>&1 | tail -40 | sed 's/^/    /' || true
    done
    kubectl -n "${KONVEYOR_NS}" logs deployment/tackle-hub --tail=20 2>&1 | sed 's/^/    hub: /' || true
    exit 1
fi
echo ""
echo "E2E PASSED: the rule reached the model."
