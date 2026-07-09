#!/usr/bin/env bash
# lib/step-detect.sh — structural inspection of the repo.
# No migration knowledge - just extract the code graph using graphify.
set -euo pipefail

step_detect() {
  local repo="$1"
  local out="$RUN_DIR/detect.json"

  info "Step 1/5: detecting project structure"

  # ── 1a. Check manifest files ──
  info "  1a. checking manifest files..."
  local has_pom=false has_pkg=false has_pyproj=false has_reqtxt=false has_setup=false has_gomod=false has_cargo=false has_gemfile=false
  [[ -f "$repo/pom.xml" ]]          && has_pom=true
  [[ -f "$repo/package.json" ]]     && has_pkg=true
  [[ -f "$repo/pyproject.toml" ]]   && has_pyproj=true
  [[ -f "$repo/requirements.txt" ]] && has_reqtxt=true
  [[ -f "$repo/setup.py" ]]         && has_setup=true
  [[ -f "$repo/go.mod" ]]           && has_gomod=true
  [[ -f "$repo/Cargo.toml" ]]       && has_cargo=true
  [[ -f "$repo/Gemfile" ]]          && has_gemfile=true
  ok "  1a. manifests: pom=$has_pom pkg=$has_pkg pyproj=$has_pyproj req=$has_reqtxt setup=$has_setup gomod=$has_gomod cargo=$has_cargo gemfile=$has_gemfile"

  # ── 1b. Build code graph with graphify ──
  info "  1b. building code graph (AST extraction, edges, communities)..."
  info "       this may take 10-30s for large codebases (parallelized across 12 workers)..."

  # Detect correct Python interpreter
  local PYTHON=""
  # Check for uv-installed graphify Python
  if [[ -x "$HOME/.local/share/uv/tools/graphifyy/bin/python" ]]; then
    PYTHON="$HOME/.local/share/uv/tools/graphifyy/bin/python"
  elif command -v python3 >/dev/null 2>&1; then
    # Fallback to system python3 (check if graphify is available)
    if python3 -c "import graphify" 2>/dev/null; then
      PYTHON="python3"
    fi
  fi

  if [[ -z "$PYTHON" ]]; then
    fatal "graphify Python module not found. Run: pip install graphifyy"
  fi

  # Run graphify via Python API
  "$PYTHON" "$MH_INSTALL_DIR/lib/build-graph.py" "$repo" 2>&1 | while IFS= read -r line; do
    # Show all output to user
    echo "       $line"
  done

  if [[ ! -f "$repo/graphify-out/graph.json" ]]; then
    fatal "graphify failed - check logs above"
  fi

  # Copy outputs to run directory
  cp "$repo/graphify-out/graph.json" "$RUN_DIR/graph.json"
  if [[ -f "$repo/graphify-out/GRAPH_REPORT.md" ]]; then
    cp "$repo/graphify-out/GRAPH_REPORT.md" "$RUN_DIR/GRAPH_REPORT.md"
  fi

  # Extract summary stats from graph
  local nodes edges communities god_nodes
  nodes=$(jq '.nodes | length' "$RUN_DIR/graph.json")
  edges=$(jq '.links | length' "$RUN_DIR/graph.json")
  communities=$(jq '[.communities[].id] | unique | length' "$RUN_DIR/graph.json")
  god_nodes=$(jq '[.nodes[] | select(.degree > 20)] | length' "$RUN_DIR/graph.json")

  ok "  1b. graph: $nodes nodes, $edges edges, $communities communities ($god_nodes high-degree nodes)"

  # ── 1c. Count source files by extension (from graph, not filesystem) ──
  info "  1c. analyzing file types from graph..."
  local java_files py_files js_files ts_files go_files rs_files cs_files rb_files
  java_files=$(jq '[.nodes[] | select(.source_file | test("\\.java$"))] | length' "$RUN_DIR/graph.json")
  py_files=$(jq '[.nodes[] | select(.source_file | test("\\.py$"))] | length' "$RUN_DIR/graph.json")
  js_files=$(jq '[.nodes[] | select(.source_file | test("\\.(js|jsx)$"))] | length' "$RUN_DIR/graph.json")
  ts_files=$(jq '[.nodes[] | select(.source_file | test("\\.(ts|tsx)$"))] | length' "$RUN_DIR/graph.json")
  go_files=$(jq '[.nodes[] | select(.source_file | test("\\.go$"))] | length' "$RUN_DIR/graph.json")
  rs_files=$(jq '[.nodes[] | select(.source_file | test("\\.rs$"))] | length' "$RUN_DIR/graph.json")
  cs_files=$(jq '[.nodes[] | select(.source_file | test("\\.cs$"))] | length' "$RUN_DIR/graph.json")
  rb_files=$(jq '[.nodes[] | select(.source_file | test("\\.rb$"))] | length' "$RUN_DIR/graph.json")
  ok "  1c. files: java=$java_files py=$py_files js=$js_files ts=$ts_files go=$go_files rs=$rs_files cs=$cs_files rb=$rb_files"

  # ── 1d. Write detect.json (minimal metadata - graph has the details) ──
  info "  1d. writing detect.json..."
  jq -n \
    --arg repo "$repo" \
    --argjson manifests "$(jq -n --argjson pom "$has_pom" --argjson pkg "$has_pkg" \
                              --argjson pyproj "$has_pyproj" --argjson reqtxt "$has_reqtxt" \
                              --argjson setup "$has_setup" --argjson gomod "$has_gomod" \
                              --argjson cargo "$has_cargo" --argjson gemfile "$has_gemfile" \
                              '{pom_xml:$pom, package_json:$pkg, pyproject_toml:$pyproj, requirements_txt:$reqtxt, setup_py:$setup, go_mod:$gomod, cargo_toml:$cargo, gemfile:$gemfile}')" \
    --argjson files "$(jq -n --argjson j "$java_files" --argjson p "$py_files" \
                             --argjson js "$js_files" --argjson ts "$ts_files" \
                             --argjson go "$go_files" --argjson rs "$rs_files" \
                             --argjson cs "$cs_files" --argjson rb "$rb_files" \
                             '{java:$j, python:$p, javascript:$js, typescript:$ts, go:$go, rust:$rs, csharp:$cs, ruby:$rb}')" \
    --argjson graph_stats "$(jq -n \
                             --argjson n "$nodes" --argjson e "$edges" \
                             --argjson c "$communities" --argjson g "$god_nodes" \
                             '{nodes:$n, edges:$e, communities:$c, god_nodes:$g}')" \
    '{repo:$repo, manifests:$manifests, files:$files, graph:$graph_stats, graph_file:"graph.json"}' \
  > "$out"

  ok "Step 1/5 complete → detect.json + graph.json ($nodes nodes, $communities communities)"
  echo
  info "Graph outputs available in $RUN_DIR:"
  info "  - graph.json         (full graph structure for planning)"
  info "  - GRAPH_REPORT.md    (human-readable summary)"
}
