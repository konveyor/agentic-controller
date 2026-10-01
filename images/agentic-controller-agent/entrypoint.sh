#!/bin/sh
# Minimal harness stub for testing the controller pipeline.
# The real harness will manage git lifecycle and launch the agent runtime.
# See: https://github.com/konveyor/enhancements/pull/296

set -e

echo "=== konveyor agent-base ==="
echo "Workspace: $(pwd)"
echo "Skills:    $(ls /opt/skills/ 2>/dev/null || echo 'none')"
echo "Params:    $(cat /run/konveyor/params.json 2>/dev/null || echo 'none')"
echo "Gateway:   $(env | grep KONVEYOR_LLM_ | cut -d= -f1 | sort || echo 'none')"
echo ""

if [ -n "$KONVEYOR_INSTRUCTIONS" ]; then
    echo "Instructions: $KONVEYOR_INSTRUCTIONS"
fi

if [ -n "$KONVEYOR_PROMPT" ]; then
    echo "Prompt: $KONVEYOR_PROMPT"
fi

echo ""
echo "Agent run completed successfully."

# Serve the pod's ACP port (:4000 — fixed, it is what the controller's
# readiness probe targets) the way a real agent does: bind it only once
# "startup" is done — the harness starts goose, waits for it, then
# listens — so the ACPReady condition is exercised for real.
# STUB_ACP_DELAY_SECONDS stretches the gap for tests that want to observe
# the pre-listen state. A plain HTTP server (a listing of the empty
# workspace) answers 200 so a single curl can prove the dial, and it
# tolerates the kubelet's connect-and-close probes (ncat --sh-exec does
# not). Run it as a child so SIGTERM ends the pod promptly.
ACP_DELAY="${STUB_ACP_DELAY_SECONDS:-3}"
echo "ACP: binding :4000 in ${ACP_DELAY}s..."
sleep "${ACP_DELAY}"
echo "ACP: listening on :4000"
python3 -m http.server 4000 &
server=$!
trap 'kill "${server}" 2>/dev/null || true' TERM INT
wait "${server}"
exit 0
