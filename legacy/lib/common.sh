#!/usr/bin/env bash
# lib/common.sh — shared helpers. Sourced by every step.
set -euo pipefail

# ── Paths ────────────────────────────────────────────────────────
MH_HOME="${MH_HOME:-$HOME/.migration-harness}"
MH_CONFIG="$MH_HOME/config"
MH_RUNS="$MH_HOME/runs"
MH_INSTALL_DIR="${MH_INSTALL_DIR:-$(cd "$(dirname "$(dirname "${BASH_SOURCE[0]}")")" && pwd -P)}"
MH_RECIPES="$MH_INSTALL_DIR/recipes"

# ── Colors ───────────────────────────────────────────────────────
if [[ -t 2 ]]; then
  C_R='\033[0;31m'; C_G='\033[0;32m'; C_Y='\033[1;33m'
  C_B='\033[0;34m'; C_C='\033[0;36m'; C_BOLD='\033[1m'; C_X='\033[0m'
else
  C_R=''; C_G=''; C_Y=''; C_B=''; C_C=''; C_BOLD=''; C_X=''
fi

info()    { printf "${C_C}ℹ${C_X} %s\n" "$*" >&2; }
ok()      { printf "${C_G}✓${C_X} %s\n" "$*" >&2; }
warn()    { printf "${C_Y}⚠${C_X} %s\n" "$*" >&2; }
err()     { printf "${C_R}✗${C_X} %s\n" "$*" >&2; }
fatal()   { err "$*"; exit 1; }
header()  { printf "\n${C_BOLD}${C_B}── %s ──${C_X}\n" "$*" >&2; }

# ── Config I/O ───────────────────────────────────────────────────
load_config() {
  [[ -f "$MH_CONFIG" ]] || fatal "Not configured. Run: migration-harness init"
  # shellcheck disable=SC1090
  source "$MH_CONFIG"
  : "${MH_MODEL:?MH_MODEL missing from $MH_CONFIG}"
  : "${MH_PROVIDER:?MH_PROVIDER missing from $MH_CONFIG}"
}

save_config() {
  mkdir -p "$MH_HOME"
  cat > "$MH_CONFIG" <<EOF
# migration-harness config — generated $(date)
MH_MODEL="$MH_MODEL"
MH_PROVIDER="$MH_PROVIDER"
MH_MAX_TURNS="${MH_MAX_TURNS:-200}"
MH_MAX_FIX_ITERATIONS="${MH_MAX_FIX_ITERATIONS:-3}"
EOF
  chmod 600 "$MH_CONFIG"
}

# ── Dependency checks ────────────────────────────────────────────
require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fatal "Required command not found: $1"
}

check_deps() {
  require_cmd goose
  require_cmd jq
  require_cmd git
}

# ── Goose invocation ─────────────────────────────────────────────
# Usage: goose_run <recipe> [--max-turns N] [extra goose args...]
# Runs goose in background with live progress, extracts final output.
goose_run() {
  [[ -n "${RUN_DIR:-}" ]] || fatal "goose_run: RUN_DIR not set"

  local recipe="$1"; shift
  local name
  name=$(basename "$recipe" .yaml)
  local log="$RUN_DIR/logs/${name}-$(date +%s)-$$.json"
  mkdir -p "$RUN_DIR/logs"

  # Allow per-call max-turns override
  local max_turns="${MH_MAX_TURNS:-200}"
  if [[ "${1:-}" == "--max-turns" ]]; then
    max_turns="$2"; shift 2
  fi

  # Run goose in background so we can show live progress
  goose run \
    --recipe "$recipe" \
    --no-session \
    --quiet \
    --output-format json \
    --max-turns "$max_turns" \
    --provider "$MH_PROVIDER" \
    --model "$MH_MODEL" \
    "$@" \
  > "$log" 2>/dev/null &
  local goose_pid=$!

  # Live progress: watch the log and print what goose is doing
  _tail_goose_progress "$log" "$goose_pid" &
  local tail_pid=$!

  # Wait for goose — || true prevents set -e from killing the script
  wait "$goose_pid" 2>/dev/null || true
  local goose_exit=$?

  # Stop the tail watcher
  kill "$tail_pid" 2>/dev/null || true
  wait "$tail_pid" 2>/dev/null || true

  if (( goose_exit != 0 )); then
    warn "       goose exited with code $goose_exit"
  fi

  # Extract the recipe__final_output from the conversation JSON
  if [[ ! -s "$log" ]]; then
    warn "       goose produced no output"
    return 0
  fi

  # Strip any banner text before the JSON (goose sometimes prints ASCII art before {)
  local clean_log="$log.clean"
  sed -n '/^{/,$p' "$log" > "$clean_log" 2>/dev/null || cp "$log" "$clean_log"

  # Check for API errors in the output
  if grep -q "credit balance is too low\|rate limit\|quota exceeded" "$clean_log" 2>/dev/null; then
    err "       API error detected — check your provider billing/quota"
    head -5 "$clean_log" | grep -i "error\|credit\|rate\|quota" | head -1 | while read -r line; do
      err "       $line"
    done >&2
    rm -f "$clean_log"
    return 1
  fi

  jq -c '
    [ .messages[]?.content[]?
      | select(.type == "toolRequest"
               and .toolCall.value.name == "recipe__final_output")
      | .toolCall.value.arguments
    ] | last // empty
  ' "$clean_log" 2>/dev/null || true

  rm -f "$clean_log"
}

