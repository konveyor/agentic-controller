#!/usr/bin/env bash
# Changelog fragment management script.
#
# Usage:
#   hack/changelog.sh validate                    Check fragments are well-formed
#   hack/changelog.sh create <name> <kind>        Create a new fragment file
#   hack/changelog.sh assemble <version>          Assemble fragments into CHANGELOG.md
#   hack/changelog.sh assemble --draft            Assemble as "Unreleased" (no deletion)
#
# Requires: yq (https://github.com/mikefarah/yq)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRAGMENTS_DIR="${REPO_ROOT}/changes/unreleased"
CHANGELOG_FILE="${REPO_ROOT}/CHANGELOG.md"
REPO_URL="https://github.com/konveyor/agentic-controller"

VALID_KINDS="breaking feature enhancement bugfix deprecation"

# Maps kind -> heading (bash 3 compatible)
kind_heading() {
  case "$1" in
    breaking)    echo "Breaking Changes" ;;
    feature)     echo "New Features" ;;
    enhancement) echo "Enhancements" ;;
    bugfix)      echo "Bug Fixes" ;;
    deprecation) echo "Deprecations" ;;
  esac
}

# Ensure yq is available
YQ="${YQ:-yq}"
if ! command -v "${YQ}" &>/dev/null; then
  echo "Error: yq is required but not found."
  echo "Install it or run: make yq"
  exit 1
fi

# --- Helpers ---

list_fragments() {
  local dir="$1"
  if [ ! -d "${dir}" ]; then
    return
  fi
  find "${dir}" -maxdepth 1 \( -name '*.yaml' -o -name '*.yml' \) ! -name template.yaml | sort
}

is_valid_kind() {
  local kind="$1"
  for k in ${VALID_KINDS}; do
    if [ "${k}" = "${kind}" ]; then
      return 0
    fi
  done
  return 1
}

# --- Subcommands ---

cmd_validate() {
  local files
  files=$(list_fragments "${FRAGMENTS_DIR}")

  if [ -z "${files}" ]; then
    echo "Error: No changelog fragments found in ${FRAGMENTS_DIR}/"
    echo ""
    echo "Add a fragment file for your PR. See changes/template.yaml for the format."
    echo "Example: hack/changelog.sh create 42-my-change bugfix"
    exit 1
  fi

  local errors=0
  while IFS= read -r file; do
    [ -z "${file}" ] && continue
    local name
    name=$(basename "${file}")

    # Check valid YAML
    if ! "${YQ}" eval '.' "${file}" &>/dev/null; then
      echo "  ERROR: ${name}: invalid YAML"
      errors=$((errors + 1))
      continue
    fi

    # Check kind
    local kind
    kind=$("${YQ}" eval '.kind // ""' "${file}")
    if [ -z "${kind}" ] || ! is_valid_kind "${kind}"; then
      echo "  ERROR: ${name}: invalid kind \"${kind}\" (must be one of: ${VALID_KINDS})"
      errors=$((errors + 1))
    fi

    # Check description
    local desc
    desc=$("${YQ}" eval '.description // ""' "${file}")
    desc=$(echo "${desc}" | xargs) # trim whitespace
    if [ -z "${desc}" ]; then
      echo "  ERROR: ${name}: description is required and must be non-empty"
      errors=$((errors + 1))
    fi
  done <<< "${files}"

  if [ ${errors} -gt 0 ]; then
    echo ""
    echo "${errors} validation error(s) found."
    exit 1
  fi

  local count
  count=$(echo "${files}" | grep -c .)
  echo "Validated ${count} changelog fragment(s)."
}

cmd_create() {
  local name="${1:-}"
  local kind="${2:-}"

  if [ -z "${name}" ] || [ -z "${kind}" ]; then
    echo "Usage: hack/changelog.sh create <name> <kind>"
    echo "  name: filename without extension (e.g. 42-fix-auth)"
    echo "  kind: one of: ${VALID_KINDS}"
    exit 1
  fi

  if ! is_valid_kind "${kind}"; then
    echo "Error: invalid kind \"${kind}\" (must be one of: ${VALID_KINDS})"
    exit 1
  fi

  mkdir -p "${FRAGMENTS_DIR}"
  local filepath="${FRAGMENTS_DIR}/${name}.yaml"

  if [ -f "${filepath}" ]; then
    echo "Error: fragment already exists: ${filepath}"
    exit 1
  fi

  cat > "${filepath}" <<EOF
kind: ${kind}
description: >
  TODO: Describe your change here.
EOF

  echo "Created ${filepath}"
}

