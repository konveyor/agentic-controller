#!/usr/bin/env bash
# lib/step-plan.sh — invokes plan recipe, applies human approval gate.
set -euo pipefail

step_plan() {
  local repo="$1"
  local request="$2"
  local out="$RUN_DIR/plan-goose-output.json"
  local plan_md="$repo/PLAN.md"

  info "Step 2/5: generating migration plan"

  # ── 2a. Pre-gather project context ──
  info "  2a. gathering project context (detect results, file tree, manifests, configs)..."
  local skill_dir="$MH_INSTALL_DIR/skill-bundle/goose-migration"
  local context_file="$RUN_DIR/plan-context.txt"
  _pregather_context "$repo" "$skill_dir" > "$context_file"
  local context_lines context_kb
  context_lines=$(wc -l < "$context_file" | tr -d ' ')
  context_kb=$(( $(wc -c < "$context_file" | tr -d ' ') / 1024 ))
  ok "  2a. gathered ${context_lines} lines (${context_kb}KB) of project context"

  # ── 2b. Load planner skill + migration references ──
  info "  2b. loading planner skill and migration references..."
  local ref_count=0
  if [[ -d "$skill_dir/references" ]]; then
    ref_count=$(find "$skill_dir/references" -name "*.md" | wc -l | tr -d ' ')
  fi
  ok "  2b. loaded planner skill + ${ref_count} reference doc(s)"

  # ── 2c. Render recipe ──
  info "  2c. building plan recipe..."
  local rendered_recipe="$RUN_DIR/plan-recipe.yaml"
  _render_plan_recipe "$repo" "$request" "$context_file" > "$rendered_recipe"
  local recipe_kb
  recipe_kb=$(( $(wc -c < "$rendered_recipe" | tr -d ' ') / 1024 ))
  ok "  2c. recipe ready (${recipe_kb}KB — skill + context + references baked in)"

  # ── 2d. Run goose (the LLM call) ──
  local plan_turns
  plan_turns=$(_calc_plan_turns "$RUN_DIR/detect.json" "$skill_dir")
  info "  2d. running goose planner (max $plan_turns turns, based on project size)..."
  info "       this is the LLM step — may take 30-90s depending on model"

  goose_run "$rendered_recipe" --max-turns "$plan_turns" \
    > "$out" || true

  # ── 2e. Validate PLAN.md ──
  if [[ -s "$plan_md" ]]; then
    local step_count complex_count
    step_count=$(grep -c '^### Step' "$plan_md" 2>/dev/null || echo 0)
    complex_count=$(grep -c '⚠️' "$plan_md" 2>/dev/null || echo 0)
    cp "$plan_md" "$RUN_DIR/PLAN.md"
    ok "  2e. PLAN.md written — ${step_count} steps (${complex_count} complex)"
  else
    fatal "plan failed — PLAN.md not written; see $RUN_DIR/logs/"
  fi

  # ── 2f. Parse PLAN.md → plan.json ──
  info "  2f. parsing PLAN.md → plan.json for downstream steps..."
  _plan_md_to_json "$plan_md" > "$RUN_DIR/plan.json"
  local item_count
  item_count=$(jq '.items | length' "$RUN_DIR/plan.json")
  ok "  2f. parsed ${item_count} items from PLAN.md"

  # ── 2g. Human approval gate ──
  echo
  printf "${C_BOLD}══════════════════ PLAN ══════════════════${C_X}\n" >&2
  cat "$plan_md" >&2
  printf "${C_BOLD}══════════════════════════════════════════${C_X}\n\n" >&2

  read -rp "Approve and execute? [y/edit/N]: " approval
  case "$approval" in
    y|Y|yes)
      ok "  2g. plan approved"
      ;;
    edit|e)
      "${EDITOR:-vi}" "$plan_md"
      cp "$plan_md" "$RUN_DIR/PLAN.md"
      _plan_md_to_json "$plan_md" > "$RUN_DIR/plan.json"
      ok "  2g. plan edited and re-parsed"
      ;;
    *)
      info "  2g. aborted by user"
      exit 0
      ;;
  esac

  # ── 2h. Write .goosehints ──
  info "  2h. writing .goosehints for execution steps..."
  write_hints "$repo" "$RUN_DIR/plan.json" "$request"
  ok "  2h. .goosehints written"

  ok "Step 2/5 complete"
}