# ── Live progress watcher ────────────────────────────────────────
# Polls the log file every 2s and prints new goose tool calls.
# The log is a single JSON object that grows — we track file size changes
# and attempt to parse the latest content. Failures are silently ignored
# since the file is often incomplete while goose is running.
_tail_goose_progress() {
  local log="$1"
  local goose_pid="$2"
  local last_size=0
  local seen_count=0

  # Wait for the log file to appear (or goose to die)
  local wait_count=0
  while [[ ! -f "$log" ]] && kill -0 "$goose_pid" 2>/dev/null; do
    sleep 0.5
    wait_count=$((wait_count + 1))
    (( wait_count > 20 )) && return 0   # bail after 10s
  done

  while kill -0 "$goose_pid" 2>/dev/null; do
    local cur_size
    cur_size=$(wc -c < "$log" 2>/dev/null | tr -d ' ') || cur_size=0

    if (( cur_size > last_size )); then
      # Try to extract tool call activity from the (possibly incomplete) JSON
      local activity
      activity=$(python3 - "$log" "$seen_count" 2>/dev/null <<'PYEOF'
import json, sys, os

log_path = sys.argv[1]
skip = int(sys.argv[2])

try:
    with open(log_path) as f:
        raw = f.read()

    # Try parsing as-is; if incomplete, close any open braces
    data = None
    for suffix in ['', ']}]}', '"]}]}', '"}]}]}']:
        try:
            data = json.loads(raw + suffix)
            break
        except json.JSONDecodeError:
            continue

    if not data or 'messages' not in data:
        sys.exit(0)

    items = []
    for msg in data.get('messages', []):
        if msg.get('role') != 'assistant':
            continue
        for item in msg.get('content', []):
            t = item.get('type', '')
            if t == 'toolRequest':
                tc = item.get('toolCall', {}).get('value', {})
                name = tc.get('name', '')
                args = tc.get('arguments', {})
                if name == 'shell':
                    cmd = args.get('command', '')[:80]
                    items.append(f'shell: {cmd}')
                elif name == 'write':
                    items.append(f'write: {args.get("path", "")}')
                elif name == 'edit':
                    items.append(f'edit: {args.get("path", "")}')
                elif name == 'tree':
                    items.append(f'tree: {args.get("path", "")}')
                elif name == 'recipe__final_output':
                    items.append('finalizing output...')
                elif name:
                    items.append(name)
            elif t == 'thinking':
                items.append('thinking...')

    # Print only new items
    for item in items[skip:]:
        print(item)

except Exception:
    pass
PYEOF
) || activity=""

      if [[ -n "$activity" ]]; then
        local new_lines
        new_lines=$(echo "$activity" | wc -l | tr -d ' ')
        echo "$activity" | while IFS= read -r line; do
          [[ -n "$line" ]] && printf "       ${C_C}↳${C_X} %s\n" "$line" >&2
        done
        seen_count=$((seen_count + new_lines))
      fi

      last_size=$cur_size
    fi

    sleep 2
  done
}

