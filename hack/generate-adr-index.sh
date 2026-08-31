#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ADR_DIR="$ROOT_DIR/docs/adr"
README="$ADR_DIR/README.md"
RECONCILIATION="$ADR_DIR/RECONCILIATION.md"
CHECK=false

case "${1:-}" in
  "") ;;
  --check) CHECK=true ;;
  *) echo "usage: $0 [--check]" >&2; exit 2 ;;
esac

read_metadata() {
  local path=$1
  local metadata
  local key value

  metadata=$(awk '
    NR == 1 { valid = ($0 == "---"); next }
    !ended && $0 == "---" { ended = 1; next }
    !ended && $0 ~ /^[a-z][a-z0-9_]*:/ {
      key = $0
      sub(/:.*/, "", key)
      value = $0
      sub(/^[^:]*:[[:space:]]*/, "", value)
      if (value ~ /^".*"$/) {
        sub(/^"/, "", value)
        sub(/"$/, "", value)
      }
      print key "\t" value
    }
    END { if (!valid || !ended) exit 1 }
  ' "$path") || {
    echo "$path: missing or malformed YAML front matter" >&2
    exit 1
  }

  adr= title= description= status= date= last_updated= authors= last_reviewed=
  implementation_status= review_note= authors_seen=false

  while IFS=$'\t' read -r key value; do
    case "$key" in
      adr) adr=$value ;;
      title) title=$value ;;
      description) description=$value ;;
      status) status=$value ;;
      date) date=$value ;;
      last_updated) last_updated=$value ;;
      authors) authors=$value; authors_seen=true ;;
      last_reviewed) last_reviewed=$value ;;
      implementation_status) implementation_status=$value ;;
      review_note) review_note=$value ;;
    esac
  done <<< "$metadata"

  local missing=false
  for key in adr title description status date last_updated last_reviewed implementation_status review_note; do
    if [[ -z ${!key} ]]; then
      echo "$path: missing front matter field: $key" >&2
      missing=true
    fi
  done
  if [[ "$authors_seen" != true ]]; then
    echo "$path: missing front matter field: authors" >&2
    missing=true
  fi
  [[ "$missing" == false ]] || exit 1

  if [[ ! "$adr" =~ ^[0-9]{4}$ || ${path##*/} != "$adr"-* ]]; then
    echo "$path: adr must match the four-digit filename prefix" >&2
    exit 1
  fi
  if [[ "$status" != proposed && "$status" != accepted && "$status" != superseded ]]; then
    echo "$path: invalid status: $status" >&2
    exit 1
  fi
  case "$implementation_status" in
    in-sync|amended|superseded|deferred) ;;
    *) echo "$path: invalid implementation_status: $implementation_status" >&2; exit 1 ;;
  esac
  for key in date last_reviewed; do
    if [[ ! ${!key} =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
      echo "$path: $key must be an ISO date" >&2
      exit 1
    fi
  done
  if [[ "$last_updated" != null && ! "$last_updated" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
    echo "$path: last_updated must be an ISO date or null" >&2
    exit 1
  fi
}

markdown() {
  local value=$1
  value=${value//|/\\|}
  printf '%s' "$value"
}

replace_block() {
  local source=$1
  local begin=$2
  local end=$3
  local block=$4
  local destination=$5

  [[ $(grep -Fc "$begin" "$source") -eq 1 ]] || {
    echo "$source: expected exactly one $begin marker" >&2
    exit 1
  }
  [[ $(grep -Fc "$end" "$source") -eq 1 ]] || {
    echo "$source: expected exactly one $end marker" >&2
    exit 1
  }

  awk -v begin="$begin" -v end="$end" -v block="$block" '
    $0 == begin {
      inside = 1
      while ((getline line < block) > 0) print line
      next
    }
    inside && $0 == end { inside = 0; next }
    !inside { print }
  ' "$source" > "$destination"
}

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
readme_block="$tmpdir/readme-block"
reconciliation_block="$tmpdir/reconciliation-block"

printf '%s\n\n' '<!-- BEGIN ADR INDEX -->' > "$readme_block"
printf '%s\n' '| ADR | Title | Status | Implementation | Last reviewed |' >> "$readme_block"
printf '%s\n' '| --- | --- | --- | --- | --- |' >> "$readme_block"

printf '%s\n\n' '<!-- BEGIN ADR RECONCILIATION -->' > "$reconciliation_block"
printf '%s\n' '| ADR | Title | Status | Implementation | Last reviewed | Review result |' >> "$reconciliation_block"
printf '%s\n' '| --- | --- | --- | --- | --- | --- |' >> "$reconciliation_block"

ids=""
adr_count=0
for path in "$ADR_DIR"/[0-9][0-9][0-9][0-9]-*.md; do
  [[ -f "$path" ]] || continue
  read_metadata "$path"
  case " $ids " in
    *" $adr "*) echo "$path: duplicate ADR identifier: $adr" >&2; exit 1 ;;
  esac
  ids="$ids $adr"
  adr_count=$((adr_count + 1))

  title_md=$(markdown "$title")
  note_md=$(markdown "$review_note")
  printf '| [%s](./%s) | %s | %s | %s | %s |\n' \
    "$adr" "${path##*/}" "$title_md" "$status" "$implementation_status" "$last_reviewed" >> "$readme_block"
  printf '| [%s](./%s) | %s | %s | %s | %s | %s |\n' \
    "$adr" "${path##*/}" "$title_md" "$status" "$implementation_status" "$last_reviewed" "$note_md" >> "$reconciliation_block"
done

[[ "$adr_count" -gt 0 ]] || { echo "no ADRs found in $ADR_DIR" >&2; exit 1; }
printf '%s\n' '<!-- END ADR INDEX -->' >> "$readme_block"
printf '%s\n' '<!-- END ADR RECONCILIATION -->' >> "$reconciliation_block"

readme_output="$tmpdir/README.md"
reconciliation_output="$tmpdir/RECONCILIATION.md"
replace_block "$README" '<!-- BEGIN ADR INDEX -->' '<!-- END ADR INDEX -->' "$readme_block" "$readme_output"
replace_block "$RECONCILIATION" '<!-- BEGIN ADR RECONCILIATION -->' '<!-- END ADR RECONCILIATION -->' "$reconciliation_block" "$reconciliation_output"

stale=false
if ! cmp -s "$README" "$readme_output"; then
  stale=true
  if [[ "$CHECK" == true ]]; then
    echo "error: docs/adr/README.md is stale; run make generate-adr-index" >&2
  else
    mv "$readme_output" "$README"
    echo "updated docs/adr/README.md"
  fi
fi
if ! cmp -s "$RECONCILIATION" "$reconciliation_output"; then
  stale=true
  if [[ "$CHECK" == true ]]; then
    echo "error: docs/adr/RECONCILIATION.md is stale; run make generate-adr-index" >&2
  else
    mv "$reconciliation_output" "$RECONCILIATION"
    echo "updated docs/adr/RECONCILIATION.md"
  fi
fi

[[ "$CHECK" == false || "$stale" == false ]]
