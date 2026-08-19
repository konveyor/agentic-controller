#!/usr/bin/env bash
# Exercise the controller half of skill delivery on any cluster: enumeration,
# pruning, ownership and the collision a hand-written card can cause.
#
# run-probe.sh covers the pod shape and needs no controller. This one needs the
# controller deployed, because everything it checks is reconciler behaviour:
#
#   1 enumerate    a SkillCollection image becomes one SkillCard per skill,
#                  each with the subPath it was found at
#   2 prune        pointing the collection at a smaller image drops the cards
#                  for skills that are gone, and only those
#   3 collide      a hand-written card for a skill the collection also
#                  generates fails the pod at init, while every object still
#                  reports Ready. This is the failure discussed in #153
#   4 collect      deleting the collection garbage collects its cards and
#                  leaves hand-authored ones alone
#
# The cluster needs the ImageVolume feature gate and a deployed controller:
#
#   make deploy IMG=<your image>
#   kubectl -n agentic-controller-system set env deploy/agentic-controller-controller-manager \
#       ENUMERATION_IMAGE=<agent-base image carrying the harness>
#
# Environment:
#   KUBE_CONTEXT      kubectl context (default: agentic-dev)
#   CONTAINER_TOOL    podman or docker (default: podman)
#   PLATFORM          image platform (default: linux/arm64)
#   NS                namespace to work in (default: default)
#   AGENT_IMAGE       any agent image, for the collision probe
#
# Probe 3 needs an Agent, so it needs an image and a gateway. Set both or it is
# skipped, and the script says so rather than quietly covering less.

set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-agentic-dev}"
CONTAINER_TOOL="${CONTAINER_TOOL:-podman}"
PLATFORM="${PLATFORM:-linux/arm64}"
# A run gets its own namespace, created here and deleted on exit. Reusing one
# is how a probe comes to pass on leftovers: an earlier hand-made
# ServiceAccount made the enumeration probe pass while the code that should
# have created it was missing entirely. A namespace nothing else has touched
# is the only way the result means what it says.
NS="${NS:-skill-probe-$(date +%s)-$$}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

kc() { kubectl --context "${KUBE_CONTEXT}" -n "${NS}" "$@"; }
kubectl --context "${KUBE_CONTEXT}" create namespace "${NS}" >/dev/null
echo "==> namespace ${NS}"

fail() { echo "FAIL: $*" >&2; exit 1; }

# cards <collection> lists the card names a collection owns, sorted.
cards() {
    kc get skillcards -l "konveyor.io/skillcollection=$1" \
        -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | sort | tr -d ' '
}

# await <seconds> <predicate...> retries until the predicate succeeds. The
# predicate must be a function: anything expanded here, including a command
# substitution, is evaluated once by the caller and would never change.
await() {
    local deadline=$(( SECONDS + $1 )); shift
    until "$@"; do
        [ "${SECONDS}" -lt "${deadline}" ] || return 1
        sleep 3
    done
}

