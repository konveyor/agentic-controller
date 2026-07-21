#!/usr/bin/env bash
# Pre-demo smoke: verify every surface the demo touches, then run the full
# protocol smoke (create mock run -> Running -> ACP prompt over WebSocket ->
# session/load replay -> delete). Exit 0 = safe to present.
#
#   hack/demo-check.sh        # after hack/demo-up.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SHIM_PORT="${SHIM_PORT:-7080}"
UI_PORT="${UI_PORT:-5199}"
NS=konveyor-agents

ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
die()  { printf '  \033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

echo "── surfaces ────────────────────────────────────────"
curl -sf --max-time 2 "http://127.0.0.1:$SHIM_PORT/healthz" >/dev/null \
  || die "shim not answering on :$SHIM_PORT — run hack/demo-up.sh"
ok "shim healthy (:$SHIM_PORT)"
if curl -sf --max-time 2 "http://127.0.0.1:$UI_PORT/" >/dev/null 2>&1; then
  ok "ui serving (:$UI_PORT)"
else
  warn "ui not answering on :$UI_PORT — browser beats will fail; run hack/demo-up.sh"
fi

echo "── cluster ─────────────────────────────────────────"
avail=$(kubectl get deploy agentic-controller-controller-manager -n agentic-controller-system \
  -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || echo "")
[ "$avail" = "True" ] || die "controller not Available — run hack/demo-up.sh"
ok "controller Available"
ready=$(kubectl get agent migration-analyzer -n $NS \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
[ "$ready" = "True" ] || die "agent migration-analyzer not Ready — check: kubectl get llmproviders,agents -n $NS"
ok "agent migration-analyzer Ready"
gready=$(kubectl get agent migration-analyzer-goose -n $NS \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
if [ "$gready" = "True" ]; then
  ok "agent migration-analyzer-goose Ready (real-agent beat available)"
else
  warn "migration-analyzer-goose not Ready — beat 2 (goose+Bedrock) unavailable"
fi

echo "── full protocol smoke (mock run, free) ────────────"
# create -> Running -> WS ACP -> prompt -> session/load replay -> delete,
# using only fetch + native WebSocket (browser constraints).
(cd "$ROOT/packages/hub-shim" && SHIM_URL="http://127.0.0.1:$SHIM_PORT" npx tsx dev/browser-smoke.ts)

echo
ok "all checks passed — safe to present"
