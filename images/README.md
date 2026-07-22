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

## Stage agent images

Production agent image hierarchy for migration stages. Skills are
mounted at runtime via SkillCards, not baked into images.

```text
agent-base             UBI 10 + goose CLI + git + harness binary
├── agent-plan         + Python 3, graphify
├── agent-java-base    + JDK 21, Maven
│   ├── agent-execute-java  (inherits java-base)
│   └── agent-verify-java   (inherits java-base)
```

```bash
make agent-images-build                              # build all stage images
make agent-images-push CONTAINER_TOOL=podman          # push to quay
```