# ── Render recipe with planner skill + pre-gathered context inlined ──
_render_plan_recipe() {
  local repo="$1"
  local request="$2"
  local context_file="$3"
  local skill_dir="$MH_INSTALL_DIR/skill-bundle/goose-migration"

  # Load planner skill
  local planner_skill=""
  if [[ -f "$skill_dir/skills/migration-plan/SKILL.md" ]]; then
    planner_skill=$(sed 's/^/    /' "$skill_dir/skills/migration-plan/SKILL.md")
  fi

  # Indent context for YAML block scalar
  local indented_context
  indented_context=$(sed 's/^/    /' "$context_file")

  cat <<RECIPE_EOF
version: "1.0.0"
title: "Migration plan"
description: "Generate PLAN.md using the planner skill."

settings:
  temperature: 1

extensions:
  - type: builtin
    name: developer
    timeout: 600
    bundled: true

instructions: |
  You are the planner sub-skill. Your ONLY job is to produce PLAN.md in the
  repo root. Do NOT modify any source files.

  === PLANNER SKILL ===
$planner_skill
  === END PLANNER SKILL ===

  === PRE-GATHERED CONTEXT ===
  The following has been pre-collected: detect.json, source file tree,
  and build manifests. Do NOT re-run these discovery commands.
$indented_context
  === END PRE-GATHERED CONTEXT ===

  YOUR JOB (follow this order strictly):

  PHASE 1 — Quick scan (max 3 reads):
  1. Read the pre-gathered context above (detect.json, file tree) — already done.
  2. Read the build manifest (pom.xml, package.json, .csproj, etc.) — 1 read.
  3. Check AVAILABLE REFERENCES list. If one matches, read it — 1 read.

  PHASE 2 — Write a DRAFT PLAN.md:
  4. Based on what you know so far, write PLAN.md to $repo/PLAN.md NOW.
     For files you haven't read, make your best guess from the file name
     and mark uncertain steps with ⚠️.

  PHASE 3 — Refine (max 5 reads):
  5. Read ONLY the source files where your guess might be wrong — complex
     patterns like MDBs, JNDI lookups, lifecycle listeners, etc.
  6. Update PLAN.md with corrections based on what you read.
     If no corrections needed, skip this phase.

  RULES:
  - Write PLAN.md BEFORE reading source files. Draft first, refine after.
  - Max 8 file reads total across all phases.
  - If uncertain about a file, mark it ⚠️ and move on. Do NOT read every file.

prompt: |
  Repo:              $repo
  Migration request: $request

  Follow the planner skill phases. All discovery data and references are
  already in your instructions. Read only complex source files you need,
  then write PLAN.md.

response:
  json_schema:
    type: object
    required: [plan_written, step_count]
    properties:
      plan_written: { type: boolean }
      step_count:   { type: integer }
      complex_count: { type: integer }
      summary:      { type: string }
RECIPE_EOF
}

