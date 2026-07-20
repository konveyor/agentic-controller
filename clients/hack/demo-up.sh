#!/usr/bin/env bash
# One-command demo bring-up: converge the cluster, start the shim + UI.
# Idempotent — safe to re-run after a reboot or `minikube stop`.
#
#   hack/demo-up.sh          # bring everything up
#   hack/demo-down.sh        # stop the local processes (cluster untouched)
#
# Logs: /tmp/demo-hub-shim.log, /tmp/demo-ui.log
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SHIM_PORT="${SHIM_PORT:-7080}"
UI_PORT="${UI_PORT:-5199}"
NS=konveyor-agents
# Real Konveyor Hub the shim reads the application inventory from. A
# port-forward gives the laptop a stable HUB_URL; in-cluster this would be
# the Hub service DNS. If Hub is unreachable the shim falls back to a stub.
HUB_NS="${HUB_NS:-konveyor}"
HUB_SVC="${HUB_SVC:-tackle2-hub}"
HUB_LOCAL_PORT="${HUB_LOCAL_PORT:-18090}"

ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
die()  { printf '  \033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

echo "── cluster ──────────────────────────────────────────"
minikube status >/dev/null 2>&1 || die "minikube is not running — start it: minikube start"
[ "$(kubectl config current-context)" = "minikube" ] || die "kubectl context is not minikube"
ok "minikube up, context correct"

kubectl get deploy agent-sandbox-controller -n agent-sandbox-system >/dev/null 2>&1 \
  || die "Agent Sandbox missing — install v0.5.0 (helm chart from kubernetes-sigs/agent-sandbox)"
ok "Agent Sandbox present"

echo "── controller (this repo) ──────────────────────────"
if ! kubectl get deployment agentic-controller-controller-manager \
    -n agentic-controller-system >/dev/null 2>&1; then
  die "agentic-controller not deployed — from the repo root: make docker-build deploy IMG=agentic-controller:dev (build into minikube: eval \$(minikube docker-env) first, or minikube image load agentic-controller:dev)"
fi
kubectl wait deployment/agentic-controller-controller-manager \
  -n agentic-controller-system --for=condition=Available --timeout=120s >/dev/null
ok "controller Available"

echo "── agent-base images ───────────────────────────────"
if ! minikube image ls 2>/dev/null | grep -q 'acp-mock-harness:dev'; then
  warn "building acp-mock-harness:dev"
  (cd "$ROOT/harness-mock" && minikube image build -t acp-mock-harness:dev -f Dockerfile . >/dev/null)
fi
ok "acp-mock-harness:dev"
if ! minikube image ls 2>/dev/null | grep -q 'goose-harness:dev'; then
  warn "building goose-harness:dev"
  (cd "$ROOT/harness-goose" && minikube image build -t goose-harness:dev -f Dockerfile . >/dev/null)
fi
ok "goose-harness:dev"

echo "── sample resources ────────────────────────────────"
kubectl apply -f "$ROOT/manifests/samples.yaml" >/dev/null
ok "samples applied (mock agent + provider)"
if kubectl get secret aws-bedrock-creds -n $NS >/dev/null 2>&1; then
  kubectl apply -f "$ROOT/manifests/goose-bedrock.yaml" >/dev/null
  ok "goose-bedrock applied (aws-bedrock-creds present)"
else
  warn "aws-bedrock-creds secret missing — skipping goose-bedrock.yaml (mock-only demo); see manifests/goose-bedrock.yaml header to create it"
fi

# Readiness gate: providers verify via a Job, then agents flip Ready.
if kubectl wait agent/migration-analyzer -n $NS \
     --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True --timeout=90s >/dev/null 2>&1; then
  ok "agent migration-analyzer Ready"
else
  warn "migration-analyzer not Ready yet — check: kubectl get llmproviders,agents -n $NS"
fi

echo "── konveyor hub (application inventory) ────────────"
HUB_URL="http://127.0.0.1:$HUB_LOCAL_PORT"
if curl -sf --max-time 2 "$HUB_URL/applications" >/dev/null 2>&1; then
  ok "Hub reachable on :$HUB_LOCAL_PORT — reusing"
elif kubectl get svc "$HUB_SVC" -n "$HUB_NS" >/dev/null 2>&1; then
  # kubectl port-forward drops on "lost connection to pod" — wrap it in a
  # restart loop so a mid-demo drop self-heals (the shim re-fetches per
  # request, so it recovers as soon as the forward is back).
  nohup bash -c "while true; do kubectl port-forward -n '$HUB_NS' 'svc/$HUB_SVC' '$HUB_LOCAL_PORT:8080'; sleep 1; done" \
    </dev/null >/tmp/demo-hub-pf.log 2>&1 &
  echo $! > /tmp/demo-hub-pf.pid
  for _ in $(seq 1 20); do
    curl -sf --max-time 1 "$HUB_URL/applications" >/dev/null 2>&1 && break
    sleep 0.5
  done
  if curl -sf --max-time 2 "$HUB_URL/applications" >/dev/null 2>&1; then
    ok "port-forward $HUB_NS/$HUB_SVC -> :$HUB_LOCAL_PORT (pid $(cat /tmp/demo-hub-pf.pid))"
  else
    warn "Hub port-forward not ready — shim will use its offline stub"
    HUB_URL=""
  fi
else
  warn "no $HUB_SVC in ns $HUB_NS — shim will use its offline stub"
  HUB_URL=""
fi

echo "── hub-shim (:$SHIM_PORT) ──────────────────────────"
if curl -sf --max-time 2 "http://127.0.0.1:$SHIM_PORT/healthz" >/dev/null 2>&1; then
  ok "already serving — reusing"
else
  [ -d "$ROOT/packages/hub-shim/node_modules" ] || (cd "$ROOT/packages/hub-shim" && npm install >/dev/null 2>&1)
  # Background ONLY the nohup command (an `&` after a `cmd1 && cmd2` list
  # would fork a lingering wrapper shell that holds our stdout open and
  # poisons the pidfile).
  (
    cd "$ROOT/packages/hub-shim"
    PORT=$SHIM_PORT HUB_URL=$HUB_URL nohup npm start </dev/null > /tmp/demo-hub-shim.log 2>&1 &
    echo $! > /tmp/demo-hub-shim.pid
  )
  for _ in $(seq 1 20); do
    curl -sf --max-time 1 "http://127.0.0.1:$SHIM_PORT/healthz" >/dev/null 2>&1 && break
    sleep 0.5
  done
  curl -sf --max-time 2 "http://127.0.0.1:$SHIM_PORT/healthz" >/dev/null || die "shim failed — /tmp/demo-hub-shim.log"
  ok "started (pid $(cat /tmp/demo-hub-shim.pid), log /tmp/demo-hub-shim.log)"
fi

echo "── ui (:$UI_PORT) ──────────────────────────────────"
if curl -sf --max-time 2 "http://127.0.0.1:$UI_PORT/" >/dev/null 2>&1; then
  ok "already serving — reusing"
else
  [ -d "$ROOT/ui/node_modules" ] || (cd "$ROOT/ui" && npm install >/dev/null 2>&1)
  (
    cd "$ROOT/ui"
    VITE_SHIM_URL="http://127.0.0.1:$SHIM_PORT" nohup npm run dev -- --host 127.0.0.1 --port "$UI_PORT" --strictPort </dev/null > /tmp/demo-ui.log 2>&1 &
    echo $! > /tmp/demo-ui.pid
  )
  for _ in $(seq 1 30); do
    curl -sf --max-time 1 "http://127.0.0.1:$UI_PORT/" >/dev/null 2>&1 && break
    sleep 0.5
  done
  curl -sf --max-time 2 "http://127.0.0.1:$UI_PORT/" >/dev/null || die "ui failed — /tmp/demo-ui.log"
  ok "started (pid $(cat /tmp/demo-ui.pid), log /tmp/demo-ui.log)"
fi

echo
echo "ready:"
echo "  ui        http://localhost:$UI_PORT"
echo "  shim      http://127.0.0.1:$SHIM_PORT/healthz"
echo "  real run  kubectl create -f docs/demo/real-run.yaml   (goose+Bedrock; needs aws-bedrock-creds)"
echo "  note      don't rely on runs across a minikube restart —"
echo "            create a fresh run right before presenting."
echo "  script    docs/DEMO.md"
