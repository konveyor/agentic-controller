# Container Images

For end-to-end authoring instructions, see
[Creating agentic base images](../docs/agent-base-images.md) and
[Creating SkillCard images](../docs/skill-card-images.md).

## agentic-controller-agent

Minimal agent image owned by the controller for verification and
testing. Used by:
- Gateway verification Jobs (connectivity check)
- E2E tests (proves the controller → Sandbox → Pod pipeline)

This is NOT the production agent base image. The real agent images
(harness, Lightspeed runtime, language toolchains) are Stream 4 work
tracked in the [agent-base-image-composition enhancement](https://github.com/konveyor/enhancements/pull/296).

```bash
make controller-agent-build                           # build locally
make controller-agent-push CONTAINER_TOOL=podman      # push to quay
```

## Agent images

Production agent image hierarchy. Skills are mounted at runtime via
SkillCards, not baked into images.

```text
agent-base             UBI 10 + Lightspeed + git + harness binary
├── agent-java         + JDK 21, Maven
├── agent-go           + Go toolchain
├── agent-csharp       + .NET SDK
└── agent-nodejs       + Node.js, npm
```

```bash
make agent-images-build                              # build all agent images (native arch)
make agent-images-push CONTAINER_TOOL=podman          # push to quay (native arch)
```

`agent-images-push` pushes single-arch images under the same `:latest`
tags CI's multi-arch build publishes — running it against the real quay
repos overwrites the multi-arch manifest with a single-arch image. Use it
only for scratch/dev registries; for quay, use the multi-arch targets
below.

The `controller` image is amd64-only, as is `agentic-controller-agent` —
the agentic stack targets amd64 Sandbox clusters, so "multi-arch" among
the runtime images covers only the agent-base + language image hierarchy.
The skill bundle is multi-arch too, for a different reason: it carries no
binary at all, and a SkillCard pointing at it has to pull wherever the
cluster runs.

### Publishing (versioned)

Publishing lives in `image-build-push.yml`, a dedicated workflow with no
`paths:` filter. This is separate from `images.yml` because GitHub gates
`push` events on both `tags:`/`branches:` and `paths:` — tag pushes would
not reliably fire under a `paths:` filter. The workflow triggers on pushes
to `main`, `release-*` branches, and `v*` tags, and publishes every shipped
operand image via
[konveyor/release-tools](https://github.com/konveyor/release-tools)'
shared `build-push-images.yaml` reusable workflow:

- **`agentic-controller`** — the controller (amd64).
- **`agent-base` + the four language images** — multi-arch (`linux/amd64`
  and `linux/arm64`): each arch builds natively on its own runner and pushes
  under an arch-suffixed tag, then a final job assembles those into a
  manifest list under the real tag. agent-base publishes first so the
  language images' `FROM quay.io/konveyor/agent-base` resolves against an
  already-published, genuinely multi-arch manifest.
- **`skills`** — the skill bundle (`linux/amd64` and `linux/arm64`;
  `FROM scratch` plus text, so each arch is the same content under a
  manifest list), built from `catalog/` (build
  context `catalog/`, `catalog/Containerfile`), gated on `make skill-validate`
  so an unusable skill never ships. Everything under `catalog/skills/` ships;
  the worked examples in `catalog/examples/` and the repo's maintainer
  workflow skills in `skills/` are outside that context and never ship
  (see ADR 0019).

The tag is derived from the ref by the reusable workflow:

- **`main`** pushes tag images as `:latest`.
- **`v*`** tag pushes tag images with the version (e.g. `:v0.11.0`) —
  derived from `github.ref_name`.
- **`release-*`** branch pushes tag images with the branch name.

We publish tags only; the operator converts a tag to a `@sha256` digest at
bundle time.

`images.yml` keeps the `paths:`-filtered PR artifact builds, including a
controller archive, and the `controller-agent` build+push.
`skills.yml` keeps the PR-time skill validate + build check.

### Multi-arch (agent images)

The agent-base + language images build for `linux/amd64` and `linux/arm64`
as described above.

For local testing without pushing to CI, the same two platforms can be
built with podman directly (Linux hosts need `qemu-user-static` installed
for cross-arch emulation; macOS's podman machine ships with it):

```bash
make agent-images-multiarch-build                    # build both platforms locally, no push
make agent-images-multiarch-push                     # build and push multi-arch manifests to quay
```

These local targets build agent-base first under a `localhost/...` tag
rather than its real quay.io tag — `--platform` forces podman's pull
policy to "newer" for any name that resolves to a real registry, so
building directly under the real name would let podman silently pull the
already-published image from quay instead of using the multi-arch
manifest just built locally under that same name. Real tags are only
attached at push time, and the language builds also pass `--pull=never`
to make the local-only intent explicit. The reusable CI workflow above
avoids this altogether by pushing each arch under its own explicit tag
before ever assembling the manifest.

### PR artifacts

On a pull request, images aren't pushed anywhere — instead `images.yml`
builds all five images for both `linux/amd64` and `linux/arm64` and
uploads each as a downloadable per-arch workflow artifact
(`<image>--pr<N>-<arch>`, e.g. `agent-java--pr148-amd64`), via
[konveyor/ci](https://github.com/konveyor/ci)'s shared `build-image`
action (the same one analyzer-lsp's `demo-testing.yml` uses,
transitively, via `e2e-image-build.yaml`):

1. `agent-base-artifact` builds agent-base per arch and uploads
   `agent-base--pr<N>-<arch>`.
2. `agent-lang-images-artifact` (needs agent-base-artifact) builds each
   language image per arch, downloading and loading the matching
   agent-base artifact as its `BASE_IMAGE` build-arg.

Each per-arch tar is a plain `podman load`-able single-arch image (`docker
load` works too) — grab the one matching your machine's architecture to
test it locally; it loads as `localhost/<image>:pr<N>-<arch>`. The shared action
expires these artifacts after 1 day; download them before expiry. The controller
archive (`agentic-controller--pr<N>-amd64`) is retained for 7 days.


The base image installs the Lightspeed source revision pinned by
`LIGHTSPEED_REVISION` in `images/agent-base/Containerfile` into
`/opt/lightspeed`, with dependencies from its frozen lockfile. The `openai`
and `acp` extras include the DeepAgents provider integrations for OpenAI,
Anthropic, Bedrock, and Vertex. Every native image build imports those
integrations and the ACP bridge as UID 1001, without model credentials.
The Red Hat Go toolset continues to build the harness with strict FIPS
runtime support; that alone does not qualify all Python dependencies for FIPS.

PR builds label the base and inherited language images with the actual
checkout revision (`org.opencontainers.image.revision`), PR head
(`io.konveyor.controller.pr-head-revision`), and pinned runtime revision
(`io.konveyor.lightspeed.revision`). The checkout normally represents
GitHub's test merge commit, so it can differ from the PR head. CI verifies
these labels for each image and records the revisions and archive checksums
in its job summaries. Language images consume the matching base archive
from the same workflow run; stale fallback images fail provenance checks.

Download all required archives from a successful **Build and Push Images**
run for the intended PR head. For example, for an amd64 cluster:

```bash
gh run download <run-id> --repo konveyor/agentic-controller \
  --name agent-base--pr345-amd64 --dir /tmp/agent-images
podman load -i /tmp/agent-images/agent-base--pr345-amd64.tar
```

Load the imported image into Kind or minikube using its normal image-loading
mechanism; `hack/lib/cluster.sh` also supports a `LOAD_IMAGE_HOOK`. Use Docker
instead when required by that cluster's container provider. For a remote
cluster, tag and push the unchanged GitHub-built image to a registry the
cluster can pull from, using the registry access supplied for the demo.
These steps transfer the built image rather than rebuilding it locally. Use
an explicit tag and `IfNotPresent` when an image is available only on nodes.
PR artifact creation needs no registry credentials and performs no registry
publication. The workflow has no draft-PR exclusion; a draft can produce
these archives. Workflow dispatch builds do not produce PR archives.
