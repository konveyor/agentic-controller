#!/bin/bash
# Setup harness integration test in a Kind cluster.
#
# Prerequisites:
#   - Kind cluster running (make e2e-setup)
#   - agent-base-goose-java image loaded into Kind
#
# Usage:
#   hack/harness-test/setup.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

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
echo "=== Loading harness image into Kind ==="
CONTAINER_TOOL="${CONTAINER_TOOL:-podman}"
KIND_CLUSTER="${KIND_CLUSTER:-agentic-controller-e2e}"

make -C "$REPO_ROOT" agent-java-goose-build CONTAINER_TOOL="$CONTAINER_TOOL"

if [ "$CONTAINER_TOOL" = "podman" ]; then
    $CONTAINER_TOOL save localhost/agent-base-goose-java:dev -o /tmp/harness-image.tar
    kind load image-archive /tmp/harness-image.tar --name "$KIND_CLUSTER"
    rm -f /tmp/harness-image.tar
else
    kind load docker-image localhost/agent-base-goose-java:dev --name "$KIND_CLUSTER"
fi
echo "  image loaded"

echo ""
echo "=== Applying resources ==="
GCP_PROJECT_ID=$(gcloud config get-value project 2>/dev/null)
if [ -z "$GCP_PROJECT_ID" ]; then
    echo "ERROR: No GCP project set. Run: gcloud config set project <project-id>"
    exit 1
fi
echo "  GCP project: $GCP_PROJECT_ID"
sed "s/__GCP_PROJECT_ID__/$GCP_PROJECT_ID/" "$SCRIPT_DIR/resources.yaml" | kubectl apply -f -

echo ""
echo "=== Done ==="
echo "Watch the run: kubectl get agentrun coolstore-migration -w"
echo "Check pods:    kubectl get pods"
echo "View logs:     kubectl logs -f coolstore-migration -c agent"
