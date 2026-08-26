# Build the manager binary
FROM golang:1.26.6 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests (root + api sub-module)
COPY go.mod go.mod
COPY go.sum go.sum
COPY api/go.mod api/go.mod
COPY api/go.sum api/go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build
# the GOARCH has no default value to allow the binary to be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager ./cmd/main.go

# The skill loader and the SkillCollection enumerator run as short-lived
# containers the controller schedules, so they ship here rather than in the
# agent's image. An agent image is then not required to carry our binary, and
# the KONVEYOR_SKILL_SOURCES contract cannot skew across versions. ADR 0015.
#
# No -a: it shares nearly all of its dependencies with the manager above, which
# has just compiled them into a build cache this RUN can still see. Repeating
# -a here recompiles the standard library and client-go a second time for
# nothing.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o skill-loader ./cmd/skill-loader

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/skill-loader /skill-loader
USER 65532:65532

ENTRYPOINT ["/manager"]
