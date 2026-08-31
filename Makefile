# Image URL to use all building/pushing image targets
IMG ?= quay.io/konveyor/agentic-controller:latest
# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." paths="./api/..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./..." paths="./api/..."

.PHONY: generate-adr-index
generate-adr-index: ## Generate the ADR index and reconciliation table from ADR front matter.
	hack/generate-adr-index.sh

.PHONY: verify-adr-index
verify-adr-index: ## Verify that generated ADR indexes match ADR front matter.
	hack/generate-adr-index.sh --check

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

KIND_CLUSTER ?= agentic-controller-e2e

.PHONY: e2e-setup
e2e-setup: ## Create a Kind cluster with Agent Sandbox and deploy the controller.
	hack/start-kind.sh
	hack/setup-e2e.sh

.PHONY: harness-test
harness-test: ## Build, load, and deploy the harness agent in Kind (requires e2e-setup).
	hack/harness-test/setup.sh

.PHONY: e2e-run
e2e-run: ## Run the e2e test (cluster must be set up with e2e-setup).
	hack/run-e2e.sh

.PHONY: e2e-skills
e2e-skills: ## Run the skill delivery scenarios (cluster must be set up with e2e-setup).
	hack/run-e2e-skills.sh

.PHONY: konveyor-install
konveyor-install: ## Install Konveyor from tackle2-operator's Helm chart (idempotent).
	hack/install-konveyor.sh

.PHONY: agent-base-load
agent-base-load: ## Build the agent image and load it into the Kind cluster.
	hack/load-agent-base.sh

.PHONY: e2e-rule
e2e-rule: konveyor-install ## Check a rule reaches the model. Needs agent-base built and loaded.
	hack/run-e2e-rule.sh

.PHONY: e2e
e2e: e2e-setup e2e-run e2e-skills ## Full e2e: create cluster, deploy, test.

.PHONY: e2e-cleanup
e2e-cleanup: ## Tear down the Kind cluster used for e2e tests.
	kind delete cluster --name $(KIND_CLUSTER)

.PHONY: verify-loader-image
verify-loader-image: kustomize ## Check every overlay keeps SKILL_LOADER_IMAGE equal to the manager's image.
	KUSTOMIZE="$(KUSTOMIZE)" hack/verify-loader-image.sh

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

CONTROLLER_AGENT_IMG ?= quay.io/konveyor/agentic-controller-agent:latest
export AGENT_BASE_IMG ?= quay.io/konveyor/agent-base:latest
export AGENT_JAVA_IMG ?= quay.io/konveyor/agent-java:latest
export AGENT_GO_IMG ?= quay.io/konveyor/agent-go:latest
export AGENT_CSHARP_IMG ?= quay.io/konveyor/agent-csharp:latest
export AGENT_NODEJS_IMG ?= quay.io/konveyor/agent-nodejs:latest
# AGENT_PLATFORMS defines the target platforms for multi-arch agent image
# builds (make agent-images-multiarch-build/-push). Limited to what
# agent-base's goose install step resolves: x86_64 and aarch64.
export AGENT_PLATFORMS ?= linux/amd64,linux/arm64

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

# GOOS/GOARCH default to the host toolchain; override to cross-compile, e.g.
# make harness-build GOOS=windows GOARCH=amd64
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

.PHONY: harness-build
harness-build: ## Build the migration-harness binary for local use (GOOS/GOARCH override to cross-compile; the agent images always build it for linux inside their Containerfile).
	mkdir -p harness/bin
	cd harness && CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o bin/migration-harness ./cmd/migration-harness/
	if [ "$(GOOS)" = "windows" ]; then mv harness/bin/migration-harness harness/bin/migration-harness.exe; fi

.PHONY: controller-agent-build
controller-agent-build: ## Build the controller's test/verification agent image.
	$(CONTAINER_TOOL) build -t $(CONTROLLER_AGENT_IMG) -f images/agentic-controller-agent/Containerfile images/agentic-controller-agent/

.PHONY: controller-agent-push
controller-agent-push: controller-agent-build ## Build and push the controller's test/verification agent image.
	$(CONTAINER_TOOL) push $(CONTROLLER_AGENT_IMG)

