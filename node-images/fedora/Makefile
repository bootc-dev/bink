.PHONY: build-bootc-image build-disk-image build-disk-image-composefs clean help

KUBE_MINOR ?= 1.35
FEDORA_VERSION ?= 44
DISK_SIZE ?= 10G
# Used only by build-disk-image-composefs (still bcvk-based, see below).
BUILD_MEMORY ?= 4G
BUILD_LOG_DIR ?= /tmp/bcvk-logs
# TODO: currently, there is a bug in bcvk where the digest for the image
# doesn't match with the remote digest. A temporary fix is available at this
# branch
BCVK_REF ?= img-to-install-cli-opt

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

# Build the disk image (qcow2 in a scratch container, for use as a podman image volume).
# Uses bootc-image-builder, which builds via osbuild's loop-device/manifest
# pipeline in its default privileged mode -- no VM boot, so no /dev/kvm
# needed, unlike the bcvk-based composefs target below.
#
# DISK_SIZE is not honored here: bootc-image-builder has no CLI flag for it,
# and rejects a customizations.filesystem minsize block for the qcow2 type
# ("blueprint validation failed... not supported"). The image is sized by
# content instead. DISK_SIZE still applies to build-disk-image-composefs,
# which passes it straight through to bcvk.
BIB_IMAGE ?= quay.io/centos-bootc/bootc-image-builder:latest

build-disk-image:
	@echo "=== Building node image with qcow2 disk (bootc-image-builder) ==="
	OUTDIR=$$(mktemp -d) && \
	trap 'rm -rf "$$OUTDIR"' EXIT && \
	STORAGE_PATH=$$(podman info --format '{{.Store.GraphRoot}}') && \
	if [ -z "$(BOOTC_DIGEST)" ]; then \
		BOOTC_DIGEST=$$(podman inspect --format '{{.Digest}}' $(BOOTC_IMAGE)); \
	else \
		BOOTC_DIGEST="$(BOOTC_DIGEST)"; \
	fi && \
	printf '[customizations.kernel]\nappend = "console=tty0 console=ttyS0,115200n8 console=hvc0"\n' \
		> "$$OUTDIR/config.toml" && \
	podman run --rm \
		--privileged \
		--security-opt label=disable \
		--volume "$$STORAGE_PATH:$$STORAGE_PATH" \
		--volume "$$OUTDIR:/output" \
		--volume "$$OUTDIR/config.toml:/config.toml:ro" \
		$(BIB_IMAGE) \
		--local \
		--type qcow2 \
		--rootfs ext4 \
		--config /config.toml \
		$(BOOTC_IMAGE) && \
	mv "$$OUTDIR/qcow2/disk.qcow2" "$$OUTDIR/disk.qcow2" && \
	podman run --rm $(BOOTC_IMAGE) kubeadm config images list > "$$OUTDIR/images.txt" && \
	podman build \
		--build-arg BOOTC_IMAGE="$(BOOTC_IMAGE)" \
		--build-arg KUBE_MINOR="$(KUBE_MINOR)" \
		--build-arg BOOTC_DIGEST="$$BOOTC_DIGEST" \
		-t $(NODE_IMAGE) \
		-f Containerfile.disk \
		"$$OUTDIR"
	@echo "✅ Node image built: $(NODE_IMAGE)"
	@echo ""
	@echo "This image can be used with: bink cluster start --node-image $(NODE_IMAGE)"

# Build the composefs-backend variant. bootc-image-builder has no
# user-facing way to force the composefs backend (it auto-derives it from
# whether the source image uses a unified kernel), so this stays on bcvk,
# which genuinely boots a VM for install-to-disk and therefore still needs
# /dev/kvm -- amd64-only until that changes.
build-disk-image-composefs:
	@echo "=== Building node image with qcow2 disk, composefs backend (bcvk) ==="
	STORAGE_PATH=$$(podman info --format '{{.Store.GraphRoot}}') && \
	if [ -z "$(BOOTC_DIGEST)" ]; then \
		BOOTC_DIGEST=$$(podman inspect --format '{{.Digest}}' $(BOOTC_IMAGE)); \
	else \
		BOOTC_DIGEST="$(BOOTC_DIGEST)"; \
	fi && \
	LOG_VOLUME="" && \
	if [ -n "$(BUILD_LOG_DIR)" ]; then \
		mkdir -p "$(BUILD_LOG_DIR)"; \
		LOG_VOLUME="--volume $(BUILD_LOG_DIR):$(BUILD_LOG_DIR) --build-arg LOG_DIR=$(BUILD_LOG_DIR)"; \
	fi && \
	podman build \
		--cap-add=SYS_ADMIN \
		--cap-add=DAC_READ_SEARCH \
		--cap-add=MKNOD \
		--security-opt=label=disable \
		--ulimit nofile=65536:65536 \
		--device=/dev/kvm \
		--volume "$$STORAGE_PATH:$$STORAGE_PATH" \
		$$LOG_VOLUME \
		--build-arg STORAGE_PATH="$$STORAGE_PATH" \
		--build-arg FEDORA_VERSION="$(FEDORA_VERSION)" \
		--build-arg BOOTC_IMAGE="$(BOOTC_IMAGE)" \
		--build-arg DISK_SIZE="$(DISK_SIZE)" \
		--build-arg MEMORY="$(BUILD_MEMORY)" \
		--build-arg KUBE_MINOR="$(KUBE_MINOR)" \
		--build-arg BOOTC_DIGEST="$$BOOTC_DIGEST" \
		--build-arg BCVK_EXTRA_ARGS="--composefs-backend" \
		--build-arg BCVK_REF="$(BCVK_REF)" \
		-t $(NODE_IMAGE_COMPOSEFS) \
		-f Containerfile.disk.bcvk \
		.
	@echo "✅ Node image built: $(NODE_IMAGE_COMPOSEFS)"

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
	@echo ""
	@echo "  build-disk-image-composefs only (bcvk-based, amd64-only):"
	@echo "  DISK_SIZE        - Disk image size (default: $(DISK_SIZE))"
	@echo "  BUILD_MEMORY     - Memory for bcvk's install VM (default: $(BUILD_MEMORY))"
	@echo "  BUILD_LOG_DIR    - Host path to collect bcvk logs (default: none)"
	@echo "  BCVK_REF         - Git commit/tag to build bcvk from source (default: $(BCVK_REF))"
