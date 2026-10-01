#!/bin/sh
# E2E-only ACP stub. The base production image owns the normal agent stub;
# this entrypoint adds a bounded lifetime for workflow stage tests.

set -e

echo "=== konveyor e2e agent stub ==="
echo "Workspace: $(pwd)"
echo "Params:    $(cat /run/konveyor/params.json 2>/dev/null || echo 'none')"

if [ -n "${KONVEYOR_INSTRUCTIONS:-}" ]; then
    echo "Instructions: ${KONVEYOR_INSTRUCTIONS}"
fi

ACP_DELAY="${STUB_ACP_DELAY_SECONDS:-3}"
EXIT_AFTER="${STUB_EXIT_AFTER_SECONDS:-20}"
echo "ACP: binding :4000 in ${ACP_DELAY}s..."
sleep "${ACP_DELAY}"
echo "ACP: listening on :4000"
python3 -m http.server 4000 &
server=$!

(
    sleep "${EXIT_AFTER}"
    kill "${server}" 2>/dev/null || true
) &
stopper=$!

trap 'kill "${server}" "${stopper}" 2>/dev/null || true' TERM INT
wait "${server}" || true
kill "${stopper}" 2>/dev/null || true
exit 0
