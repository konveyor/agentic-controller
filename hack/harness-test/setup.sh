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

# Hub token (JWT signed with default key "tackle")
HUB_KEY="${HUB_KEY:-tackle}"
EXP=$(( $(date +%s) + 86400 ))
HEADER_B64=$(printf '{"typ":"JWT","alg":"HS512"}' | base64 | tr -d '=' | tr '+/' '-_' | tr -d '\n')
PAYLOAD_B64=$(printf '{"sub":"admin","scope":"*:*","exp":%d}' "$EXP" | base64 | tr -d '=' | tr '+/' '-_' | tr -d '\n')
SIGNATURE=$(printf '%s.%s' "$HEADER_B64" "$PAYLOAD_B64" | openssl dgst -sha512 -hmac "$HUB_KEY" -binary | base64 | tr -d '=' | tr '+/' '-_' | tr -d '\n')
HUB_TOKEN="${HEADER_B64}.${PAYLOAD_B64}.${SIGNATURE}"
echo "  hub token generated (expires in 24h)"

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
    "quay.io/konveyor/agent-base"
    "quay.io/konveyor/agent-java"
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
    if $CONTAINER_TOOL image inspect "${SKILL_IMAGE}:${SKILL}" >/dev/null 2>&1; then
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
sed -e "s/__GCP_PROJECT_ID__/$GCP_PROJECT_ID/g" \
    -e "s/__TIMESTAMP__/$TIMESTAMP/g" \
    -e "s|__HUB_TOKEN__|$HUB_TOKEN|g" \
    "$SCRIPT_DIR/playbook-resources.yaml" | kubectl apply -f -
echo "  AgentPlaybookRun: coolstore-migration-$TIMESTAMP"

echo ""
echo "=== Done ==="
echo "Watch the run: kubectl get agentplaybookrun coolstore-migration-$TIMESTAMP -w"
echo "Check pods:    kubectl get pods"
echo "View logs:     kubectl logs -f coolstore-migration-${TIMESTAMP}-plan -c agent"
