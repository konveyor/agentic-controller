#!/usr/bin/env bash
# lib/step-execute.sh — per-item stateless loop.
set -euo pipefail

# ── Extract goal from PLAN.md ──
_extract_goal() {
  local plan_md="$1"
  sed -n '/^## Goal$/,/^##/p' "$plan_md" | sed '1d;$d' | sed '/^$/d'
}

step_execute() {
  local repo="$1"
  local plan="$RUN_DIR/plan.json"
  local plan_md="$RUN_DIR/PLAN.md"
  local execution_log="$RUN_DIR/execution-log.md"
  local migration_type
  migration_type=$(jq -r '.migration_type' "$plan")

  local total_items
  total_items=$(jq -r '.items | length' "$plan")
  info "Step 3/5: executing $total_items items (migration: $migration_type)"

  # ── Initialize execution-log.md ──
  {
    echo "# Execution Log"
    echo ""
    echo "**Migration:** $migration_type"
    echo "**Started:** $(date)"
    echo ""
    echo "---"
    echo ""
  } > "$execution_log"

  local ok_count=0 fail_count=0 skip_count=0 current=0

  while IFS= read -r item_json; do
    local n path action out
    n=$(echo "$item_json" | jq -r '.n')
    path=$(echo "$item_json" | jq -r '.path')
    action=$(echo "$item_json" | jq -r '.action')
    out="$RUN_DIR/item-$(printf '%03d' "$n").json"
    current=$((current + 1))

    # Resume: skip if already complete
    if [[ -s "$out" ]] && [[ "$(jq -r '.status' "$out" 2>/dev/null)" == "ok" ]]; then
      ok "  ($current/$total_items) #$n already done, skipping"
      ok_count=$((ok_count + 1))
      continue
    fi

    info "  ($current/$total_items) #$n [$action] $path"
    info "       invoking goose — this may take 15-60s per file..."

    goose_run "$MH_RECIPES/execute.yaml" --max-turns 10 \
      --params repo="$repo" \
      --params plan_md_path="$plan_md" \
      --params migration_type="$migration_type" \
      --params item_n="$n" \
      --params item_path="$path" \
      --params item_action="$action" \
    > "$out" || true

    # Check if we got valid output
    if [[ ! -s "$out" ]] || [[ "$(cat "$out")" == "null" ]]; then
      err "  ($current/$total_items) #$n goose produced no output"
      echo '{"status":"failed"}' > "$out"
      fail_count=$((fail_count + 1))
      continue
    fi

    local status lesson error_log files_touched
    status=$(jq -r '.status // "unknown"' "$out" 2>/dev/null)
    lesson=$(jq -r '.lesson // ""' "$out" 2>/dev/null)
    error_log=$(jq -r '.error_log // ""' "$out" 2>/dev/null)
    files_touched=$(jq -r '.files_touched // [] | join(", ")' "$out" 2>/dev/null)

    # Append to execution-log.md
    {
      echo "## Step #$n: $action - $path"
      echo ""
      echo "**Status:** $status"
      [[ -n "$files_touched" ]] && echo "**Files touched:** $files_touched"
      [[ -n "$lesson" ]] && echo -e "\n**Lesson learned:**\n$lesson"
      [[ -n "$error_log" ]] && echo -e "\n**Errors:**\n\`\`\`\n$error_log\n\`\`\`"
      echo ""
      echo "---"
      echo ""
    } >> "$execution_log"

    case "$status" in
      ok)
        ok_count=$((ok_count + 1))
        # Best-effort: tick the checklist (escape the dot in regex)
        sed -i.bak "s|- \[ \] ${n}\\\. |- [x] ${n}. |" "$repo/.goosehints" 2>/dev/null && rm -f "$repo/.goosehints.bak" || true
        ok "  ($current/$total_items) #$n done"
        [[ -n "$lesson" ]] && info "       lesson: ${lesson:0:80}..."
        ;;
      skipped)
        skip_count=$((skip_count + 1))
        warn "  ($current/$total_items) #$n skipped"
        ;;
      *)
        fail_count=$((fail_count + 1))
        err "  ($current/$total_items) #$n status=$status"
        [[ -n "$error_log" ]] && err "       error: ${error_log:0:100}..."
        ;;
    esac
  done < <(jq -c '.items[]' "$plan")

  jq -n --argjson ok "$ok_count" --argjson fail "$fail_count" --argjson skip "$skip_count" \
    '{ok:$ok, failed:$fail, skipped:$skip}' > "$RUN_DIR/execute-summary.json"

  # Copy execution-log to repo for easy access
  cp "$execution_log" "$repo/execution-log.md" 2>/dev/null || true

  echo
  ok "Step 3/5 complete: $ok_count ok, $fail_count failed, $skip_count skipped"
  info "  Execution log: $execution_log"
}