.PHONY: agent-base-build
agent-base-build: ## Build the base agent image (goose + git + Python + graphify + harness binary).
	$(CONTAINER_TOOL) build -t $(AGENT_BASE_IMG) -f images/agent-base/Containerfile .

.PHONY: agent-java-build
agent-java-build: agent-base-build ## Build the Java agent image (JDK 21 + Maven).
	$(CONTAINER_TOOL) build --build-arg BASE_IMAGE=$(AGENT_BASE_IMG) -t $(AGENT_JAVA_IMG) -f images/agent-java/Containerfile .

.PHONY: agent-go-build
agent-go-build: agent-base-build ## Build the Go agent image.
	$(CONTAINER_TOOL) build --build-arg BASE_IMAGE=$(AGENT_BASE_IMG) -t $(AGENT_GO_IMG) -f images/agent-go/Containerfile .

.PHONY: agent-csharp-build
agent-csharp-build: agent-base-build ## Build the C# agent image (.NET SDK).
	$(CONTAINER_TOOL) build --build-arg BASE_IMAGE=$(AGENT_BASE_IMG) -t $(AGENT_CSHARP_IMG) -f images/agent-csharp/Containerfile .

.PHONY: agent-nodejs-build
agent-nodejs-build: agent-base-build ## Build the Node.js agent image.
	$(CONTAINER_TOOL) build --build-arg BASE_IMAGE=$(AGENT_BASE_IMG) -t $(AGENT_NODEJS_IMG) -f images/agent-nodejs/Containerfile .

.PHONY: agent-images-build
agent-images-build: agent-java-build agent-go-build agent-csharp-build agent-nodejs-build ## Build all agent images.

.PHONY: agent-images-push
agent-images-push: agent-images-build ## Build and push all agent images.
	$(CONTAINER_TOOL) push $(AGENT_BASE_IMG)
	$(CONTAINER_TOOL) push $(AGENT_JAVA_IMG)
	$(CONTAINER_TOOL) push $(AGENT_GO_IMG)
	$(CONTAINER_TOOL) push $(AGENT_CSHARP_IMG)
	$(CONTAINER_TOOL) push $(AGENT_NODEJS_IMG)

# Multi-arch builds use podman's native manifest-list support: build
# --platform + --manifest, then manifest push.
#
# Each image is first built under a "localhost/..." tag, not its real
# quay.io tag. --platform forces podman's pull policy to "newer" for any
# name that resolves to a real registry, so pointing BASE_IMAGE at the real
# quay.io/konveyor/agent-base:latest would silently pull that
# already-published image instead of using the multi-arch manifest just
# built locally under the same name, baking the wrong (or merely stale)
# base into the language build. A "localhost/..." reference has no real
# registry behind it, forcing strictly local, per-platform-correct
# resolution; --pull=never on the language builds makes that explicit. The
# real tags are only ever attached at push time.
AGENT_BASE_LOCALHOST := localhost/agent-base:latest
AGENT_JAVA_LOCALHOST := localhost/agent-java:latest
AGENT_GO_LOCALHOST := localhost/agent-go:latest
AGENT_CSHARP_LOCALHOST := localhost/agent-csharp:latest
AGENT_NODEJS_LOCALHOST := localhost/agent-nodejs:latest

