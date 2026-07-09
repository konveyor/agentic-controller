#!/usr/bin/env bash
# lib/metrics.sh — Track timing and generate metrics.json
set -euo pipefail

# Track when a step starts
track_step_start() {
  local step="$1"
  [[ -n "${RUN_DIR:-}" ]] || return 0
  echo "$(date +%s)|$step|start" >> "$RUN_DIR/.timings"
}

# Track when a step ends
track_step_end() {
  local step="$1"
  local status="${2:-success}"  # success/failed
  [[ -n "${RUN_DIR:-}" ]] || return 0
  echo "$(date +%s)|$step|end|$status" >> "$RUN_DIR/.timings"
}

# Generate metrics.json from all collected data
generate_metrics() {
  local repo="$1"
  local request="$2"
  [[ -n "${RUN_DIR:-}" ]] || fatal "generate_metrics: RUN_DIR not set"

  local timings_file="$RUN_DIR/.timings"
  local metrics_file="$RUN_DIR/metrics.json"

  # Parse timings
  local detect_start detect_end detect_duration detect_status
  local plan_start plan_end plan_duration plan_status
  local execute_start execute_end execute_duration execute_status
  local verify_start verify_end verify_duration verify_status
  local fix_start fix_end fix_duration fix_status

  if [[ -f "$timings_file" ]]; then
    detect_start=$(grep "detect|start" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    detect_end=$(grep "detect|end" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    detect_status=$(grep "detect|end" "$timings_file" 2>/dev/null | cut -d'|' -f4 || echo "unknown")

    plan_start=$(grep "plan|start" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    plan_end=$(grep "plan|end" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    plan_status=$(grep "plan|end" "$timings_file" 2>/dev/null | cut -d'|' -f4 || echo "unknown")

    execute_start=$(grep "execute|start" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    execute_end=$(grep "execute|end" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    execute_status=$(grep "execute|end" "$timings_file" 2>/dev/null | cut -d'|' -f4 || echo "unknown")

    verify_start=$(grep "verify|start" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    verify_end=$(grep "verify|end" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    verify_status=$(grep "verify|end" "$timings_file" 2>/dev/null | cut -d'|' -f4 || echo "unknown")

    fix_start=$(grep "fix_loop|start" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    fix_end=$(grep "fix_loop|end" "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
    fix_status=$(grep "fix_loop|end" "$timings_file" 2>/dev/null | cut -d'|' -f4 || echo "unknown")
  else
    detect_start=0; detect_end=0; detect_status="unknown"
    plan_start=0; plan_end=0; plan_status="unknown"
    execute_start=0; execute_end=0; execute_status="unknown"
    verify_start=0; verify_end=0; verify_status="unknown"
    fix_start=0; fix_end=0; fix_status="unknown"
  fi

  # Calculate durations
  detect_duration=$((detect_end - detect_start))
  plan_duration=$((plan_end - plan_start))
  execute_duration=$((execute_end - execute_start))
  verify_duration=$((verify_end - verify_start))
  fix_duration=$((fix_end - fix_start))

  # Get overall start/end
  local overall_start=$(head -1 "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
  local overall_end=$(tail -1 "$timings_file" 2>/dev/null | cut -d'|' -f1 || echo "0")
  local total_duration=$((overall_end - overall_start))

  # Extract data from step outputs
  local nodes=0 edges=0 communities=0
  if [[ -f "$RUN_DIR/detect.json" ]]; then
    nodes=$(jq -r '.graph.nodes // 0' "$RUN_DIR/detect.json")
    edges=$(jq -r '.graph.edges // 0' "$RUN_DIR/detect.json")
    communities=$(jq -r '.graph.communities // 0' "$RUN_DIR/detect.json")
  fi

  local reference_used="none" steps_generated=0 complex_steps=0 plan_turns=0
  if [[ -f "$RUN_DIR/plan-goose-output.json" ]]; then
    reference_used=$(jq -r '.reference_used // "none"' "$RUN_DIR/plan-goose-output.json")
    steps_generated=$(jq -r '.step_count // 0' "$RUN_DIR/plan-goose-output.json")
    complex_steps=$(jq -r '.complex_count // 0' "$RUN_DIR/plan-goose-output.json")
  fi
  if [[ -f "$RUN_DIR/plan.json" ]]; then
    steps_generated=$(jq -r '.items | length' "$RUN_DIR/plan.json")
  fi

  local items_total=0 items_succeeded=0 items_failed=0
  if [[ -f "$RUN_DIR/plan.json" ]]; then
    items_total=$(jq -r '.items | length' "$RUN_DIR/plan.json")
    items_succeeded=$(find "$RUN_DIR" -name 'item-*.json' -exec jq -r 'select(.status=="ok") | .n' {} \; 2>/dev/null | wc -l | tr -d ' ')
    items_failed=$((items_total - items_succeeded))
  fi

  local build_ok=false tests_total=0 tests_passed=0 tests_failed=0 fix_attempts=0 verify_turns=0
  if [[ -f "$RUN_DIR/verify.json" ]]; then
    build_ok=$(jq -r '.build_ok // false' "$RUN_DIR/verify.json")
    tests_total=$(jq -r '.tests_total // 0' "$RUN_DIR/verify.json")
    tests_passed=$(jq -r '.tests_passed // 0' "$RUN_DIR/verify.json")
    tests_failed=$(jq -r '.tests_failed // 0' "$RUN_DIR/verify.json")
    fix_attempts=$(jq -r '.fix_attempts // 0' "$RUN_DIR/verify.json")
  fi

  local fix_iterations=0
  if [[ -f "$RUN_DIR/fix-loop-report.md" ]]; then
    fix_iterations=$(grep "Iterations:" "$RUN_DIR/fix-loop-report.md" | head -1 | grep -oE '[0-9]+' || echo "0")
  fi

  # Determine overall status
  local overall_status="success"
  if [[ "$detect_status" == "failed" || "$plan_status" == "failed" || "$execute_status" == "failed" ]]; then
    overall_status="failed"
  elif [[ "$build_ok" == "false" ]]; then
    overall_status="partial"
  fi

  # Generate JSON
  jq -n \
    --arg model "${MH_MODEL}" \
    --arg provider "${MH_PROVIDER}" \
    --arg request "$request" \
    --arg repo "$repo" \
    --arg started "$(date -r "$overall_start" -Iseconds 2>/dev/null || date -Iseconds)" \
    --arg completed "$(date -r "$overall_end" -Iseconds 2>/dev/null || date -Iseconds)" \
    --argjson total_duration "$total_duration" \
    --arg overall_status "$overall_status" \
    --argjson detect_duration "$detect_duration" \
    --arg detect_status "$detect_status" \
    --argjson nodes "$nodes" \
    --argjson edges "$edges" \
    --argjson communities "$communities" \
    --argjson plan_duration "$plan_duration" \
    --arg plan_status "$plan_status" \
    --arg reference_used "$reference_used" \
    --argjson steps_generated "$steps_generated" \
    --argjson complex_steps "$complex_steps" \
    --argjson execute_duration "$execute_duration" \
    --arg execute_status "$execute_status" \
    --argjson items_total "$items_total" \
    --argjson items_succeeded "$items_succeeded" \
    --argjson items_failed "$items_failed" \
    --argjson verify_duration "$verify_duration" \
    --arg verify_status "$verify_status" \
    --argjson build_ok "$build_ok" \
    --argjson tests_total "$tests_total" \
    --argjson tests_passed "$tests_passed" \
    --argjson tests_failed "$tests_failed" \
    --argjson fix_attempts "$fix_attempts" \
    --argjson fix_duration "$fix_duration" \
    --arg fix_status "$fix_status" \
    --argjson fix_iterations "$fix_iterations" \
    '{
      model: $model,
      provider: $provider,
      migration_request: $request,
      repo_path: $repo,
      started_at: $started,
      completed_at: $completed,
      total_duration_seconds: $total_duration,
      overall_status: $overall_status,
      steps: {
        detect: {
          duration_seconds: $detect_duration,
          status: $detect_status,
          nodes: $nodes,
          edges: $edges,
          communities: $communities
        },
        plan: {
          duration_seconds: $plan_duration,
          status: $plan_status,
          reference_used: $reference_used,
          steps_generated: $steps_generated,
          complex_steps: $complex_steps
        },
        execute: {
          duration_seconds: $execute_duration,
          status: $execute_status,
          total_items: $items_total,
          items_succeeded: $items_succeeded,
          items_failed: $items_failed
        },
        verify: {
          duration_seconds: $verify_duration,
          status: $verify_status,
          build_ok: $build_ok,
          tests_total: $tests_total,
          tests_passed: $tests_passed,
          tests_failed: $tests_failed,
          fix_attempts: $fix_attempts
        },
        fix_loop: {
          duration_seconds: $fix_duration,
          status: $fix_status,
          iterations: $fix_iterations
        }
      }
    }' > "$metrics_file"

  echo "$metrics_file"
}
