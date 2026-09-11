#!/usr/bin/env bash
# Install Konveyor, so the rule e2e has a Hub the harness can resolve from.
#
# This clones tackle2-operator and runs its Helm chart rather than applying
# manifests of our own. Hub's deployment, its RBAC and the tackle.konveyor.io
# CRDs are the operator's to define, and a copy here would be a second
# definition to drift.
#
# Environment:
#   OPERATOR_REF    tackle2-operator commit to install (pinned below)
#   KONVEYOR_NS     namespace to install into (default konveyor-tackle)
#   AUTH_REQUIRED   "true" to install with Hub authentication on (default false)
#   TIMEOUT         how long to wait for Hub (default 600s)

set -euo pipefail

# Pinned rather than tracking main, so an unrelated operator change cannot
# break this repo's CI. Bump it deliberately.
OPERATOR_REF="${OPERATOR_REF:-d57baa682a96be8ae329914e6e44e5271bba44a4}"
KONVEYOR_NS="${KONVEYOR_NS:-konveyor-tackle}"
AUTH_REQUIRED="${AUTH_REQUIRED:-false}"
TIMEOUT="${TIMEOUT:-600s}"

if kubectl -n "${KONVEYOR_NS}" get deployment tackle-hub >/dev/null 2>&1; then
    echo "Konveyor already installed in ${KONVEYOR_NS}"
else
    echo "=== Installing Konveyor from tackle2-operator ${OPERATOR_REF:0:12} ==="
    OPERATOR_DIR=$(mktemp -d)
    trap 'rm -rf "${OPERATOR_DIR}"' EXIT

    git init -q "${OPERATOR_DIR}"
    git -C "${OPERATOR_DIR}" remote add origin https://github.com/konveyor/tackle2-operator.git
    git -C "${OPERATOR_DIR}" fetch -q --depth 1 origin "${OPERATOR_REF}"
    git -C "${OPERATOR_DIR}" checkout -q FETCH_HEAD

    kubectl create namespace "${KONVEYOR_NS}" --dry-run=client -o yaml | kubectl apply -f -
    helm upgrade --install konveyor "${OPERATOR_DIR}/helm" \
        --namespace "${KONVEYOR_NS}" \
        --wait --timeout "${TIMEOUT}"

    # The operator reconciles this into Hub and its database. Auth defaults to
    # off because the e2e only resolves an application and tests no tokens;
    # AUTH_REQUIRED=true turns on Hub's built-in OIDC (seeded login admin/admin).
    kubectl -n "${KONVEYOR_NS}" apply -f - <<EOF
apiVersion: tackle.konveyor.io/v1alpha1
kind: Tackle
metadata:
  name: tackle
spec:
  feature_auth_required: "${AUTH_REQUIRED}"
EOF
fi

echo "=== Waiting for Hub ==="
# The Deployment does not exist until the operator has reconciled the CR, so
# wait for it to appear before waiting on its condition.
for _ in $(seq 1 120); do
    kubectl -n "${KONVEYOR_NS}" get deployment tackle-hub >/dev/null 2>&1 && break
    sleep 5
done
kubectl -n "${KONVEYOR_NS}" rollout status deployment/tackle-hub --timeout="${TIMEOUT}"

# Hub serves the agentic API (/hub/agentic/*) by reading konveyor.io resources
# from its own namespace, and the operator does not yet grant its ServiceAccount
# access to them. Without this Role every agentic page in the UI 500s with
# "... is forbidden ... in the namespace ${KONVEYOR_NS}". The manifests default
# to konveyor-tackle; substitute the target namespace (RBAC requires it on the
# ServiceAccount subject, so a plain -n is not enough).
echo "=== Granting Hub access to agentic resources in ${KONVEYOR_NS} ==="
kubectl kustomize "$(dirname "$0")/../config/hub-rbac" \
    | sed "s/konveyor-tackle/${KONVEYOR_NS}/g" \
    | kubectl apply -f -

echo "Hub is at http://tackle-hub.${KONVEYOR_NS}.svc:8080"
