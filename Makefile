.PHONY: all build-bink build-bink-container build-vm-image build-cluster-image build-disk build-images-container build-populator-image clean clean-disk rebuild help test-integration test-integration-quick update-calico

# Extract image names and versions from internal/config/defaults.go (single source of truth)
DEFAULTS_GO := internal/config/defaults.go
extract = $(shell grep '$(1)' $(DEFAULTS_GO) | head -1 | sed 's/.*"\(.*\)"/\1/')

BOOTC_IMAGE := $(call extract,DefaultBootcImage )
CLUSTER_IMAGE := $(call extract,DefaultClusterImage )
NODE_IMAGE := $(call extract,DefaultNodeImage )
POPULATOR_IMAGE := $(call extract,DefaultPopulatorImage )
FEDORA_VERSION := $(call extract,FedoraVersion )
KUBE_MINOR := $(call extract,KubernetesMinorVersion )
DISK_IMAGE := fedora-bootc-k8s.qcow2
DISK_SIZE := 10G

# Binary
BINK_BINARY := bink

# Directories
IMAGES_DIR := containerfiles/images
VM_DIR := containerfiles/vm
POPULATOR_DIR := containerfiles/populator
OUTPUT_DIR := vm/images

all: build-cluster-image build-vm-image build-disk build-images-container build-bink

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

# Build the fedora-bootc-k8s VM image
build-vm-image:
	@echo "=== Building fedora-bootc-k8s VM image ==="
	podman build --build-arg KUBE_MINOR=$(KUBE_MINOR) -t $(BOOTC_IMAGE) -f $(IMAGES_DIR)/Containerfile $(IMAGES_DIR)
	@echo "✅ VM image built: $(BOOTC_IMAGE)"

# Build the cluster container image
build-cluster-image:
	@echo "=== Building cluster container image ==="
	podman build --build-arg FEDORA_VERSION=$(FEDORA_VERSION) -t $(CLUSTER_IMAGE) -f $(VM_DIR)/Containerfile $(VM_DIR)
	@echo "✅ Cluster image built: $(CLUSTER_IMAGE)"

# Convert bootc image to qcow2 disk
build-disk: build-vm-image
	@echo "=== Converting bootc image to disk ==="
	@mkdir -p $(OUTPUT_DIR)
	cd $(OUTPUT_DIR) && \
	RUST_LOG=debug bcvk to-disk -K \
		--karg 'console=tty0' \
		--karg 'console=ttyS0,115200n8' \
		--filesystem ext4 \
		--format qcow2 \
		--disk-size $(DISK_SIZE) \
		$(BOOTC_IMAGE) $(DISK_IMAGE)
	@echo "✅ Disk image created: $(OUTPUT_DIR)/$(DISK_IMAGE)"

# Build container image with qcow2 disk for image volume mounting
build-images-container: build-disk
	@echo "=== Building node image with qcow2 disk ==="
	@echo "FROM scratch" > $(OUTPUT_DIR)/Containerfile.images
	@echo "COPY $(DISK_IMAGE) /$(DISK_IMAGE)" >> $(OUTPUT_DIR)/Containerfile.images
	podman build -t $(NODE_IMAGE) -f $(OUTPUT_DIR)/Containerfile.images $(OUTPUT_DIR)
	@rm -f $(OUTPUT_DIR)/Containerfile.images
	@echo "✅ Node image built: $(NODE_IMAGE)"
	@echo ""
	@echo "This image can be used with: bink cluster start --node-image $(NODE_IMAGE)"

# Build the populator image (skopeo pre-installed for fast volume population)
build-populator-image:
	@echo "=== Building cluster images populator ==="
	podman build --build-arg FEDORA_VERSION=$(FEDORA_VERSION) --build-arg KUBE_MINOR=$(KUBE_MINOR) -t $(POPULATOR_IMAGE) -f $(POPULATOR_DIR)/Containerfile $(POPULATOR_DIR)
	@echo "✅ Populator image built: $(POPULATOR_IMAGE)"

# Clean built images and disk
clean:
	@echo "=== Cleaning up ==="
	podman rmi -f $(BOOTC_IMAGE) $(CLUSTER_IMAGE) $(NODE_IMAGE) $(POPULATOR_IMAGE) 2>/dev/null || true
	rm -f $(OUTPUT_DIR)/$(DISK_IMAGE)
	@echo "✅ Cleaned up images and disk"

# Clean disk image only
clean-disk:
	@echo "=== Cleaning disk image ==="
	rm -f $(OUTPUT_DIR)/$(DISK_IMAGE)
	@echo "✅ Disk image removed"

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

test-integration:
	@test -f ./$(BINK_BINARY) || (echo "Error: bink binary not found. Run 'make build-bink' first" && exit 1)
	@echo "=== Running Integration Tests ==="
	$(GINKGO) -v --procs=2 --randomize-all --randomize-suites test/integration/

test-integration-quick:
	@test -f ./$(BINK_BINARY) || (echo "Error: bink binary not found. Run 'make build-bink' first" && exit 1)
	@echo "=== Running Quick Integration Tests ==="
	$(GINKGO) -v --focus="quick" test/integration/

help:
	@echo "Makefile for building bootc images, cluster images, and bink CLI"
	@echo ""
	@echo "Build Targets:"
	@echo "  all                      - Build all images and bink binary (default)"
	@echo "  build-bink               - Build the bink CLI binary"
	@echo "  build-bink-container     - Build the bink CLI binary in container (with C deps)"
	@echo "  build-vm-image           - Build the fedora-bootc-k8s VM container image"
	@echo "  build-cluster-image      - Build the cluster container image"
	@echo "  build-disk               - Convert bootc image to qcow2 disk image"
	@echo "  build-images-container   - Build container with qcow2 disk for image volume"
	@echo "  build-populator-image    - Build the cluster images populator image (skopeo)"
	@echo ""
	@echo "Clean Targets:"
	@echo "  clean                    - Remove all built images and disk"
	@echo "  clean-disk               - Remove only the disk image"
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
	@echo "  VM image:       $(BOOTC_IMAGE)"
	@echo "  Cluster image:  $(CLUSTER_IMAGE)"
	@echo "  Node image:     $(NODE_IMAGE)"
	@echo "  Disk image:     $(OUTPUT_DIR)/$(DISK_IMAGE)"
