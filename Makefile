.PHONY: all build-bink build-bink-image build-cluster-image build-dns-image clean rebuild help test-integration test-integration-quick update-calico check-license

# Extract image names and versions from internal/config/defaults.go (single source of truth)
DEFAULTS_GO := internal/config/defaults.go
extract = $(shell grep '$(1)' $(DEFAULTS_GO) | head -1 | sed 's/.*"\(.*\)"/\1/')

BINK_IMAGE := $(call extract,binkImageBase ):latest
CLUSTER_IMAGE := $(call extract,clusterImageBase ):latest
DNS_IMAGE := $(call extract,dnsImageBase ):latest
FEDORA_VERSION := $(call extract,FedoraVersion )

# Binary
BINK_BINARY := bink

# Version info (overridable, e.g. make build-bink VERSION=v0.1.0)
VERSION ?= dev
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/bootc-dev/bink/internal/version
LDFLAGS := -X $(VERSION_PKG).Version=$(VERSION) \
           -X $(VERSION_PKG).GitCommit=$(GIT_COMMIT) \
           -X $(VERSION_PKG).BuildDate=$(BUILD_DATE)

# Build tags (auto-detect optional C dependencies)
BUILDTAGS ?= \
	$(shell hack/btrfs_installed_tag.sh)

# Directories
VM_DIR := containerfiles/cluster-image

all: build-bink

# Build the bink CLI binary
build-bink:
	@echo "=== Building bink CLI binary ==="
	go build -tags "$(BUILDTAGS)" -ldflags "$(LDFLAGS)" -o $(BINK_BINARY) ./cmd/bink
	@echo "✅ bink binary built: $(BINK_BINARY)"


# Build the bink CLI container image
build-bink-image:
	@echo "=== Building bink CLI container image ==="
	podman build --build-arg FEDORA_VERSION=$(FEDORA_VERSION) --build-arg VERSION=$(VERSION) --target builder -t localhost/bink-builder:latest -f Containerfile .
	podman build --build-arg FEDORA_VERSION=$(FEDORA_VERSION) --build-arg VERSION=$(VERSION) -t $(BINK_IMAGE) -f Containerfile .
	@echo "✅ Bink CLI image built: $(BINK_IMAGE)"

# Build the cluster container image
build-cluster-image:
	@echo "=== Building cluster container image ==="
	podman build --build-arg FEDORA_VERSION=$(FEDORA_VERSION) -t $(CLUSTER_IMAGE) -f $(VM_DIR)/Containerfile $(VM_DIR)
	@echo "✅ Cluster image built: $(CLUSTER_IMAGE)"

# Build the DNS container image
build-dns-image:
	@echo "=== Building DNS container image ==="
	podman build -t $(DNS_IMAGE) -f containerfiles/dns/Containerfile containerfiles/dns
	@echo "DNS image built: $(DNS_IMAGE)"

# Clean built images
clean:
	@echo "=== Cleaning up ==="
	podman rmi -f $(CLUSTER_IMAGE) 2>/dev/null || true
	@echo "✅ Cleaned up images"

# Rebuild everything from scratch
rebuild: clean all

# Update embedded Calico CNI manifest
CALICO_VERSION := $(call extract,CalicoVersion )
CALICO_MANIFEST := internal/cluster/calico.yaml

update-calico:
	@echo "=== Fetching Calico $(CALICO_VERSION) manifest ==="
	curl -sL "https://raw.githubusercontent.com/projectcalico/calico/$(CALICO_VERSION)/manifests/calico.yaml" \
		| sed 's|docker.io/calico/|quay.io/calico/|g' \
		| sed '/name: calico-kube-controllers$$/{n;s|image:|securityContext:\n            runAsUser: 0\n            runAsGroup: 0\n          image:|;}' \
		> $(CALICO_MANIFEST)
	@echo "✅ Updated $(CALICO_MANIFEST)"

# Test targets
GINKGO := go run github.com/onsi/ginkgo/v2/ginkgo
TEST_PROCS ?= 3
GINKGO_FOCUS ?=
GINKGO_FOCUS_FLAG := $(if $(GINKGO_FOCUS),--focus="$(GINKGO_FOCUS)",)

test-integration:
	@test -f ./$(BINK_BINARY) || (echo "Error: bink binary not found. Run 'make build-bink' first" && exit 1)
	@echo "=== Running Integration Tests ==="
	$(GINKGO) -v --procs=$(TEST_PROCS) $(GINKGO_FOCUS_FLAG) --fail-fast --randomize-all --randomize-suites test/integration/

test-integration-quick:
	@test -f ./$(BINK_BINARY) || (echo "Error: bink binary not found. Run 'make build-bink' first" && exit 1)
	@echo "=== Running Quick Integration Tests ==="
	$(GINKGO) -v --focus="quick" test/integration/

# Check REUSE/SPDX license compliance
check-license:
	@echo "=== Checking REUSE compliance ==="
	reuse lint

help:
	@echo "Makefile for building bootc images, cluster images, and bink CLI"
	@echo ""
	@echo "Build Targets:"
	@echo "  all                      - Build cluster image and bink binary (default)"
	@echo "  build-bink               - Build the bink CLI binary"
	@echo "  build-bink-image         - Build the bink CLI container image"
	@echo "  build-cluster-image      - Build the cluster container image"
	@echo "  build-dns-image          - Build the DNS container image"
	@echo ""
	@echo "Clean Targets:"
	@echo "  clean                    - Remove built images"
	@echo "  rebuild                  - Clean and rebuild everything"
	@echo ""
	@echo "Maintenance Targets:"
	@echo "  update-calico            - Fetch/update embedded Calico CNI manifest"
	@echo ""
	@echo "Check Targets:"
	@echo "  check-license            - Verify all Go files have the Apache 2.0 license header"
	@echo ""
	@echo "Test Targets:"
	@echo "  test-integration         - Run all integration tests"
	@echo "  test-integration-quick   - Run quick integration tests only"
	@echo ""
	@echo "Outputs:"
	@echo "  Binary:         $(BINK_BINARY)"
	@echo "  Bink image:     $(BINK_IMAGE)"
	@echo "  Cluster image:  $(CLUSTER_IMAGE)"
	@echo "  DNS image:      $(DNS_IMAGE)"
