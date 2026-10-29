# Build the manager binary.
# Red Hat go-toolset (not upstream golang) is required for FIPS: built with
# CGO_ENABLED=1 + GOEXPERIMENT=strictfipsruntime, the Go crypto routes through
# the system OpenSSL (a FIPS-validated module) instead of Go's built-in crypto.
FROM registry.access.redhat.com/ubi9/go-toolset:1.26 AS builder
# go-toolset defaults to UID 1001; the build writes under /workspace.
USER 0
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
RUN CGO_ENABLED=1 GOEXPERIMENT=strictfipsruntime GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -tags strictfipsruntime -a -o manager ./cmd/main.go

# The skill loader and the SkillCollection enumerator run as short-lived
# containers the controller schedules, so they ship here rather than in the
# agent's image. An agent image is then not required to carry our binary, and
# the KONVEYOR_SKILL_SOURCES contract cannot skew across versions. ADR 0015.
#
# No -a: it shares nearly all of its dependencies with the manager above, which
# has just compiled them into a build cache this RUN can still see. Repeating
# -a here recompiles the standard library and client-go a second time for
# nothing.
RUN CGO_ENABLED=1 GOEXPERIMENT=strictfipsruntime GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -tags strictfipsruntime -o skill-loader ./cmd/skill-loader

# UBI-minimal (not distroless/static) because the FIPS binaries are dynamically
# linked and load libcrypto/libssl at runtime; a static-only base has no OpenSSL.
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
RUN microdnf install -y openssl-libs ca-certificates && microdnf clean all
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/skill-loader /skill-loader
USER 65532:65532

ENTRYPOINT ["/manager"]