# ── Interactive goose with session resumption ────────────────────
# Usage: goose_run_interactive <recipe> [--max-turns N] [extra goose args...]
# Like goose_run but supports resuming with more turns if incomplete.
# Max total turns: 140, asks user for turn increments.
goose_run_interactive() {
  [[ -n "${RUN_DIR:-}" ]] || fatal "goose_run_interactive: RUN_DIR not set"

  local recipe="$1"; shift
  local name
  name=$(basename "$recipe" .yaml)
  local session_name="${name}-$(date +%s)-$$"
  local log="$RUN_DIR/logs/${session_name}.json"
  mkdir -p "$RUN_DIR/logs"

  # Allow per-call max-turns override for initial run
  local initial_turns=50
  if [[ "${1:-}" == "--max-turns" ]]; then
    initial_turns="$2"; shift 2
  fi

  local max_total_turns=140
  local current_turns=0
  local session_started=false

  # Store extra args for all goose calls
  local -a extra_args=("$@")

  while true; do
    local turns_to_add=$initial_turns
    if $session_started; then
      # Resuming - ask user for turn increment
      echo
      read -rp "How many more turns to add? [default: 30, max: $((max_total_turns - current_turns))]: " user_turns
      turns_to_add=${user_turns:-30}

      # Validate input
      if ! [[ "$turns_to_add" =~ ^[0-9]+$ ]]; then
        err "Invalid input. Please enter a number."
        break
      fi

      # Cap at remaining budget
      local remaining=$((max_total_turns - current_turns))
      if (( turns_to_add > remaining )); then
        turns_to_add=$remaining
        warn "Capped to $turns_to_add turns (total limit: $max_total_turns)"
      fi
    fi

    current_turns=$((current_turns + turns_to_add))
    info "       running goose session '$session_name' (turns: $current_turns / $max_total_turns)..."

    # Run or resume goose session
    if ! $session_started; then
      # First run - create session
      goose run \
        --recipe "$recipe" \
        --name "$session_name" \
        --quiet \
        --output-format json \
        --max-turns "$turns_to_add" \
        --provider "$MH_PROVIDER" \
        --model "$MH_MODEL" \
        "${extra_args[@]}" \
      > "$log" 2>/dev/null &
      session_started=true
    else
      # Resume existing session with more turns
      goose run \
        --name "$session_name" \
        --resume \
        --max-turns "$turns_to_add" \
        --output-format json \
        --quiet \
        --provider "$MH_PROVIDER" \
        --model "$MH_MODEL" \
      >> "$log" 2>/dev/null &
    fi

    local goose_pid=$!

    # Live progress watcher
    _tail_goose_progress "$log" "$goose_pid" &
    local tail_pid=$!

    # Wait for goose
    wait "$goose_pid" 2>/dev/null || true
    local goose_exit=$?

    # Stop watcher
    kill "$tail_pid" 2>/dev/null || true
    wait "$tail_pid" 2>/dev/null || true

    if (( goose_exit != 0 )); then
      warn "       goose exited with code $goose_exit"
    fi

    # Check if we got final output
    if [[ ! -s "$log" ]]; then
      err "       goose produced no log output"
      break
    fi

    local clean_log="$log.clean"
    sed -n '/^{/,$p' "$log" > "$clean_log" 2>/dev/null || cp "$log" "$clean_log"

    # Check for API errors
    if grep -q "credit balance is too low\|rate limit\|quota exceeded" "$clean_log" 2>/dev/null; then
      err "       API error — check your provider billing/quota"
      rm -f "$clean_log"
      break
    fi

    # Try to extract final output
    local output
    output=$(jq -c '
      [ .messages[]?.content[]?
        | select(.type == "toolRequest"
                 and .toolCall.value.name == "recipe__final_output")
        | .toolCall.value.arguments
      ] | last // empty
    ' "$clean_log" 2>/dev/null || true)

    rm -f "$clean_log"

    # Check if we got complete output
    if [[ -n "$output" && "$output" != "null" ]]; then
      ok "       session completed successfully"
      echo "$output"
      # Cleanup session
      goose session remove --name "$session_name" >/dev/null 2>&1 || true
      return 0
    fi

    # No output yet - check if we hit turn limit
    if (( current_turns >= max_total_turns )); then
      warn "       reached maximum turns ($max_total_turns) without completing"
      warn "       session incomplete - manual inspection needed"
      # Cleanup session
      goose session remove --name "$session_name" >/dev/null 2>&1 || true
      return 1
    fi

    # Ask user if they want to continue
    echo
    warn "       session incomplete (used $current_turns turns so far)"
    read -rp "Continue with more turns? [Y/n]: " continue_choice
    if [[ "$continue_choice" =~ ^[Nn] ]]; then
      info "       stopping at user request"
      # Cleanup session
      goose session remove --name "$session_name" >/dev/null 2>&1 || true
      return 1
    fi
  done

  # Cleanup on any error path
  goose session delete "$session_name" >/dev/null 2>&1 || true
  return 1
}

# ── Run-id helpers ───────────────────────────────────────────────
new_run_dir() {
  local repo_name="$1"
  local id="${repo_name}-$(date +%Y%m%d-%H%M%S)"
  RUN_DIR="$MH_RUNS/$id"
  mkdir -p "$RUN_DIR/logs"
  echo "$RUN_DIR"
}

latest_run_dir() {
  local result
  result=$(ls -1dt "$MH_RUNS"/*/ 2>/dev/null | head -1 | sed 's:/$::')
  [[ -n "$result" ]] || return 1
  echo "$result"
}
