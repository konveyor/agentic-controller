#!/usr/bin/env bash
# lib/step-verify.sh — single-shot verify recipe call.
set -euo pipefail

step_verify() {
  local repo="$1"
  local out="$2"        # caller specifies path so fix-loop can re-verify
  local plan_md="$RUN_DIR/PLAN.md"
  local execution_log="$repo/execution-log.md"
  local migration_type
  migration_type=$(jq -r '.migration_type' "$RUN_DIR/plan.json")

  info "Step 4/5: verifying build + tests (migration: $migration_type)"
  info "  invoking goose — running verification with auto-fix (up to 3 attempts)..."
  info "  this may take 2-5 minutes depending on project size..."
  info "  session will ask for more turns if needed (max total: 140)"

  # Use interactive session - starts with 50 turns, can resume up to 140 total
  goose_run_interactive "$MH_RECIPES/verify.yaml" --max-turns 50 \
    --params repo="$repo" \
    --params plan_md_path="$plan_md" \
    --params execution_log_path="$execution_log" \
    --params migration_type="$migration_type" \
  > "$out" || true

  if [[ ! -s "$out" ]] || [[ "$(cat "$out")" == "null" ]]; then
    err "  verify produced no output"
    return 1
  fi

  local build_ok tests_passed tests_total err_count fix_attempts fixes_applied
  build_ok=$(jq -r '.build_ok // false' "$out")
  tests_passed=$(jq -r '.tests_passed // 0' "$out")
  tests_total=$(jq -r '.tests_total // 0' "$out")
  err_count=$(jq -r '(.errors // []) | length' "$out")
  fix_attempts=$(jq -r '.fix_attempts // 0' "$out")
  fixes_applied=$(jq -r '.fixes_applied // ""' "$out")

  # Show fix attempts if any were made
  if (( fix_attempts > 0 )); then
    info "  fix attempts: $fix_attempts"
    [[ -n "$fixes_applied" ]] && info "  fixes: ${fixes_applied:0:100}..."
  fi

  if [[ "$build_ok" == "true" ]]; then
    ok "  build: OK | tests: $tests_passed/$tests_total passed | errors: $err_count"
  else
    err "  build: FAILED | tests: $tests_passed/$tests_total passed | errors: $err_count"
  fi

  if (( err_count > 0 )); then
    info "  first 3 errors:"
    jq -r '.errors[0:3][] | "    \(.file // "?"):\(.line // "?") — \(.message // "unknown")"' "$out" >&2
  fi

  # Create verification-report.md
  local verify_report="$RUN_DIR/verification-report.md"
  {
    echo "# Verification Report"
    echo ""
    echo "**Migration:** $migration_type"
    echo "**Timestamp:** $(date)"
    echo ""
    echo "## Build Status"
    echo ""
    if [[ "$build_ok" == "true" ]]; then
      echo "- ✅ Compilation: **SUCCESS**"
    else
      echo "- ❌ Compilation: **FAILED**"
    fi
    echo "- Tests: $tests_passed/$tests_total passed"
    echo ""
    if (( fix_attempts > 0 )); then
      echo "## Auto-Fix Attempts"
      echo ""
      echo "- Fix iterations: $fix_attempts"
      [[ -n "$fixes_applied" ]] && echo "- Fixes applied: $fixes_applied"
      echo ""
    fi
    if (( err_count > 0 )); then
      echo "## Remaining Errors ($err_count total)"
      echo ""
      jq -r '.errors[] | "### \(.file // "unknown"):\(.line // 0)\n\n```\n\(.message // "unknown")\n```\n"' "$out"
    fi
    echo ""
    echo "## Summary"
    echo ""
    jq -r '.summary // "No summary provided"' "$out"
  } > "$verify_report"

  cp "$verify_report" "$repo/verification-report.md" 2>/dev/null || true
  info "  verification report: $verify_report"

  # Return non-zero if build failed
  [[ "$build_ok" == "true" ]] || return 1
}
