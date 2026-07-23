# Container Images

## agentic-controller-agent

Minimal agent image owned by the controller for verification and
testing. Used by:
- LLMProvider verification Jobs (connectivity check)
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
make agent-images-build                              # build all agent images
make agent-images-push CONTAINER_TOOL=podman          # push to quay
```
