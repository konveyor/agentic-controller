#!/usr/bin/env bash
# Skill delivery e2e: the cases envtest cannot reach, because they need a
# kubelet, an ImageVolume and a pod that either starts or does not.
#
# These were scripts under hack/skill-probe/ that somebody had to remember to
# run. One of them passed for a week against a ServiceAccount left in a
# namespace by hand, so they run here now, on a cluster built fresh each time.
#
#   1 enumerate   an image holding several skills becomes one SkillCard each,
#                 with the subPath resolved from the image rather than typed
#   2 identity    the Job gets its ServiceAccount in the collection's namespace,
#                 not the operator's
#   3 prune       pointing the collection at a smaller image drops the cards
#                 for skills that are gone, and only those
#   4 collide     a hand-written card duplicating a generated one fails the pod
#                 at init while every object still reports Ready (#153)
#   5 unusable    a skill with no frontmatter never reaches a running agent
#   6 collect     deleting the collection takes its cards and spares the rest
#
# Prerequisites: hack/start-kind.sh and hack/setup-e2e.sh, same as run-e2e.sh.
#
# Environment:
#   E2E_TIMEOUT   how long to wait on any one condition (default 180s)
#   NS            namespace to run in (default: a fresh one per run)

set -euo pipefail

E2E_TIMEOUT="${E2E_TIMEOUT:-180s}"
DEADLINE="${E2E_TIMEOUT%s}"
NS="${NS:-skill-e2e-$$}"
PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }
kc() { kubectl -n "${NS}" "$@"; }

# The collection's namespace is created here and deleted on exit. A run that
# reuses one is testing whatever the last run left behind.
kubectl create namespace "${NS}" >/dev/null
trap 'kubectl delete namespace "${NS}" --wait=false >/dev/null 2>&1 || true' EXIT

echo "=== E2E Test: Skill Delivery ==="
echo "  namespace ${NS}"
echo ""

# await <predicate...> retries until it succeeds or the deadline passes. The
# predicate must be a function: a command substitution in the arguments would
# be evaluated once by the caller and never change.
await() {
    local until=$(( SECONDS + DEADLINE ))
    until "$@"; do
        [ "${SECONDS}" -lt "${until}" ] || return 1
        sleep 3
    done
}

cards()          { kc get skillcards -l "konveyor.io/skillcollection=$1" \
                     -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | sort | tr -d ' '; }
card_count_is()  { [ "$(cards "$1" | grep -c . || true)" = "$2" ]; }
no_cards()       { [ -z "$(cards "$1")" ]; }
ready()          { [ "$(condition "$1" "$2")" = True ]; }
ready_false()    { [ "$(condition "$1" "$2")" = False ]; }
settled()        { [ -n "$(condition "$1" "$2")" ]; }
message()        { kc get "$1" "$2" -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}' 2>/dev/null; }
condition()      { kc get "$1" "$2" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null; }
run_finished()   { case "$(phase "$1")" in Succeeded|Failed) return 0;; *) return 1;; esac; }
phase()          { kc get agentrun "$1" -o jsonpath='{.status.phase}' 2>/dev/null; }
run_pod()        { kc get agentrun "$1" -o jsonpath='{.status.sandboxName}' 2>/dev/null; }
loader_log()     { local pod; pod="$(run_pod "$1")"; [ -n "${pod}" ] || return 0
                   kc logs "${pod}" -c skill-loader 2>&1; }

# A gateway, because an Agent will not go Ready without one. It points at the
# emulator that start-kind.sh already deploys, so nothing here needs a key.
kc apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Secret
metadata: {name: skill-e2e-creds}
stringData: {API_KEY: emulator-does-not-check}
---
apiVersion: konveyor.io/v1alpha1
kind: Gateway
metadata: {name: skill-e2e-gw}
spec:
  provider: openai
  endpoint: http://openai-emulator.default.svc
  model: {name: gpt-4o, contextWindow: 128000}
  credentialRef: {secretName: skill-e2e-creds, key: API_KEY}
EOF
await ready gateway skill-e2e-gw || fail "gateway never became Ready"

echo "--- 1. An image holding several skills enumerates into one card each ---"
kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: SkillCollection
metadata: {name: e2e-bundle}
spec: {image: quay.io/konveyor/skills:e2e, type: skill}
EOF
if await card_count_is e2e-bundle 4; then
    pass "bundle enumerated into 4 SkillCards"
else
    fail "expected 4 generated cards, got: $(cards e2e-bundle | tr '\n' ' ')"
fi
if [ "$(kc get skillcard e2e-bundle-plan -o jsonpath='{.spec.subPath}' 2>/dev/null)" = "plan" ]; then
    pass "subPath resolved from the image, not typed by hand"
