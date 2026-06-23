.PHONY: build-bootc-image build-disk-image build-disk-image-composefs clean help

KUBE_MINOR ?= 1.35
FEDORA_VERSION ?= 44
DISK_SIZE ?= 10G
BUILD_MEMORY ?= 4G
BCVK_EXTRA_ARGS ?=

IMAGE_TAG ?= v$(KUBE_MINOR)-fedora-$(FEDORA_VERSION)
REGISTRY ?= ghcr.io/bootc-dev/bink
BOOTC_IMAGE ?= $(REGISTRY)/node:$(IMAGE_TAG)
NODE_IMAGE ?= $(REGISTRY)/node:$(IMAGE_TAG)-disk
NODE_IMAGE_COMPOSEFS ?= $(REGISTRY)/node:$(IMAGE_TAG)-disk-composefs
BOOTC_DIGEST ?=

# Build the OCI bootc image (k8s + cri-o)
build-bootc-image:
	@echo "=== Building bootc image ==="
	podman build \
		--cap-add=all \
		--security-opt=label=disable \
		--device /dev/fuse \
		--build-arg KUBE_MINOR=$(KUBE_MINOR) \
		-t $(BOOTC_IMAGE) \
		-f Containerfile \
		.
	@echo "✅ Bootc image built: $(BOOTC_IMAGE)"

# Build the disk image (qcow2 in a scratch container, for use as a podman image volume)
build-disk-image:
	@echo "=== Building node image with qcow2 disk ==="
	STORAGE_PATH=$$(podman info --format '{{.Store.GraphRoot}}') && \
	if [ -z "$(BOOTC_DIGEST)" ]; then \
		BOOTC_DIGEST=$$(podman inspect --format '{{.Digest}}' $(BOOTC_IMAGE)); \
	else \
		BOOTC_DIGEST="$(BOOTC_DIGEST)"; \
	fi && \
	podman build \
		--cap-add=SYS_ADMIN \
		--cap-add=DAC_READ_SEARCH \
		--cap-add=MKNOD \
		--security-opt=label=disable \
		--device=/dev/kvm \
		--volume "$$STORAGE_PATH:$$STORAGE_PATH" \
		--build-arg STORAGE_PATH="$$STORAGE_PATH" \
		--build-arg FEDORA_VERSION="$(FEDORA_VERSION)" \
		--build-arg BOOTC_IMAGE="$(BOOTC_IMAGE)" \
		--build-arg DISK_SIZE="$(DISK_SIZE)" \
		--build-arg MEMORY="$(BUILD_MEMORY)" \
		--build-arg KUBE_MINOR="$(KUBE_MINOR)" \
		--build-arg BOOTC_DIGEST="$$BOOTC_DIGEST" \
		--build-arg BCVK_EXTRA_ARGS="$(BCVK_EXTRA_ARGS)" \
		-t $(NODE_IMAGE) \
		-f Containerfile.disk \
		.
	@echo "✅ Node image built: $(NODE_IMAGE)"
	@echo ""
	@echo "This image can be used with: bink cluster start --node-image $(NODE_IMAGE)"

build-disk-image-composefs:
	$(MAKE) build-disk-image BCVK_EXTRA_ARGS="--composefs-backend" NODE_IMAGE="$(NODE_IMAGE_COMPOSEFS)"

print-image-tag:
	@echo $(IMAGE_TAG)

print-bootc-image:
	@echo $(BOOTC_IMAGE)

print-node-image:
	@echo $(NODE_IMAGE)

print-node-image-composefs:
	@echo $(NODE_IMAGE_COMPOSEFS)

clean:
	@echo "=== Cleaning up ==="
	podman rmi -f $(BOOTC_IMAGE) $(NODE_IMAGE) 2>/dev/null || true
	@echo "✅ Cleaned up images"

help:
	@echo "Makefile for building bink node images (Fedora)"
	@echo ""
	@echo "Targets:"
	@echo "  build-bootc-image          - Build the OCI bootc image"
	@echo "  build-disk-image           - Build the OCI disk image with qcow2 (default, ostree backend)"
	@echo "  build-disk-image-composefs - Build the OCI disk image with composefs backend"
	@echo "  clean                      - Remove built images"
	@echo ""
	@echo "Variables:"
	@echo "  BOOTC_IMAGE      - Bootc OCI image name (default: $(BOOTC_IMAGE))"
	@echo "  NODE_IMAGE       - Node image name (default: $(NODE_IMAGE))"
	@echo "  KUBE_MINOR       - Kubernetes minor version (default: $(KUBE_MINOR))"
	@echo "  FEDORA_VERSION   - Fedora version (default: $(FEDORA_VERSION))"
	@echo "  DISK_SIZE        - VM disk size (default: $(DISK_SIZE))"
	@echo "  BUILD_MEMORY     - Memory for bcvk build (default: $(BUILD_MEMORY))"
	@echo "  BCVK_EXTRA_ARGS  - Extra arguments passed to bcvk to-disk (default: none)"