# ── Calculate plan turns from detect.json ──
#
# The plan step needs turns for:
#   - reading build manifest          (~1 turn)
#   - reading a reference             (~1 turn)
#   - reading complex source files    (~1 turn each, max 5-8)
#   - reading config files            (~1-2 turns)
#   - thinking + writing PLAN.md      (~2-3 turns)
#   - final_output                    (~1 turn)
#
# Formula:
#   base        = 10 (covers manifest + reference + write + thinking + final)
#   +1 per complex pattern (each needs a source file read)
#   +2 if no matching reference (goose explores more)
#   +1 per 50 source files (larger projects need more config reads)
#   +3 if multi-language (>2 languages with files)
#   clamped to [12, 50]
#
_calc_plan_turns() {
  local detect="$1"
  local skill_dir="$2"
  local turns=10

  # Complex patterns — each one likely needs goose to read a source file
  local mdb ejb react_class py2_xrange
  mdb=$(jq '.patterns.mdb_files // 0' "$detect")
  ejb=$(jq '.patterns.ejb_files // 0' "$detect")
  react_class=$(jq '.patterns.react_class_components // 0' "$detect")
  py2_xrange=$(jq '.patterns.py2_xrange_files // 0' "$detect")

  # Count distinct complex pattern types (not individual files)
  local complex=0
  (( mdb > 0 )) && complex=$((complex + mdb))           # each MDB is unique
  (( ejb > 0 )) && complex=$((complex + 1))              # EJBs are similar, 1 read enough
  (( react_class > 0 )) && complex=$((complex + 1))
  (( py2_xrange > 0 )) && complex=$((complex + 1))
  # Cap complex reads at 5 — draft-first handles the rest
  (( complex > 5 )) && complex=5
  turns=$((turns + complex))

  # Check if a matching reference exists
  local has_ref=false
  if [[ -d "$skill_dir/references" ]]; then
    local manifests
    manifests=$(jq -c '.manifests' "$detect")
    local files
    files=$(jq -c '.files' "$detect")
    # Java project + javaee reference?
    if [[ "$(echo "$manifests" | jq '.pom_xml')" == "true" ]] && \
       [[ -f "$skill_dir/references/javaee-quarkus.md" ]]; then
      has_ref=true
    fi
    # Python project + python reference?
    if (( $(echo "$files" | jq '.python // 0') > 0 )) && \
       ls "$skill_dir/references/"*python* >/dev/null 2>&1; then
      has_ref=true
    fi
  fi
  if [[ "$has_ref" == "false" ]]; then
    turns=$((turns + 2))
  fi

  # Project size — larger projects have more config files to read
  # Use the primary language count, not total (avoids bower_components noise)
  local primary_files
  primary_files=$(jq '[.files | to_entries[] | .value] | sort | reverse | .[0] // 0' "$detect")
  turns=$((turns + primary_files / 50))

  # Multi-language project
  local lang_count
  lang_count=$(jq '[.files | to_entries[] | select(.value > 0)] | length' "$detect")
  if (( lang_count > 2 )); then
    turns=$((turns + 3))
  fi

  # Clamp
  (( turns < 12 )) && turns=12
  (( turns > 50 )) && turns=50

  echo "$turns"
}

# ── Pre-gather context that goose would otherwise fetch via tool calls ──
_pregather_context() {
  local repo="$1"
  local skill_dir="$2"

  echo "=== DETECTION SUMMARY ==="
  cat "$RUN_DIR/detect.json"
  echo ""

  echo "=== FILE TREE (source + config only) ==="
  find "$repo" -type f \
    \( -name "*.java" -o -name "*.py" -o -name "*.xml" -o -name "*.yaml" \
       -o -name "*.yml" -o -name "*.properties" -o -name "*.sql" \
       -o -name "*.json" -o -name "*.toml" -o -name "*.gradle" \
       -o -name "*.kt" -o -name "*.groovy" -o -name "Dockerfile" \
       -o -name "*.cs" -o -name "*.csproj" -o -name "*.sln" -o -name "*.config" \
       -o -name "*.go" -o -name "*.mod" -o -name "*.sum" \
       -o -name "*.rb" -o -name "*.gemspec" -o -name "Gemfile" \
       -o -name "*.rs" -o -name "Cargo.toml" -o -name "Cargo.lock" \
       -o -name "*.swift" -o -name "*.php" -o -name "*.sh" \
       -o -name "*.cfg" -o -name "*.ini" -o -name "Makefile" \) \
    -not -path "*/target/*" -not -path "*/node_modules/*" \
    -not -path "*/.git/*" -not -path "*/.vscode/*" \
    -not -path "*/bower_components/*" -not -path "*/.metadata/*" \
    -not -path "*/bin/*" -not -path "*/obj/*" -not -path "*/vendor/*" \
    | sed "s|^$repo/||" | sort
  echo ""

  # Build manifests and config files — NOT pre-read.
  # Goose reads what it needs via developer tools based on the file tree above.

  # List available references — goose reads what it needs via developer tools
  if [[ -d "$skill_dir/references" ]]; then
    echo "=== AVAILABLE REFERENCES ==="
    echo "Directory: $skill_dir/references/"
    for ref in "$skill_dir/references/"*.md; do
      [[ -f "$ref" ]] || continue
      echo "  - $(basename "$ref")"
    done
    echo ""
    echo "Use developer tools to read the reference(s) relevant to the migration request."
    echo ""
  fi
}

