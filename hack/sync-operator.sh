#!/usr/bin/env bash
# Render this repo's operator-facing manifests into a checkout of
# konveyor/operator (a.k.a. tackle2-operator).
#
# The operator ships the agentic controller as an operand: its CRDs, its RBAC,
# and the curated default content (the skill catalog, the stage Agents, and the
# AgentWorkflow the UI runs). Those artifacts are defined HERE and copied there
# -- a hand-maintained second copy would drift. This script is that copy step,
# run by .github/workflows/sync-operator.yml on every merge and runnable
# locally to preview the result.
#
# What it renders:
#   CRDs       config/crd/bases/konveyor.io_*.yaml -> helm/templates/crds/ (verbatim)
#   RBAC       config/rbac/{role,leader_election_role,metrics_reader_role}.yaml -> helm/templates/rbac/
#                agentic_{cluster_role,leader_election_role,metrics_reader_role}.yaml; metadata.name
#                renamed to avoid colliding with the operator's own roles
#   operands   config/operator/*.yaml.j2 -> roles/tackle/templates/agentic/ (verbatim)
#   defaults   config/defaults/*.yaml -> roles/tackle/templates/agentic/defaults/ (verbatim)
#
# It does NOT run `make bundle`; the workflow does that in the operator checkout
# so the ClusterServiceVersion reflects any CRD/RBAC change. The RBAC binding and
# ServiceAccount files are operator-authored (they reference the operator's
# release namespace and SA name) and are intentionally left alone. The controller
# roles and metrics-reader role are re-rendered.
#
# Usage: hack/sync-operator.sh <path-to-operator-checkout>

set -euo pipefail

OPERATOR="${1:?usage: hack/sync-operator.sh <path-to-operator-checkout>}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ ! -d "${OPERATOR}/helm" || ! -d "${OPERATOR}/roles/tackle" ]]; then
  echo "error: ${OPERATOR} does not look like a konveyor/operator checkout" >&2
  exit 1
fi

crd_dir="${OPERATOR}/helm/templates/crds"
rbac_dir="${OPERATOR}/helm/templates/rbac"
agentic_dir="${OPERATOR}/roles/tackle/templates/agentic"
defaults_dir="${agentic_dir}/defaults"

# CRDs -- verbatim copy of every konveyor.io CRD. Clear our own CRDs first (by
# the konveyor.io_ prefix, so other operators' CRDs in this dir are untouched)
# so a renamed or removed CRD does not leave a stale file behind, matching the
# defaults sync below.
echo "==> CRDs -> ${crd_dir}"
mkdir -p "${crd_dir}"
rm -f "${crd_dir}"/konveyor.io_*.yaml
cp "${REPO_ROOT}"/config/crd/bases/konveyor.io_*.yaml "${crd_dir}/"

# RBAC roles -- rename metadata.name and strip the kustomize labels block, then
# prepend a banner so nobody hand-edits the generated file. This is a purely
# textual edit (not a YAML reserialization), so the rules stay byte-for-byte
# identical to the source: the synced file only changes when the rules do.
render_role() {
  local src="$1" dst="$2" oldname="$3" newname="$4" header="$5"
  {
    printf -- '---\n%s\n' "${header}"
    # Purely textual transform, done in one awk pass so it is robust to where
    # controller-gen places metadata keys:
    #   - drop everything above `apiVersion:` (the source's --- and comments);
    #   - drop a `metadata.labels:` block wherever it sits -- the `  labels:`
    #     line and its more-indented children, stopping at the next key indented
    #     two spaces or less, so the rules body is never touched even if labels
    #     were to follow `name:`;
    #   - rename metadata.name to avoid colliding with the operator's own role.
    awk -v oldname="${oldname}" -v newname="${newname}" '
      !started { if ($0 ~ /^apiVersion:/) started = 1; else next }
      inlabels { if ($0 ~ /^   /) next; inlabels = 0 }
      $0 == "  labels:" { inlabels = 1; next }
      $0 == "  name: " oldname { print "  name: " newname; next }
      { print }
    ' "${src}"
  } > "${dst}"
  # Guard the exact-match rename above: if controller-gen ever reformats the
  # metadata block (different indentation, quoting, or key order) the awk rule
  # silently passes the source through unchanged, which would ship the operator's
  # own role name -- a collision -- with no error. Fail loudly instead. (We keep
  # the textual transform rather than reserializing with yq so the rules stay
  # byte-for-byte identical to the source; this check restores the safety yq
  # would have given.)
  if ! grep -qx "  name: ${newname}" "${dst}"; then
    echo "error: RBAC rename '${oldname}' -> '${newname}' did not apply to ${src##*/};" \
         "controller-gen output format may have changed -- update render_role in $(basename "$0")." >&2
    exit 1
  fi
}

echo "==> RBAC -> ${rbac_dir}"
mkdir -p "${rbac_dir}"
render_role \
  "${REPO_ROOT}/config/rbac/role.yaml" \
  "${rbac_dir}/agentic_cluster_role.yaml" \
  "manager-role" \
  "agentic-controller-manager-role" \
  "# Synced from konveyor/agentic-controller config/rbac/role.yaml (manager-role).
# metadata.name is renamed to avoid colliding with the operator's own
# manager-role. Rules are otherwise verbatim; do not hand-edit -- update by
# re-syncing from agentic-controller."
render_role \
  "${REPO_ROOT}/config/rbac/leader_election_role.yaml" \
  "${rbac_dir}/agentic_leader_election_role.yaml" \
  "leader-election-role" \
  "agentic-controller-leader-election-role" \
  "# Synced from konveyor/agentic-controller config/rbac/leader_election_role.yaml
# (leader-election-role), renamed to avoid collisions. Permits the controller's
# leader election. Do not hand-edit -- update by re-syncing."

# Keep this install-scoped role name aligned with the operator-authored reader
# ServiceAccount and binding. agentic_component_name is a per-Tackle Ansible
# variable, not a Helm/OLM RBAC resource name.
render_role \
  "${REPO_ROOT}/config/rbac/metrics_reader_role.yaml" \
  "${rbac_dir}/agentic_metrics_reader_role.yaml" \
  "metrics-reader" \
  "agentic-controller-metrics-reader" \
  "# Synced from konveyor/agentic-controller config/rbac/metrics_reader_role.yaml.
# Renamed to avoid colliding with another metrics-reader role."

# Copying templates does not install their resources. When this adds variables
# or operands, the resulting operator PR must also add role defaults and lifecycle
# tasks. In particular, reconcile the metrics Service/credentials before applying
# the Deployment that requires the service-serving certificate Secret.
# Controller operand templates -- verbatim copies. They carry operator-specific
# Jinja ({{ agentic_fqin }}, namespace, labels, SA) that a reserialization would
# reformat, so we maintain it as a first-class artifact here and copy it as-is.
echo "==> deployment -> ${agentic_dir}"
mkdir -p "${agentic_dir}"
cp "${REPO_ROOT}"/config/operator/*.yaml.j2 "${agentic_dir}/"

# Default content -- verbatim copy, minus the kustomization (the operator applies
# these through its Ansible role, not kustomize). The directory is ours alone, so
# clear it first: a default removed here must disappear there too.
echo "==> defaults -> ${defaults_dir}"
mkdir -p "${defaults_dir}"
rm -f "${defaults_dir}"/*.yaml
for f in "${REPO_ROOT}"/config/defaults/*.yaml; do
  [[ "$(basename "$f")" == "kustomization.yaml" ]] && continue
  cp "$f" "${defaults_dir}/"
done

echo "==> done"
