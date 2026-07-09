# Running Migration Harness in Docker

This guide explains how to run migration-harness in a Docker container for reproducibility and portability.

## Quick Start

```bash
# 1. Build the image
docker build -t migration-harness:latest .

# 2. Run interactive container
docker run --rm -it \
  -v ~/.config/goose:/root/.config/goose:ro \
  -v ~/.config/gcloud:/root/.config/gcloud:ro \
  -e GOOGLE_APPLICATION_CREDENTIALS=/root/.config/gcloud/application_default_credentials.json \
  -e GCP_PROJECT_ID=your-project-id \
  -e GCP_LOCATION=us-east5 \
  --entrypoint bash \
  migration-harness:latest

# Inside the container:
migration-harness init
cd /workspace
git clone https://github.com/konveyor-ecosystem/coolstore.git
migration-harness /workspace/coolstore "Migrate from Java EE to Quarkus"
```

**Note:** Results will be at `/root/.migration-harness/runs/` inside the container but will be **lost when you exit** unless you also mount an output directory with `-v ~/.migration-harness/runs:/root/.migration-harness/runs:rw`.

---

## Prerequisites

### 1. Install Docker
- Docker Desktop (Mac/Windows): https://www.docker.com/products/docker-desktop
- Docker Engine (Linux): https://docs.docker.com/engine/install/

### 2. Install Goose on Host Machine
You need goose installed on your **host machine** to create the initial configuration:
```bash
# Mac (Homebrew)
brew install --cask block-goose

# Linux
curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh | bash
```

Then configure goose:
```bash
goose configure
# OR manually create ~/.config/goose/config.yaml
```

### 3. Authenticate with Cloud Provider (if using Vertex AI)
```bash
# Google Cloud
gcloud auth application-default login
```

---

## Usage

### Interactive Container (Recommended)

Start an interactive shell inside the container:

```bash
docker run --rm -it \
  -v ~/.config/goose:/root/.config/goose:ro \
  -v ~/.config/gcloud:/root/.config/gcloud:ro \
  -e GOOGLE_APPLICATION_CREDENTIALS=/root/.config/gcloud/application_default_credentials.json \
  -e GCP_PROJECT_ID=itpc-gcp-hcm-pe-eng-claude \
  -e GCP_LOCATION=us-east5 \
  --entrypoint bash \
  migration-harness:latest
```

**Once inside the container:**

```bash
# 1. Initialize migration-harness config
migration-harness init

# 2. Clone your repository
cd /workspace
git clone https://github.com/your-org/coolstore.git

# 3. Run migration
migration-harness /workspace/coolstore "Migrate from Java EE to Quarkus"
```

**Note:** Results will be at `/root/.migration-harness/runs/` inside the container but will be **lost when you exit** unless you also mount an output directory. To persist results, add:
```bash
-v ~/.migration-harness/runs:/root/.migration-harness/runs:rw \
```

---

### Direct Migration (Non-Interactive)

Run a migration directly with a repository already on your host:

```bash
docker run --rm -it \
  -v ~/coolstore:/workspace/repo:rw \
  -v ~/.migration-harness/config:/root/.migration-harness/config:ro \
  -v ~/.config/goose:/root/.config/goose:ro \
  -v ~/.migration-harness/runs:/root/.migration-harness/runs:rw \
  -v ~/.config/gcloud:/root/.config/gcloud:ro \
  -e GOOGLE_APPLICATION_CREDENTIALS=/root/.config/gcloud/application_default_credentials.json \
  -e GCP_PROJECT_ID=itpc-gcp-hcm-pe-eng-claude \
  -e GCP_LOCATION=us-east5 \
  migration-harness:latest \
  /workspace/repo "Migrate from Java EE to Quarkus"
```

---

## Running Individual Steps

You can run migration steps individually for more control. This is useful for:
- Debugging specific steps
- Re-running a step after manual fixes
- Inspecting intermediate outputs

**Inside the container:**

```bash
# Step 1: Detect (analyzes code structure, builds graph)
migration-harness step detect /workspace/coolstore "Migrate from Java EE to Quarkus"

# Step 2: Plan (generates migration plan with human approval)
migration-harness step plan /workspace/coolstore "Migrate from Java EE to Quarkus"

# Step 3: Execute (transforms code according to plan)
migration-harness step execute /workspace/coolstore "Migrate from Java EE to Quarkus"

# Step 4: Verify (builds and tests)
migration-harness step verify /workspace/coolstore

# Step 5: Fix (auto-fixes compilation errors)
migration-harness step fix /workspace/coolstore "Migrate from Java EE to Quarkus"
```

**Check status and resume:**

```bash
# Show summary of last run
migration-harness status

# Resume incomplete run
migration-harness resume
```

**Important Notes:**

1. **Run directory**: Steps use the latest run directory at `/root/.migration-harness/runs/`. The detect step creates a new run directory, subsequent steps reuse it.

2. **Step dependencies**: 
   - `detect` → creates graph and manifest
   - `plan` → needs graph from detect
   - `execute` → needs plan from plan step
   - `verify` → needs executed code
   - `fix` → needs verification errors

3. **Intermediate outputs**: Each step writes to the run directory:
   - `detect.json` - project structure analysis
   - `graph.json` - code graph
   - `PLAN.md` - migration plan
   - `execution-log.md` - execution log with lessons
   - `verification-report.md` - build/test results
   - `metrics.json` - final metrics

---

## Debugging

### Interactive Shell in Container
```bash
docker run --rm -it \
  -v "$(pwd)/coolstore:/workspace/repo:rw" \
  --entrypoint /bin/bash \
  migration-harness:latest
```

Inside the container:
```bash
# Check installed tools
java -version
python3 --version
node --version
dotnet --version
goose --version
mvn --version

# Run migration manually
cd /workspace/repo
migration-harness . "Migrate from Java EE to Quarkus"
```

### View Container Logs
```bash
docker logs migration-harness
```

### Check Goose Config in Container
```bash
docker run --rm -it \
  -v "$HOME/.config/goose:/root/.config/goose:ro" \
  --entrypoint /bin/bash \
  migration-harness:latest \
  -c "cat /root/.config/goose/config.yaml"
```

---

## Next Steps

- Push image to registry: `docker push myregistry.com/migration-harness:latest`
- Set up automated builds with GitHub Actions
- Create language-specific variants (java-only, dotnet-only, etc.)