.PHONY: agent-images-multiarch-build
agent-images-multiarch-build: ## Build agent-base + all agent images as multi-arch (linux/amd64,linux/arm64) manifests, without pushing. Requires podman.
	podman manifest rm $(AGENT_BASE_LOCALHOST) >/dev/null 2>&1 || true
	podman rmi $(AGENT_BASE_LOCALHOST) >/dev/null 2>&1 || true
	podman build --platform=$(AGENT_PLATFORMS) --manifest $(AGENT_BASE_LOCALHOST) -f images/agent-base/Containerfile .
	@for entry in "agent-java:$(AGENT_JAVA_LOCALHOST)" "agent-go:$(AGENT_GO_LOCALHOST)" "agent-csharp:$(AGENT_CSHARP_LOCALHOST)" "agent-nodejs:$(AGENT_NODEJS_LOCALHOST)"; do \
		lang=$${entry%%:*}; img=$${entry#*:}; \
		echo "--- $$lang ($$img) ---"; \
		podman manifest rm $$img >/dev/null 2>&1 || true; \
		podman rmi $$img >/dev/null 2>&1 || true; \
		podman build --pull=never --platform=$(AGENT_PLATFORMS) --build-arg BASE_IMAGE=$(AGENT_BASE_LOCALHOST) --manifest $$img -f images/$$lang/Containerfile . || exit 1; \
	done

.PHONY: agent-images-multiarch-push
agent-images-multiarch-push: agent-images-multiarch-build ## Build and push agent-base + all agent images as multi-arch manifests. Requires podman.
	podman manifest push --all $(AGENT_BASE_LOCALHOST) docker://$(AGENT_BASE_IMG)
	podman manifest push --all $(AGENT_JAVA_LOCALHOST) docker://$(AGENT_JAVA_IMG)
	podman manifest push --all $(AGENT_GO_LOCALHOST) docker://$(AGENT_GO_IMG)
	podman manifest push --all $(AGENT_CSHARP_LOCALHOST) docker://$(AGENT_CSHARP_IMG)
	podman manifest push --all $(AGENT_NODEJS_LOCALHOST) docker://$(AGENT_NODEJS_IMG)

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	# The manager refuses to start without this, because the loader init
	# container is on every AgentRun pod. Running from your host, the value has
	# to be an image the *cluster* can pull, not one on this machine.
	SKILL_LOADER_IMAGE="$(IMG)" go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name agentic-controller-builder
	$(CONTAINER_TOOL) buildx use agentic-controller-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm agentic-controller-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Skills

# Skills are ordinary OCI images: a Containerfile, any builder, the same
# signing and mirroring pipeline as every other image we ship. This repo's
# skills travel as one bundle image — the loader detects a bundle by the
# absence of SKILL.md at the image root.
SKILL_IMAGE ?= quay.io/konveyor/skills:latest

.PHONY: skill-build
skill-build: ## Build the skill bundle image.
	$(CONTAINER_TOOL) build -t $(SKILL_IMAGE) -f catalog/Containerfile catalog

.PHONY: skill-push
skill-push: skill-build ## Build and push the skill bundle image.
	$(CONTAINER_TOOL) push $(SKILL_IMAGE)

# A skill is one directory deep, so a directory of directories is not itself a
# bundle and its contents are not seen. Each tree is therefore named: the
# shipped bundle (catalog/skills), the single-skill worked examples
# (catalog/examples), and the repo's own maintainer workflow skills (skills).
SKILL_TREES ?= catalog/skills catalog/examples skills

.PHONY: skill-validate
skill-validate: ## Check every SKILL.md frontmatter parses and is complete.
	@for tree in $(SKILL_TREES); do \
		echo "Validating $${tree}" ;\
		go run ./cmd/skill-loader validate "$${tree}" || exit 1 ;\
	done

##@ Changelog

.PHONY: changelog-validate
changelog-validate: yq ## Validate changelog fragments are well-formed.
	YQ="$(YQ)" hack/changelog.sh validate

.PHONY: changelog-create
changelog-create: yq ## Create a changelog fragment. Usage: make changelog-create NAME=42-fix-auth KIND=bugfix
	YQ="$(YQ)" hack/changelog.sh create "$(NAME)" "$(KIND)"

.PHONY: changelog-assemble
changelog-assemble: yq ## Assemble changelog fragments into CHANGELOG.md. Usage: make changelog-assemble VERSION=v0.1.0
	YQ="$(YQ)" hack/changelog.sh assemble "$(VERSION)"

.PHONY: changelog-draft
changelog-draft: yq ## Assemble changelog fragments as "Unreleased" without deleting fragments.
	YQ="$(YQ)" hack/changelog.sh assemble --draft

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
YQ ?= $(LOCALBIN)/yq

## Tool Versions
YQ_VERSION ?= v4.45.4
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.21.0

#ENVTEST_VERSION is the controller-runtime version to use for setup-envtest, derived from go.mod
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v")

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.12.2
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: yq
yq: $(YQ) ## Download yq locally if necessary.
$(YQ): $(LOCALBIN)
	$(call go-install-tool,$(YQ),github.com/mikefarah/yq/v4,$(YQ_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		echo "Building custom golangci-lint with plugins..." && \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
