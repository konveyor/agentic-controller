#!/bin/bash
# Setup harness integration test in a Kind cluster.
#
# Prerequisites:
#   - Kind cluster running (make e2e-setup)
#
# Usage:
#   hack/harness-test/setup.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

CONTAINER_TOOL="${CONTAINER_TOOL:-podman}"
KIND_CLUSTER="${KIND_CLUSTER:-agentic-controller-e2e}"

echo "=== Creating secrets ==="

# Vertex AI credentials from local ADC
ADC_PATH="${HOME}/.config/gcloud/application_default_credentials.json"
if [ ! -f "$ADC_PATH" ]; then
    echo "ERROR: ADC file not found at $ADC_PATH"
    echo "Run: gcloud auth application-default login"
    exit 1
fi
kubectl create secret generic vertex-credentials \
    --from-file=GOOGLE_APPLICATION_CREDENTIALS_JSON="$ADC_PATH" \
    --dry-run=client -o yaml | kubectl apply -f -
echo "  vertex-credentials created"

# Git token from gh CLI
GIT_TOKEN=$(gh auth token 2>/dev/null || echo "")
if [ -z "$GIT_TOKEN" ]; then
    echo "ERROR: Could not get GitHub token. Run: gh auth login"
    exit 1
fi
kubectl create secret generic git-credentials \
    --from-literal=token="$GIT_TOKEN" \
    --dry-run=client -o yaml | kubectl apply -f -
echo "  git-credentials created"

echo ""
echo "=== Building agent images ==="
make -C "$REPO_ROOT" agent-images-build CONTAINER_TOOL="$CONTAINER_TOOL"

echo ""
echo "=== Building skill images ==="

SKILL_IMAGE="quay.io/konveyor/skills"
SKILL_DIRS=(plan execute verify javaee-to-quarkus)

for SKILL in "${SKILL_DIRS[@]}"; do
    SKILL_PATH="$REPO_ROOT/skills/$SKILL"
    if [ ! -d "$SKILL_PATH" ]; then
        echo "  WARN: skill dir $SKILL_PATH not found, skipping"
        continue
    fi
    echo "FROM scratch
COPY . /" | $CONTAINER_TOOL build -t "${SKILL_IMAGE}:${SKILL}" -f - "$SKILL_PATH"
    echo "  built ${SKILL_IMAGE}:${SKILL}"
done

echo ""
echo "=== Loading images into Kind ==="

IMAGES=(
    "quay.io/konveyor/agent-plan"
    "quay.io/konveyor/agent-execute"
    "quay.io/konveyor/agent-verify"
)

for IMG in "${IMAGES[@]}"; do
    $CONTAINER_TOOL tag "${IMG}:latest" "${IMG}:dev"
    if [ "$CONTAINER_TOOL" = "podman" ]; then
        $CONTAINER_TOOL save "${IMG}:dev" -o /tmp/agent-image.tar
        KIND_EXPERIMENTAL_PROVIDER=podman kind load image-archive /tmp/agent-image.tar --name "$KIND_CLUSTER"
        rm -f /tmp/agent-image.tar
    else
        kind load docker-image "${IMG}:dev" --name "$KIND_CLUSTER"
    fi
    echo "  loaded ${IMG}:dev"
done

for SKILL in "${SKILL_DIRS[@]}"; do
    if $CONTAINER_TOOL image exists "${SKILL_IMAGE}:${SKILL}" 2>/dev/null; then
        if [ "$CONTAINER_TOOL" = "podman" ]; then
            $CONTAINER_TOOL save "${SKILL_IMAGE}:${SKILL}" -o /tmp/skill-image.tar
            KIND_EXPERIMENTAL_PROVIDER=podman kind load image-archive /tmp/skill-image.tar --name "$KIND_CLUSTER"
            rm -f /tmp/skill-image.tar
        else
            kind load docker-image "${SKILL_IMAGE}:${SKILL}" --name "$KIND_CLUSTER"
        fi
        echo "  loaded ${SKILL_IMAGE}:${SKILL}"
    fi
done

echo ""
echo "=== Applying resources ==="
GCP_PROJECT_ID=$(gcloud config get-value project 2>/dev/null)
if [ -z "$GCP_PROJECT_ID" ]; then
    echo "ERROR: No GCP project set. Run: gcloud config set project <project-id>"
    exit 1
fi
echo "  GCP project: (set)"
sed "s/__GCP_PROJECT_ID__/$GCP_PROJECT_ID/" "$SCRIPT_DIR/resources.yaml" | kubectl apply -f -
TIMESTAMP=$(date +%s)
sed -e "s/__GCP_PROJECT_ID__/$GCP_PROJECT_ID/g" -e "s/__TIMESTAMP__/$TIMESTAMP/g" "$SCRIPT_DIR/playbook-resources.yaml" | kubectl apply -f -
echo "  AgentPlaybookRun: coolstore-migration-$TIMESTAMP"
echo "  Branch: konveyor/playbook-$TIMESTAMP"

echo ""
echo "=== Done ==="
echo "Watch the run: kubectl get agentplaybookrun coolstore-migration-$TIMESTAMP -w"
echo "Check pods:    kubectl get pods"
echo "View logs:     kubectl logs -f coolstore-migration-${TIMESTAMP}-plan -c agent"
