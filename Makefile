.PHONY: all build-bink build-bink-container build-cluster-image build-dns-image clean rebuild help test-integration test-integration-quick update-calico

# Extract image names and versions from internal/config/defaults.go (single source of truth)
DEFAULTS_GO := internal/config/defaults.go
extract = $(shell grep '$(1)' $(DEFAULTS_GO) | head -1 | sed 's/.*"\(.*\)"/\1/')

CLUSTER_IMAGE := $(call extract,DefaultClusterImage )
DNS_IMAGE := $(call extract,DNSImage )
FEDORA_VERSION := $(call extract,FedoraVersion )

# Binary
BINK_BINARY := bink

# Directories
VM_DIR := containerfiles/cluster-image

all: build-bink

# Build the bink CLI binary
build-bink:
	@echo "=== Building bink CLI binary ==="
	go build -o $(BINK_BINARY) ./cmd/bink
	@echo "✅ bink binary built: $(BINK_BINARY)"

# Build the bink CLI binary using containerized build (with all C dependencies)
build-bink-container:
	@echo "=== Building bink CLI binary in container ==="
	podman build --build-arg FEDORA_VERSION=$(FEDORA_VERSION) -t localhost/bink-builder:latest -f Containerfile .
	@echo "Extracting binary from container..."
	@podman create --name bink-temp localhost/bink-builder:latest
	@podman cp bink-temp:/output/bink ./bink
	@podman rm bink-temp
	@echo "✅ bink binary built in container: $(BINK_BINARY)"

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

help:
	@echo "Makefile for building bootc images, cluster images, and bink CLI"
	@echo ""
	@echo "Build Targets:"
	@echo "  all                      - Build cluster image and bink binary (default)"
	@echo "  build-bink               - Build the bink CLI binary"
	@echo "  build-bink-container     - Build the bink CLI binary in container (with C deps)"
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
	@echo "Test Targets:"
	@echo "  test-integration         - Run all integration tests"
	@echo "  test-integration-quick   - Run quick integration tests only"
	@echo ""
	@echo "Outputs:"
	@echo "  Binary:         $(BINK_BINARY)"
	@echo "  Cluster image:  $(CLUSTER_IMAGE)"
	@echo "  DNS image:      $(DNS_IMAGE)"
