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

## Stream 4 placeholders

Placeholder Containerfiles for the production agent image hierarchy.
These will be implemented as part of Stream 4.

```text
agent-base-goose       Goose runtime (extends future agent-base)
agent-java-goose       JDK 21 + Maven (extends agent-base-goose)
```
