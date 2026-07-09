#!/bin/sh
# Phase-4 agent base entrypoint: adapt the agentic-controller's KONVEYOR_*
# env contract to goose, then serve ACP.
#
# The controller injects: KONVEYOR_PARAM_* (run params),
# KONVEYOR_MODEL_<ROLE>_{PROVIDER,MODEL,API_KEY} (model selections),
# KONVEYOR_PROMPT / KONVEYOR_INSTRUCTIONS, GOOSE_SERVER__SECRET_KEY,
# plus run.spec.env / run.spec.envFrom passthrough. It does NOT clone the
# repository (the retired dev simulator did that in an init container) —
# the agent base owns workspace setup now.
#
# Credentials: the controller's single-key API_KEY injection fits
# OpenAI-style providers only. For SigV4 (Bedrock), pass the whole
# credential secret via run.spec.envFrom (AWS_ACCESS_KEY_ID,
# AWS_SECRET_ACCESS_KEY, AWS_REGION).
set -u

log() { echo "[agent-base] $*"; }

# 1. Workspace: clone the target repo into the controller's EmptyDir.
REPO="${KONVEYOR_PARAM_REPOSITORY:-}"
BRANCH="${KONVEYOR_PARAM_BRANCH:-main}"
if [ -n "$REPO" ]; then
  if [ -z "$(ls -A /workspace 2>/dev/null)" ]; then
    log "cloning $REPO@$BRANCH into /workspace"
    if git clone --depth 1 --branch "$BRANCH" "$REPO" /workspace 2>&1; then
      log "clone OK: $(ls /workspace | head -6 | tr '\n' ' ')"
    else
      log "WARNING: clone failed — agent starts with an empty workspace"
    fi
  else
    log "workspace not empty — skipping clone"
  fi
else
  log "no repository param — skipping clone"
fi

# 2. Model: map KONVEYOR_MODEL_PRIMARY_* onto goose env. Explicit
#    GOOSE_PROVIDER / GOOSE_MODEL (from run.spec.env) win. The provider
#    value is the LLMProvider CR *name*; map well-known names onto goose
#    provider implementations, else pass through verbatim.
if [ -z "${GOOSE_MODEL:-}" ] && [ -n "${KONVEYOR_MODEL_PRIMARY_MODEL:-}" ]; then
  GOOSE_MODEL="$KONVEYOR_MODEL_PRIMARY_MODEL"
  export GOOSE_MODEL
fi
if [ -z "${GOOSE_PROVIDER:-}" ] && [ -n "${KONVEYOR_MODEL_PRIMARY_PROVIDER:-}" ]; then
  case "$KONVEYOR_MODEL_PRIMARY_PROVIDER" in
    *bedrock*) GOOSE_PROVIDER=aws_bedrock ;;
    *anthropic*) GOOSE_PROVIDER=anthropic ;;
    *openai*) GOOSE_PROVIDER=openai ;;
    *ollama*) GOOSE_PROVIDER=ollama ;;
    *) GOOSE_PROVIDER="$KONVEYOR_MODEL_PRIMARY_PROVIDER" ;;
  esac
  export GOOSE_PROVIDER
fi
log "provider=${GOOSE_PROVIDER:-unset} model=${GOOSE_MODEL:-unset}"
if [ "${GOOSE_PROVIDER:-}" = "aws_bedrock" ] && [ -z "${AWS_ACCESS_KEY_ID:-}" ]; then
  log "WARNING: aws_bedrock selected but AWS_ACCESS_KEY_ID is unset — pass the credential secret via run.spec.envFrom"
fi

# 3. Standing prompt + instructions -> .goosehints in the workspace
#    (sessions run with cwd /workspace; goose reads hints from there).
if [ -n "${KONVEYOR_PROMPT:-}${KONVEYOR_INSTRUCTIONS:-}" ]; then
  {
    [ -n "${KONVEYOR_PROMPT:-}" ] && printf '%s\n' "$KONVEYOR_PROMPT"
    [ -n "${KONVEYOR_INSTRUCTIONS:-}" ] && printf '\n%s\n' "$KONVEYOR_INSTRUCTIONS"
    true # group status must reflect the redirect, not the last [ -n ] test
  } > /workspace/.goosehints 2>/dev/null && log "wrote /workspace/.goosehints" \
    || log "WARNING: could not write /workspace/.goosehints"
fi

# 4. Skills: the controller mounts each resolved SkillCard as an ImageVolume
#    at /opt/skills/<name>/. Fold every skill's SKILL.md into the hints so
#    the agent actually knows its skills. (Requires a runtime with k8s
#    ImageVolume support — containerd >= 2.0 / CRI-O; docker/cri-dockerd
#    pods fail with CreateContainerError before we ever run.)
if [ -d /opt/skills ]; then
  for d in /opt/skills/*/; do
    [ -f "${d}SKILL.md" ] || continue
    name="$(basename "$d")"
    {
      printf '\n\n## Skill: %s (files under %s)\n\n' "$name" "$d"
      cat "${d}SKILL.md"
    } >> /workspace/.goosehints 2>/dev/null \
      && log "folded skill '$name' into .goosehints" \
      || log "WARNING: could not fold skill '$name'"
  done
fi

exec goose serve --host 0.0.0.0 --port 4000