card_count_is() { [ "$(cards "$1" | grep -c . || true)" = "$2" ]; }
no_cards()      { [ -z "$(cards "$1")" ]; }
gateway_ready() {
    [ "$(kc get gateway "$1" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = True ]
}
card_ready()    {
    [ "$(kc get skillcard "$1" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = True ]
}
agent_ready()   {
    [ "$(kc get agent "$1" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = True ]
}
run_finished()  {
    case "$(kc get agentrun "$1" -o jsonpath='{.status.phase}' 2>/dev/null)" in
        Succeeded|Failed) return 0;; *) return 1;;
    esac
}

echo "==> Building a four-skill bundle and a one-skill bundle"
"${CONTAINER_TOOL}" build --platform "${PLATFORM}" -q \
    -t localhost/konveyor-skills:probe-full \
    -f "${REPO_ROOT}/skills/Containerfile" "${REPO_ROOT}/skills" >/dev/null

printf 'FROM scratch\nCOPY plan/ /plan/\n' >"${WORK}/small.Containerfile"
"${CONTAINER_TOOL}" build --platform "${PLATFORM}" -q \
    -t localhost/konveyor-skills:probe-small \
    -f "${WORK}/small.Containerfile" "${REPO_ROOT}/skills" >/dev/null

echo "==> Loading both into the node image store"
for tag in probe-full probe-small; do
    save_format=()
    if [ "${CONTAINER_TOOL}" = "podman" ]; then save_format=(--format docker-archive); fi
    "${CONTAINER_TOOL}" save "${save_format[@]}" \
        -o "${WORK}/${tag}.tar" "localhost/konveyor-skills:${tag}"
    minikube image load -p "${KUBE_CONTEXT}" "${WORK}/${tag}.tar"
done

cleanup_objects() {
    kubectl --context "${KUBE_CONTEXT}" delete namespace "${NS}" --wait=false >/dev/null 2>&1 || true
    kc delete skillcollection probe-bundle --ignore-not-found >/dev/null 2>&1 || true
    kc delete skillcard probe-hand-written --ignore-not-found >/dev/null 2>&1 || true
    kc delete agentrun probe-collision --ignore-not-found >/dev/null 2>&1 || true
    kc delete agent probe-agent --ignore-not-found >/dev/null 2>&1 || true
}
# Nothing to clear up front: the namespace was created moments ago. Calling the
# cleanup here would delete it and everything after would fail on a terminating
# namespace.
trap 'cleanup_objects; rm -rf "${WORK}"' EXIT

echo
echo "==> 1/4 an image bundle enumerates into one SkillCard per skill"
kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: SkillCollection
metadata: {name: probe-bundle}
spec: {image: localhost/konveyor-skills:probe-full, type: skill}
EOF
await 180 card_count_is probe-bundle 4 \
    || fail "expected 4 generated cards, got: $(cards probe-bundle | tr '\n' ' ')"
echo "--- cards: $(cards probe-bundle | tr '\n' ' ')"
for want in probe-bundle-execute probe-bundle-javaee-to-quarkus probe-bundle-plan probe-bundle-verify; do
    cards probe-bundle | grep -qx "${want}" || fail "missing generated card ${want}"
done
# The subPath is what the card knows that a human would otherwise have to type.
sub="$(kc get skillcard probe-bundle-plan -o jsonpath='{.spec.subPath}')"
[ "${sub}" = "plan" ] || fail "probe-bundle-plan has subPath '${sub}', want 'plan'"
echo "    subPath resolved from the image, not typed by hand"

echo
echo "==> 2/4 switching to a smaller image prunes the cards that are gone"
kc patch skillcollection probe-bundle --type=merge \
    -p '{"spec":{"image":"localhost/konveyor-skills:probe-small"}}' >/dev/null
await 180 card_count_is probe-bundle 1 \
    || fail "expected 1 card after pruning, got: $(cards probe-bundle | tr '\n' ' ')"
cards probe-bundle | grep -qx probe-bundle-plan || fail "pruning kept the wrong card"
echo "--- cards: $(cards probe-bundle | tr '\n' ' ')"

echo
echo "==> 3/4 a hand-written card duplicating a generated one fails the pod"
if [ -z "${AGENT_IMAGE:-}" ]; then
    echo "--- SKIPPED: set AGENT_IMAGE to run this probe"
    SKIPPED_COLLISION=1
fi
if [ -z "${SKIPPED_COLLISION:-}" ]; then
# An Agent needs a Ready Gateway, and this namespace is new, so make one. The
# key is never used: verification only checks the endpoint answers.
GATEWAY=probe-gw
kc apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Secret
metadata: {name: probe-gw-creds}
stringData: {API_KEY: unused-by-this-probe}
---
apiVersion: konveyor.io/v1alpha1
kind: Gateway
metadata: {name: ${GATEWAY}}
spec:
  provider: anthropic
  endpoint: https://api.anthropic.com
  model: {name: claude-sonnet-5, contextWindow: 200000}
  credentialRef: {secretName: probe-gw-creds, key: API_KEY}
EOF
await 120 gateway_ready "${GATEWAY}" || fail "gateway never became Ready"
kc patch skillcollection probe-bundle --type=merge \
    -p '{"spec":{"image":"localhost/konveyor-skills:probe-full"}}' >/dev/null
await 180 card_count_is probe-bundle 4 || fail "collection did not re-enumerate"
# Same image, same subPath, different card name: two cards, one frontmatter name.
kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: SkillCard
metadata: {name: probe-hand-written}
spec: {image: localhost/konveyor-skills:probe-full, subPath: plan, type: skill}
EOF
await 60 card_ready probe-hand-written || fail "hand-written card never became Ready"
card_ready probe-bundle-plan || fail "generated card is not Ready"
echo "--- both cards report Ready, so nothing objects at apply time"

# An Agent referencing both stages both, and only the loader can see that the
# two card names carry one frontmatter name.
kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: Agent
metadata: {name: probe-agent}
spec:
  image: ${AGENT_IMAGE}
  prompt: "probe"
  gateways: [{ref: ${GATEWAY}}]
  skillCards: [{ref: probe-hand-written}]
  skillCollections: [{ref: probe-bundle}]
EOF
await 60 agent_ready probe-agent || fail "agent never became Ready"
echo "    the Agent reports AllDependenciesReady too"

kc apply -f - >/dev/null <<EOF
apiVersion: konveyor.io/v1alpha1
kind: AgentRun
metadata: {name: probe-collision}
spec:
  agentRef: probe-agent
  gateway: ${GATEWAY}
  instructions: "This run is expected to fail at init."
EOF
await 180 run_finished probe-collision || fail "collision run never finished"
phase="$(kc get agentrun probe-collision -o jsonpath='{.status.phase}')"
[ "${phase}" = "Failed" ] || fail "collision run reached ${phase}, want Failed"
loader_log="$(kc logs probe-collision -c skill-loader 2>&1 || true)"
echo "${loader_log}" | grep -q "duplicate skill name" \
    || fail "pod failed for some other reason:\n${loader_log}"
echo "${loader_log}" | grep "duplicate skill name" | sed 's/^/    /'
echo "    the pod is the first thing to notice, which is the gap #153 is about"

fi

echo
echo "==> 4/4 deleting the collection collects its cards and spares the rest"
kc delete skillcollection probe-bundle >/dev/null
await 120 no_cards probe-bundle || fail "generated cards outlived their collection"
kc get skillcard probe-hand-written >/dev/null 2>&1 \
    || fail "a hand-authored card was garbage collected with the collection"
echo "--- generated cards gone, hand-authored card still present"

echo
if [ -n "${SKIPPED_COLLISION:-}" ]; then
    echo "Three collection probes passed; the collision probe was skipped."
else
    echo "All four collection probes behaved as ADR 0015 describes."
fi