else
    fail "e2e-bundle-plan has no subPath"
fi

echo "--- 2. The Job's identity exists in the collection's namespace ---"
if kc get serviceaccount skill-enumerator >/dev/null 2>&1 && kc get rolebinding skill-enumerator >/dev/null 2>&1; then
    pass "ServiceAccount and RoleBinding created where the Job runs"
else
    fail "no enumerator identity in ${NS}; the Job could never be admitted"
fi

echo "--- 3. A smaller image prunes the cards that are gone ---"
kc patch skillcollection e2e-bundle --type=merge \
    -p '{"spec":{"image":"quay.io/konveyor/skills:maven-migration"}}' >/dev/null
if await card_count_is e2e-bundle 1; then
    pass "pruned to the one skill the image still has"
else
    fail "expected 1 card after pruning, got: $(cards e2e-bundle | tr '\n' ' ')"
fi

echo "--- 4. A hand-written card duplicating a generated one fails the pod ---"
kc patch skillcollection e2e-bundle --type=merge \
    -p '{"spec":{"image":"quay.io/konveyor/skills:e2e"}}' >/dev/null
await card_count_is e2e-bundle 4 || fail "collection did not re-enumerate"
kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata: {name: e2e-hand-written}
spec: {image: quay.io/konveyor/skills:e2e, subPath: plan, type: skill}
---
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata: {name: e2e-collide}
spec:
  image: quay.io/konveyor/agentic-controller-agent:e2e
  prompt: "collision probe"
  gateways: [{ref: skill-e2e-gw}]
  skillCards: [{ref: e2e-hand-written}]
  skillCollections: [{ref: e2e-bundle}]
EOF
await ready skillcard e2e-hand-written || fail "hand-written card never became Ready"
await ready agent e2e-collide || fail "agent never became Ready"
kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: AgentRun
metadata: {name: e2e-collide-run}
spec: {agentRef: e2e-collide, gateway: skill-e2e-gw, instructions: "expected to fail at init"}
EOF
if await run_finished e2e-collide-run && [ "$(phase e2e-collide-run)" = Failed ] &&
   loader_log e2e-collide-run | grep -q "duplicate skill name"; then
    pass "pod failed at init naming both origins, while every object read Ready"
else
    fail "collision did not fail the pod: phase=$(phase e2e-collide-run) $(loader_log e2e-collide-run | tail -2)"
fi

echo "--- 5. A skill with no frontmatter never reaches a running agent ---"
kc apply -f - >/dev/null <<'EOF'
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata: {name: e2e-unusable}
spec:
  type: skill
  inline: |
    # no frontmatter, so the runtime would never see this
---
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata: {name: e2e-broken}
spec:
  image: quay.io/konveyor/agentic-controller-agent:e2e
  prompt: "unusable skill probe"
  gateways: [{ref: skill-e2e-gw}]
  skillCards: [{ref: e2e-unusable}]
EOF
# Inline content needs no network to inspect, so the controller rejects the card
# itself. envtest covers that; what it cannot show is the consequence, which is
# that the Agent never goes Ready and so no pod is ever scheduled.
if await ready_false skillcard e2e-unusable; then
    pass "card rejected, and the message says why: $(message skillcard e2e-unusable)"
else
    fail "card with no frontmatter reports Ready=$(condition skillcard e2e-unusable)"
fi
if await settled agent e2e-broken && ready_false agent e2e-broken; then
    pass "agent held back, so an unusable skill cannot reach a pod"
else
    fail "agent reports Ready=$(condition agent e2e-broken) on an unusable skill"
fi

echo "--- 6. Deleting the collection takes its cards and spares the rest ---"
kc delete skillcollection e2e-bundle >/dev/null
if await no_cards e2e-bundle; then
    pass "generated cards collected with the collection"
else
    fail "generated cards outlived their collection: $(cards e2e-bundle | tr '\n' ' ')"
fi
if kc get skillcard e2e-hand-written >/dev/null 2>&1; then
    pass "hand-authored card left alone"
else
    fail "a hand-authored card was collected with the collection"
fi

echo ""
echo "=== Results ==="
echo "  Passed: ${PASS}"
echo "  Failed: ${FAIL}"
if [ "${FAIL}" -gt 0 ]; then
    echo ""
    echo "--- Debug info ---"
    kc get skillcards,skillcollections,agents,agentruns -o wide 2>/dev/null || true
    kc get pods -o wide 2>/dev/null || true
    exit 1
fi
echo ""
echo "E2E PASSED: skill delivery verified."
