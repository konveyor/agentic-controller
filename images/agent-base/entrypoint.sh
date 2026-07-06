#!/bin/sh
# Minimal harness stub for testing the controller pipeline.
# The real harness will manage git lifecycle and launch the agent runtime.
# See: https://github.com/konveyor/enhancements/pull/296

set -e

echo "=== konveyor agent-base ==="
echo "Workspace: $(pwd)"
echo "Skills:    $(ls /opt/skills/ 2>/dev/null || echo 'none')"
echo "Params:    $(env | grep KONVEYOR_PARAM_ | sort || echo 'none')"
echo "Models:    $(env | grep KONVEYOR_MODEL_ | sort || echo 'none')"
echo ""

if [ -n "$KONVEYOR_INSTRUCTIONS" ]; then
    echo "Instructions: $KONVEYOR_INSTRUCTIONS"
fi

if [ -n "$KONVEYOR_PROMPT" ]; then
    echo "Prompt: $KONVEYOR_PROMPT"
fi

echo ""
echo "Agent run completed successfully."
