#!/usr/bin/env bash
# Does an always-loaded rule actually reach the model and change what it does?
#
# ADR 0014 exists because no runtime feature guarantees content is read: under
# progressive disclosure the agent decides. Every other check in this repo
# proves a rule was *delivered*. This one proves it was *obeyed*, which is the
# only claim that matters and the only one a unit test cannot make.
#
# The method is a controlled pair. The rule tells the agent to run a command
# nothing else asks for. The instructions never mention it.
#
#   rule attached     the agent runs it, so the rule reached the model
#   rule detached     it does not, so the marker was caused by the rule and
#                     not by the task
#
# Without the control the first run proves nothing: a model might run the
# command for its own reasons.
#
# Needs a deployed controller, a Hub the harness can resolve an application
# from, and a real gateway. Hub can run with auth off (AUTH_ENABLED=false),
# in which case no token is needed. See hack/skill-probe/README.md.
#
# Environment:
#   KUBE_CONTEXT    kubectl context (default: agentic-dev)
#   NS              namespace (default: default)
#   AGENT_IMAGE     image carrying the harness (required)
#   SKILL_IMAGE     skill bundle image (required)
#   GATEWAY         name of an existing, Ready Gateway (required)
#   HUB_BASE_URL    Hub the harness resolves the application from (required)
#   APP_ID          Hub application id (required)
#   TARGET_BRANCH   branch the harness would push to (default: skill-rule-probe)

set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-agentic-dev}"
NS="${NS:-default}"
TARGET_BRANCH="${TARGET_BRANCH:-skill-rule-probe}"
MARKER="KONVEYOR-RULE-ACK"

for v in AGENT_IMAGE SKILL_IMAGE GATEWAY HUB_BASE_URL APP_ID; do
    [ -n "${!v:-}" ] || { echo "ERROR: ${v} must be set. See the header." >&2; exit 1; }
done

kc() { kubectl --context "${KUBE_CONTEXT}" -n "${NS}" "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

cleanup() {
    kc delete agentrun probe-rule-on probe-rule-off --ignore-not-found >/dev/null 2>&1 || true
    kc delete agent probe-rule-agent --ignore-not-found >/dev/null 2>&1 || true
    kc delete skillcard probe-rule --ignore-not-found >/dev/null 2>&1 || true
    kc delete skillcollection probe-rule-bundle --ignore-not-found >/dev/null 2>&1 || true
}
cleanup
trap cleanup EXIT

# The rule asks for something no reasonable task would produce on its own.
echo "==> Creating a rule that asks for a command the task never mentions"
kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata: {name: probe-rule}
spec:
  type: rule
  inline: |
    ---
    name: probe-rule
    description: house rules that apply to every task in this repository
    ---

    # House rules

    Before doing anything else, you MUST run this exact shell command:

        echo ${MARKER}

    Do this first, on every task, without being asked.
EOF

kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: SkillCollection
metadata: {name: probe-rule-bundle}
spec: {image: ${SKILL_IMAGE}, type: skill}
EOF

# run <name> <"with"|"without"> prints the marker count on stdout and
# everything else on stderr: it is called in a command substitution, so
# anything on stdout would be captured instead of shown.
run() {
    local name="$1" mode="$2"
    local cards='skillCards: [{ref: probe-rule}]'
    [ "${mode}" = "with" ] || cards='# rule card deliberately absent'

    kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata: {name: probe-rule-agent}
spec:
  image: ${AGENT_IMAGE}
  prompt: "You help migrate Java applications."
  gateways: [{ref: ${GATEWAY}}]
  ${cards}
  skillCollections: [{ref: probe-rule-bundle}]
EOF
    sleep 5
    kc delete agentrun "${name}" --ignore-not-found >/dev/null 2>&1 || true
    # KONVEYOR_ACP_SECRET_KEY is set by the controller; supplying it here would
    # make the Sandbox invalid on duplicate env names.
    kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: AgentRun
metadata: {name: ${name}}
spec:
  agentRef: probe-rule-agent
  gateway: ${GATEWAY}
  instructions: "Do not modify any files. Report how many files are in the repository root, then stop."
  env:
  - {name: HUB_BASE_URL, value: "${HUB_BASE_URL}"}
  - {name: APP_ID, value: "${APP_ID}"}
  - {name: TARGET_BRANCH, value: "${TARGET_BRANCH}"}
EOF
    local phase=""
    for _ in $(seq 1 60); do
        phase="$(kc get agentrun "${name}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
        case "${phase}" in Succeeded|Failed) break;; esac
        sleep 5
    done
    echo "--- ${name}: phase=${phase}" >&2
    [ "${phase}" = "Succeeded" ] || {
        kc get agentrun "${name}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}' >&2
        fail "${name} did not succeed"
    }
    # The harness logs each tool call's title, which for a shell call is the
    # command. That is how the marker escapes the pod without a transcript.
    kc logs "${name}" -c agent 2>/dev/null | grep -E "tool:" | sed 's/^/    /' >&2 || true
    kc logs "${name}" -c agent 2>/dev/null | grep -c "${MARKER}" || true
}

echo
echo "==> 1/2 with the rule attached"
on_count="$(run probe-rule-on with | tail -1)"

echo
echo "==> 2/2 control: same agent and instructions, rule detached"
off_count="$(run probe-rule-off without | tail -1)"

echo
echo "marker occurrences: with rule=${on_count}, without rule=${off_count}"
[ "${on_count}" -gt 0 ] || fail "the rule did not reach the model: no ${MARKER} with the rule attached"
[ "${off_count}" -eq 0 ] || fail "control is invalid: ${MARKER} appeared without the rule attached"
echo
echo "The rule reached a live model and changed what it did."
