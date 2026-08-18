# Container Images

## agentic-controller-agent

Minimal agent image owned by the controller for verification and
testing. Used by:
- Gateway verification Jobs (connectivity check)
- E2E tests (proves the controller → Sandbox → Pod pipeline)

This is NOT the production agent base image. The real agent images
(harness, goose runtime, language toolchains) are Stream 4 work
tracked in the [agent-base-image-composition enhancement](https://github.com/konveyor/enhancements/pull/296).

```bash
make controller-agent-build                           # build locally
make controller-agent-push CONTAINER_TOOL=podman      # push to quay
```

## Agent images

Production agent image hierarchy. Skills are mounted at runtime via
SkillCards, not baked into images.

```text
agent-base             UBI 10 + goose CLI + git + Python 3 + graphify + harness binary
├── agent-java         + JDK 21, Maven
├── agent-go           + Go toolchain
├── agent-csharp       + .NET SDK
└── agent-nodejs       + Node.js, npm
```

```bash
make agent-images-build                              # build all agent images (native arch)
make agent-images-push CONTAINER_TOOL=podman          # push to quay (native arch)
```

### Multi-arch builds

In CI, all five images (agent-base + the four language images) build for
`linux/amd64` and `linux/arm64` via
[konveyor/release-tools](https://github.com/konveyor/release-tools)
shared `build-push-images.yaml` reusable workflow (see `images.yml`): each
arch builds natively on its own runner and pushes under an arch-suffixed
tag, then a final job assembles those into a manifest list under the real
tag. agent-base publishes first so the language images' `FROM
quay.io/konveyor/agent-base` resolves against an already-published,
genuinely multi-arch manifest.

For local testing without pushing to CI, the same two platforms can be
built with podman directly:

```bash
make agent-images-multiarch-build                    # build both platforms locally, no push
make agent-images-multiarch-push                     # build and push multi-arch manifests to quay
```

These local targets build agent-base first under a `localhost/...` tag
rather than its real quay.io tag — building directly under the real
name would let podman's per-platform `FROM` resolution pull the
already-published (single-arch) image from quay instead of using the
multi-arch manifest just built locally under that same name, silently
baking the wrong architecture into the arm64 build. Real tags are only
attached at push time. The reusable CI workflow above avoids this
altogether by pushing each arch under its own explicit tag before ever
assembling the manifest.

### PR artifacts

On a pull request, images aren't pushed anywhere — instead `images.yml`
builds all five images for both `linux/amd64` and `linux/arm64` and
uploads each as a downloadable per-arch workflow artifact
(`<image>--pr<N>-<arch>`, e.g. `agent-java--pr148-amd64`), via
[konveyor/ci](https://github.com/konveyor/ci)'s shared `build-image`
action (the same one analyzer-lsp's `demo-testing.yml` uses):

1. `agent-base-artifact` builds agent-base per arch and uploads
   `agent-base--pr<N>-<arch>`.
2. `agent-lang-images-artifact` (needs agent-base-artifact) builds each
   language image per arch, downloading and loading the matching
   agent-base artifact as its `BASE_IMAGE` build-arg.

Each per-arch tar is a plain `podman load`-able single-arch image — grab
the one matching your machine's architecture to test it locally.