cmd_assemble() {
  local version=""
  local draft=false

  while [ $# -gt 0 ]; do
    case "$1" in
      --draft) draft=true; shift ;;
      *) version="$1"; shift ;;
    esac
  done

  if [ -z "${version}" ] && [ "${draft}" = "false" ]; then
    echo "Usage: hack/changelog.sh assemble <version> [--draft]"
    exit 1
  fi

  local files
  files=$(list_fragments "${FRAGMENTS_DIR}")

  if [ -z "${files}" ]; then
    echo "No fragments to assemble."
    return
  fi

  # Validate first
  cmd_validate

  # Build heading
  local heading
  if [ "${draft}" = "true" ]; then
    heading="Unreleased"
  else
    local display_version="${version#v}"
    local today
    today=$(date +%Y-%m-%d)
    heading="[${display_version}] - ${today}"
  fi

  # Build the markdown section
  local section=""
  section="## ${heading}"

  for kind in ${VALID_KINDS}; do
    local kind_items=""

    while IFS= read -r file; do
      [ -z "${file}" ] && continue
      local fkind
      fkind=$("${YQ}" eval '.kind // ""' "${file}")
      if [ "${fkind}" != "${kind}" ]; then
        continue
      fi

      local desc
      desc=$("${YQ}" eval '.description // ""' "${file}")
      desc=$(echo "${desc}" | xargs) # trim + collapse whitespace

      # Ensure trailing punctuation
      case "${desc}" in
        *.|*!|*\?) ;; # already has punctuation
        *) desc="${desc}." ;;
      esac

      # Link PR number if present in filename
      local fname
      fname=$(basename "${file}")
      case "${fname}" in
        [0-9]*)
          local pr_num="${fname%%-*}"
          if [ "${pr_num}" != "0000" ]; then
            desc="${desc} ([#${pr_num}](${REPO_URL}/pull/${pr_num}))"
          fi
          ;;
      esac

      kind_items="${kind_items}
- ${desc}"
    done <<< "${files}"

    if [ -n "${kind_items}" ]; then
      local heading_text
      heading_text=$(kind_heading "${kind}")
      section="${section}

### ${heading_text}
${kind_items}"
    fi
  done

  # Add trailing newline
  section="${section}
"

  # Prepend to changelog
  if [ -f "${CHANGELOG_FILE}" ]; then
    # Find the first ## heading and insert before it
    if grep -qn '^## ' "${CHANGELOG_FILE}"; then
      local line_num
      line_num=$(grep -n '^## ' "${CHANGELOG_FILE}" | head -1 | cut -d: -f1)
      local tmpfile
      tmpfile=$(mktemp)
      head -n $((line_num - 1)) "${CHANGELOG_FILE}" > "${tmpfile}"
      printf '\n%s\n' "${section}" >> "${tmpfile}"
      tail -n +"${line_num}" "${CHANGELOG_FILE}" >> "${tmpfile}"
      mv "${tmpfile}" "${CHANGELOG_FILE}"
    else
      # No existing sections -- append
      printf '%s\n\n%s\n' "$(cat "${CHANGELOG_FILE}")" "${section}" > "${CHANGELOG_FILE}"
    fi
  else
    printf '# Changelog\n\n%s\n' "${section}" > "${CHANGELOG_FILE}"
  fi

  # Delete consumed fragments
  if [ "${draft}" = "false" ]; then
    while IFS= read -r file; do
      [ -z "${file}" ] && continue
      rm -f "${file}"
    done <<< "${files}"
  fi

  local mode="${version}"
  if [ "${draft}" = "true" ]; then
    mode="draft"
  fi

  local count
  count=$(echo "${files}" | grep -c .)
  echo ""
  echo "Assembled ${count} fragment(s) (${mode}) -> ${CHANGELOG_FILE}"
}

# --- Dispatch ---

cmd="${1:-}"
shift || true

case "${cmd}" in
  validate) cmd_validate ;;
  create)   cmd_create "$@" ;;
  assemble) cmd_assemble "$@" ;;
  *)
    echo "Usage: hack/changelog.sh <command>"
    echo ""
    echo "Commands:"
    echo "  validate                    Check that fragments exist and are well-formed"
    echo "  create <name> <kind>        Create a new fragment file"
    echo "  assemble <version>          Assemble fragments into CHANGELOG.md"
    echo "  assemble --draft            Assemble as 'Unreleased' without deleting fragments"
    echo ""
    echo "Valid kinds: ${VALID_KINDS}"
    ;;
esac
