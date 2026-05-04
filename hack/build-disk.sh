#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

DEFAULTS_GO="$ROOT_DIR/internal/config/defaults.go"
extract() { grep "$1" "$DEFAULTS_GO" | head -1 | sed 's/.*"\(.*\)"/\1/'; }

BOOTC_IMAGE=$(extract DefaultBootcImage)
CLUSTER_IMAGE=$(extract DefaultClusterImage)
DISK_IMAGE="fedora-bootc-k8s.qcow2"
DISK_SIZE=${DISK_SIZE:-"10G"}
MEMORY=${MEMORY:-"4G"}
OUTPUT_DIR="$ROOT_DIR/vm/images"
BUILDER_IMAGE="localhost/bcvk-disk-builder:latest"
BUILDER_DIR="$ROOT_DIR/containerfiles/disk-builder"
VOLUME_NAME="bcvk-disk-output"

STORAGE_PATH=$(podman info --format '{{.Store.GraphRoot}}')
echo "=== Building disk image with bcvk ==="
echo "  Bootc image:    $BOOTC_IMAGE"
echo "  Cluster image:  $CLUSTER_IMAGE"
echo "  Container store: $STORAGE_PATH"
echo "  Output:          $OUTPUT_DIR/$DISK_IMAGE"
echo

echo "Step 1: Building disk builder image (compiles bcvk from source)..."
podman build \
  --build-arg CLUSTER_IMAGE="$CLUSTER_IMAGE" \
  -t "$BUILDER_IMAGE" \
  -f "$BUILDER_DIR/Containerfile" \
  "$BUILDER_DIR"

echo
echo "Step 2: Running bcvk to-disk inside container..."
podman volume exists "$VOLUME_NAME" 2>/dev/null || podman volume create "$VOLUME_NAME"

podman run --rm \
  --cap-add=SYS_ADMIN \
  --cap-add=DAC_READ_SEARCH \
  --cap-add=MKNOD \
  --security-opt=label=disable \
  --device=/dev/kvm \
  -v "$STORAGE_PATH:$STORAGE_PATH" \
  -v "$VOLUME_NAME:/output" \
  "$BUILDER_IMAGE" \
  bash -c "export CONTAINERS_STORAGE_CONF=/tmp/storage.conf && \
    printf '[storage]\ndriver = \"overlay\"\ngraphroot = \"$STORAGE_PATH\"\n' > \$CONTAINERS_STORAGE_CONF && \
    bcvk to-disk -K \
    --karg 'console=tty0' \
    --karg 'console=ttyS0,115200n8' \
    --filesystem ext4 \
    --format qcow2 \
    --memory $MEMORY \
    --disk-size $DISK_SIZE \
    $BOOTC_IMAGE \
    /output/$DISK_IMAGE"

echo
echo "Step 3: Copying disk image out..."
mkdir -p "$OUTPUT_DIR"
CID=$(podman create --entrypoint "" -v "$VOLUME_NAME:/vol:ro" "$BUILDER_IMAGE" true)
podman cp "$CID:/vol/$DISK_IMAGE" "$OUTPUT_DIR/$DISK_IMAGE"
podman rm "$CID" >/dev/null

echo "Step 4: Cleaning up..."
podman volume rm "$VOLUME_NAME" >/dev/null 2>&1 || true

echo
echo "=== Done ==="
echo "Disk image: $OUTPUT_DIR/$DISK_IMAGE ($(du -h "$OUTPUT_DIR/$DISK_IMAGE" | cut -f1))"