# ── Parse PLAN.md into plan.json for downstream step_execute/step_verify ──
# Handles multiple PLAN.md formats:
#   Format A: "### Step 1: Title\n- File: path\n- Action: MODIFY"
#   Format B: "1. `path`\n   - description..."
#   Format C: "29. **DELETE:** `path`"
_plan_md_to_json() {
  local plan_md="$1"

  python3 - "$plan_md" <<'PYEOF'
import re, json, sys

plan_md = sys.argv[1]
with open(plan_md) as f:
    content = f.read()

items = []

# ── Try Format A: "### Step N: title" with "- File:" and "- Action:" ──
step_pattern_a = re.compile(
    r'### Step (\d+):\s*(.*?)(?:\s*✅.*)?\n(.*?)(?=### Step \d+|## Verification|## Notes|\Z)',
    re.DOTALL
)
for m in step_pattern_a.finditer(content):
    n = int(m.group(1))
    title = m.group(2).strip()
    body = m.group(3)

    file_m = re.search(r'- File:\s*(.+)', body)
    action_m = re.search(r'- Action:\s*(\w+)', body)
    path = file_m.group(1).strip() if file_m else ''
    action = action_m.group(1).strip().lower() if action_m else 'migrate'

    action_map = {'modify': 'migrate', 'create': 'create', 'delete': 'delete'}
    action = action_map.get(action, action)
    risk = 'high' if '⚠️' in title or 'COMPLEX' in title.upper() else 'low'

    items.append({'n': n, 'path': path, 'action': action, 'risk': risk, 'notes': title})

# ── Try Format B/C: "N. `path`" or "N. **DELETE:** `path`" ──
if not items:
    # Match: "1. `pom.xml`" or "29. **DELETE:** `src/foo.xml`"
    item_pattern = re.compile(
        r'^(\d+)\.\s+'                      # number
        r'(?:\*\*(?:DELETE|REMOVE)[:\*]*\s*)?'  # optional DELETE prefix
        r'`([^`]+)`'                         # path in backticks
        r'(.*?)$',                           # rest of line (← CREATE NEW, ⚠️, etc.)
        re.MULTILINE
    )
    for m in item_pattern.finditer(content):
        n = int(m.group(1))
        path = m.group(2).strip()
        rest = m.group(3).strip()
        full_line = m.group(0)

        # Determine action
        if re.search(r'\bDELETE\b|\bREMOVE\b', full_line, re.I):
            action = 'delete'
        elif re.search(r'\bCREATE\b', full_line, re.I):
            action = 'create'
        else:
            action = 'migrate'

        # Risk
        risk = 'high' if '⚠️' in full_line or 'COMPLEX' in full_line.upper() else 'low'

        # Notes — grab indented lines after this item until next item
        item_start = m.end()
        next_item = re.search(r'^\d+\.\s+', content[item_start:], re.MULTILINE)
        next_section = re.search(r'^##', content[item_start:], re.MULTILINE)
        end = item_start + min(
            next_item.start() if next_item else len(content),
            next_section.start() if next_section else len(content)
        )
        body_lines = content[item_start:end].strip().split('\n')
        notes = '; '.join(
            line.strip().lstrip('- ') for line in body_lines[:3]
            if line.strip().startswith('-')
        )
        if not notes:
            notes = path

        items.append({'n': n, 'path': path, 'action': action, 'risk': risk, 'notes': notes})

# ── Assign layers from paths ──
for item in items:
    path = item['path']
    layer = 'unknown'
    p = path.lower()
    if any(x in p for x in ['pom.xml', 'package.json', 'build.gradle', '.csproj', '.sln',
                              'cargo.toml', 'gemfile', 'go.mod', 'requirements.txt',
                              'pyproject.toml', 'setup.py']):
        layer = 'build'
    elif any(x in p for x in ['application.properties', 'application.yml', 'appsettings',
                                'web.config', '.env', 'config.yaml', 'config.json']):
        layer = 'config'
    elif any(x in p for x in ['persistence.xml', 'web.xml', 'beans.xml',
                                'global.asax', 'startup.cs', 'program.cs']):
        layer = 'config'
    elif any(x in p for x in ['/model/', '/models/', '/domain/', '/entity/', '/entities/']):
        layer = 'model'
    elif any(x in p for x in ['/service/', '/services/']):
        layer = 'service'
    elif any(x in p for x in ['/rest/', '/controller/', '/controllers/', '/api/',
                                '/endpoint/', '/endpoints/', '/handler/', '/handlers/']):
        layer = 'api'
    elif any(x in p for x in ['/utils/', '/util/', '/helper/', '/helpers/', '/common/']):
        layer = 'util'
    elif any(x in p for x in ['/persistence/', '/repository/', '/repositories/', '/dao/',
                                '/data/']):
        layer = 'persistence'
    elif any(x in p for x in ['weblogic/', '/views/', '/pages/']):
        layer = 'cleanup' if 'weblogic' in p else 'view'
    item['layer'] = layer

# ── Detect migration type from full content ──
mt = 'custom'
if re.search(r'quarkus|java.?ee|jakarta|weblogic', content, re.I):
    mt = 'java-ee-to-quarkus'
elif re.search(r'python.?[23]|py2|py3', content, re.I):
    mt = 'python2-to-python3'
elif re.search(r'react|hooks|class.?component', content, re.I):
    mt = 'react-class-to-hooks'
elif re.search(r'\.net|asp\.net|csharp|c#|\.NET\s*(Core|Framework)', content, re.I):
    mt = 'dotnet-upgrade'
elif re.search(r'spring.?boot|spring.?framework', content, re.I):
    mt = 'spring-boot-upgrade'

# Source/target from content
src_match = re.search(r'(?:from|source)[:\s]+(.+?)(?:\n|→|->)', content, re.I)
tgt_match = re.search(r'(?:to|target|→|->)\s*(.+?)(?:\n|$)', content, re.I)
source = src_match.group(1).strip() if src_match else ''
target = tgt_match.group(1).strip() if tgt_match else ''

plan = {
    'migration_type': mt,
    'source_stack': source,
    'target_stack': target,
    'items': items
}
print(json.dumps(plan))
PYEOF
}

write_hints() {
  local repo="$1"
  local plan="$2"
  local request="$3"
  local hints="$repo/.goosehints"

  {
    echo "# AUTO-GENERATED by migration-harness"
    echo "# $(date) | request: $request"
    echo ""
    echo "## TOKEN DISCIPLINE"
    echo "- Read ONE file at a time"
    echo "- After writing each file: STOP"
    echo "- Do NOT re-read migrated files"
    echo "- Do NOT compile unless explicitly asked"
    echo ""
    echo "## Migration"
    jq -r '"- Source: \(.source_stack)\n- Target: \(.target_stack)\n- Type:   \(.migration_type)"' "$plan"
    echo ""
    echo "## Order"
    jq -r '.items[] | "\(.n). [\(.action)] \(.path)  — \(.notes)"' "$plan"
    echo ""
    echo "## Checklist"
    jq -r '.items[] | "- [ ] \(.n). \(.path)"' "$plan"
  } > "$hints"

  ok "  wrote $hints"
}
