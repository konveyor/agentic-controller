# Migration Harness on OpenShift

## Architecture

```
┌─────────────────────────────────────────────┐
│  Pod: migration-harness                     │
│                                             │
│  /workspace/         ← PVC (mh-workspace)   │
│    coolstore/        ← git clone            │
│                                             │
│  /root/.migration-harness/                  │
│    config            ← ConfigMap            │
│    runs/             ← PVC (mh-outputs)     │
│                                             │
│  /root/.config/goose/                       │
│    config.yaml       ← ConfigMap            │
│                                             │
│  /credentials/                              │
│    gcloud-creds.json ← Secret               │
└─────────────────────────────────────────────┘
```

## Setup

### 1. Push the Container Image

```bash
# Build locally
cd ~/migration-harness
docker build -t migration-harness:latest .

# Tag for OpenShift internal registry
docker tag migration-harness:latest \
  default-route-openshift-image-registry.apps.<cluster>/migration-harness/migration-harness:latest

# Login to registry and push
oc registry login
docker push default-route-openshift-image-registry.apps.<cluster>/migration-harness/migration-harness:latest
```

Or use an external registry (quay.io, ghcr.io) and update the image in `06-pod.yaml`.

### 2. Update Configs

Edit these files before applying:

- **`04-configmap.yaml`** — Set your `MH_MODEL`, `MH_PROVIDER`, and goose config
- **`05-secret.yaml`** — Add your cloud credentials (service account key or API key)
- **`06-pod.yaml`** — Update `GCP_PROJECT_ID`, image reference, resource limits

### 3. Apply Resources

```bash
# Apply all in order
oc apply -f 01-namespace.yaml
oc apply -f 02-pvc-workspace.yaml
oc apply -f 03-pvc-outputs.yaml
oc apply -f 04-configmap.yaml
oc apply -f 05-secret.yaml
oc apply -f 06-pod.yaml
```

Or apply all at once:

```bash
oc apply -f openshift/
```

### 4. Connect to the Pod

```bash
oc rsh -n migration-harness migration-harness
```

### 5. Run a Migration

Inside the pod:

```bash
# Clone repo (stored on PVC, survives restarts)
cd /workspace
git clone https://github.com/konveyor-ecosystem/coolstore.git

# Run migration
migration-harness /workspace/coolstore "Migrate from Java EE to Quarkus"

# Or run individual steps
migration-harness step detect /workspace/coolstore "Migrate from Java EE to Quarkus"
migration-harness step plan /workspace/coolstore "Migrate from Java EE to Quarkus"
migration-harness step execute /workspace/coolstore "Migrate from Java EE to Quarkus"
migration-harness step verify /workspace/coolstore
migration-harness step fix /workspace/coolstore "Migrate from Java EE to Quarkus"
```

## Pod Gets Killed?

No data is lost. Both PVCs retain their data:

```bash
# Re-create the pod (PVCs remain)
oc delete pod migration-harness -n migration-harness
oc apply -f 06-pod.yaml

# Connect and resume
oc rsh -n migration-harness migration-harness
migration-harness resume
```

- `/workspace/coolstore` — repo is still there (on PVC)
- `/root/.migration-harness/runs/` — partial results are still there (on PVC)

## Check Results

From outside the pod:

```bash
# Copy results to local machine
oc cp migration-harness/migration-harness:/root/.migration-harness/runs/ ./results/

# View metrics
oc rsh -n migration-harness migration-harness cat /root/.migration-harness/runs/*/metrics.json
```

## Cleanup

```bash
# Delete pod only (keeps PVCs and data)
oc delete pod migration-harness -n migration-harness

# Delete everything including data
oc delete -f openshift/
```
