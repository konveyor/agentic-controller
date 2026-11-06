#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
    echo "Usage: $0 /path/to/lightspeed-agentic-sandbox [local-image-tag]" >&2
    exit 2
fi
runtime_dir=$(cd "$1" && pwd)
repo_dir=$(cd "$(dirname "$0")/../.." && pwd)
image=${2:-localhost/agent-lightspeed:prototype}
engine=${CONTAINER_TOOL:-podman}
runtime_revision=$(git -C "$runtime_dir" rev-parse HEAD)
controller_revision=$(git -C "$repo_dir" rev-parse HEAD)
if [[ -n $(git -C "$runtime_dir" status --porcelain --untracked-files=normal) ]]; then
    echo "Commit runtime changes before building so its source revision identifies the image." >&2
    exit 1
fi
if [[ -n $(git -C "$repo_dir" status --porcelain --untracked-files=normal -- harness api) ]]; then
    echo "Commit harness and API changes before building so their source revision identifies the image." >&2
    exit 1
fi
"$engine" build --build-context "lightspeed=$runtime_dir" \
    --label "io.konveyor.lightspeed.revision=$runtime_revision" \
    --label "io.konveyor.controller.revision=$controller_revision" \
    -f "$repo_dir/images/agent-lightspeed/Containerfile" -t "$image" "$repo_dir"
