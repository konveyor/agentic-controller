#!/usr/bin/env bash
# lib/step-fix-loop.sh — bounded verify/fix iteration.
set -euo pipefail

step_fix_loop() {
  local repo="$1"
  local migration_type
  migration_type=$(jq -r '.migration_type' "$RUN_DIR/plan.json")
  local max="${MH_MAX_FIX_ITERATIONS:-3}"

  # Check if Step 4 verification succeeded
  local verify_out="$RUN_DIR/verify.json"
  local verify_report="$RUN_DIR/verification-report.md"

  info "Step 5/5: fix loop (conditional on build status)"

  # Read build status from Step 4 verification
  if [[ ! -f "$verify_out" ]]; then
    err "  no verification output found — run step 4 (verify) first"
    return 1
  fi

  local build_ok
  build_ok=$(jq -r '.build_ok // false' "$verify_out")

  if [[ "$build_ok" == "true" ]]; then
    ok "  build status: SUCCESS (from verification report)"
    ok "  fix loop not needed — skipping"
    ok "Step 5/5 complete (skipped - build already successful)"
    return 0
  fi

  warn "  build status: FAILED (from verification report)"
  info "  starting fix loop (max $max iterations)..."

  local iter=1
  while (( iter <= max )); do
    local verify_iter_out="$RUN_DIR/verify-fix-$iter.json"

    info "  iteration $iter/$max — re-verifying..."
    if step_verify "$repo" "$verify_iter_out"; then
      local err_count
      err_count=$(jq -r '(.errors // []) | length' "$verify_iter_out" 2>/dev/null || echo 0)
      if (( err_count == 0 )); then
        ok "  iteration $iter/$max — build clean, no errors!"
        ok "  all compilation errors resolved after $iter fix iteration(s)"
        ok "Step 5/5 complete"

        # Create success report
        local fix_report="$RUN_DIR/fix-loop-report.md"
        {
          echo "# Fix Loop Report"
          echo ""
          echo "**Migration:** $migration_type"
          echo "**Status:** ✅ **SUCCESS**"
          echo "**Iterations:** $iter"
          echo ""
          echo "## Fixes Applied"
          echo ""
          for (( i=1; i<iter; i++ )); do
            if [[ -f "$RUN_DIR/fix-$i.json" ]]; then
              local fix_file fix_summary
              fix_file=$(jq -r '.file_changed // "unknown"' "$RUN_DIR/fix-$i.json")
              fix_summary=$(jq -r '.summary // "No summary"' "$RUN_DIR/fix-$i.json")
              echo "### Iteration $i"
              echo ""
              echo "- **File:** $fix_file"
              echo "- **Summary:** $fix_summary"
              echo ""
            fi
          done
          echo "## Result"
          echo ""
          echo "All compilation errors resolved. Build is now successful."
        } > "$fix_report"

        cp "$fix_report" "$repo/fix-loop-report.md" 2>/dev/null || true
        info "  fix loop report: $fix_report"

        return 0
      fi
    fi

    # Use the appropriate verify output
    local current_verify_out
    if (( iter == 1 )); then
      current_verify_out="$verify_out"  # Initial verify from Step 4
    else
      current_verify_out="$verify_iter_out"  # From previous iteration
    fi

    # Check if we have errors to fix
    if [[ ! -s "$current_verify_out" ]]; then
      err "  iteration $iter/$max — verify produced no output; stopping"
      return 1
    fi

    local err_count
    err_count=$(jq -r '(.errors // []) | length' "$current_verify_out" 2>/dev/null || echo 0)
    if (( err_count == 0 )); then
      warn "  iteration $iter/$max — build not OK but no errors reported; cannot auto-fix"
      return 1
    fi

    local err_file err_msg
    err_file=$(jq -r '.errors[0].file // "unknown"' "$current_verify_out")
    err_msg=$(jq -r '.errors[0].message // "unknown error"' "$current_verify_out")

    info "  iteration $iter/$max — fixing: $err_file"
    info "       error: ${err_msg:0:100}"
    info "       invoking goose to fix..."

    local fix_out="$RUN_DIR/fix-$iter.json"
    goose_run "$MH_RECIPES/fix.yaml" --max-turns 8 \
      --params repo="$repo" \
      --params verification_report_path="$verify_report" \
      --params migration_type="$migration_type" \
      --params error_file="$err_file" \
      --params error_message="$err_msg" \
    > "$fix_out" || true

    if [[ ! -s "$fix_out" ]] || [[ "$(cat "$fix_out")" == "null" ]]; then
      err "  iteration $iter/$max — fix produced no output"
      return 1
    fi

    local fixed
    fixed=$(jq -r '.fixed // false' "$fix_out" 2>/dev/null)
    if [[ "$fixed" != "true" ]]; then
      err "  iteration $iter/$max — fix did not succeed ($err_file)"
      jq -r '.summary // empty' "$fix_out" >&2 2>/dev/null || true
      return 1
    fi

    local file_changed
    file_changed=$(jq -r '.file_changed // "?"' "$fix_out")
    ok "  iteration $iter/$max — fixed $file_changed"
    iter=$((iter + 1))
  done

  warn "  hit max iterations ($max) — manual intervention needed"

  # Create final fix-loop report
  local fix_report="$RUN_DIR/fix-loop-report.md"
  {
    echo "# Fix Loop Report"
    echo ""
    echo "**Migration:** $migration_type"
    echo "**Iterations:** $max"
    echo "**Status:** Manual intervention needed (max iterations reached)"
    echo ""
    echo "## Attempted Fixes"
    echo ""
    for (( i=1; i<iter; i++ )); do
      if [[ -f "$RUN_DIR/fix-$i.json" ]]; then
        local fix_file fix_summary
        fix_file=$(jq -r '.file_changed // "unknown"' "$RUN_DIR/fix-$i.json")
        fix_summary=$(jq -r '.summary // "No summary"' "$RUN_DIR/fix-$i.json")
        echo "### Iteration $i"
        echo ""
        echo "- **File:** $fix_file"
        echo "- **Summary:** $fix_summary"
        echo ""
      fi
    done
    echo "## Next Steps"
    echo ""
    echo "Manual intervention is required. Review the remaining errors in verification-report.md"
  } > "$fix_report"

  cp "$fix_report" "$repo/fix-loop-report.md" 2>/dev/null || true
  info "  fix loop report: $fix_report"

  return 1
}
