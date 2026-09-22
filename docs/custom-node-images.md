# Building Custom Node Images

## Overview

When debugging a bug in a node component (bootc, CRI-O, kubelet, kernel, etc.), you need to replace the package or binary with a custom build and verify the fix. There are two approaches:

1. **Runtime update** — Build a custom bootc OCI image and use `bootc switch` to deploy it on a running node. Faster, no disk rebuild needed.
2. **Disk image rebuild** — Rebuild the entire qcow2 disk image. Required when the fix must be present from first boot (kernel, bootc itself, initrd).

**When to use which:**

| Situation | Approach |
|-----------|----------|
| Patching a userspace component (CRI-O, kubelet, a CLI tool) | Runtime update |
| Patching bootc itself, the kernel, or initrd | Disk image rebuild |
| Need the fix before the first boot completes | Disk image rebuild |
| Testing composefs-specific behavior or fs-verity | Disk image rebuild (composefs variant) |

## Prerequisites

- Podman with socket running (`systemctl --user start podman.socket`)
- `/dev/kvm` and `/dev/fuse` accessible
- bink binary built (see [README](../README.md))
- A running bink cluster, or you will create one

## Inspecting the Current Node State

Before making changes, inspect what is currently deployed. SSH into a node:

```bash
bink node ssh node1 --cluster-name <name>
```

Then run:

```bash
# Check the ostree deployment
ostree admin status

# Show ostree commit details
ostree show --repo=/sysroot/ostree/repo <checksum>

# Check the origin file (ostree backend)
sudo cat /sysroot/ostree/deploy/default/deploy/*.origin

# Check the origin file (composefs backend)
sudo cat /sysroot/state/deploy/*.origin

# Full bootc status
sudo bootc status --json | jq

# Determine the deployment backend (ostree or composefs)
sudo bootc status --json | jq '.status.booted.store'

# Check specific package versions
rpm -qa | grep -E 'bootc|cri-o|kubeadm'
```

## Approach 1: Runtime Update via `bootc switch`

The simpler path when the patch does not need to be in the boot image. Build a custom bootc OCI image, push it to bink's local registry, and switch the node to it.

### Example: Building bootc from Source

The [bootc repository](https://github.com/bootc-dev/bootc) has a Dockerfile with `ARG base=quay.io/centos-bootc/centos-bootc:stream10`. Override this to use the bink node image as the base, producing a new bootable container image with bootc compiled from any git commit:

```bash
git clone https://github.com/bootc-dev/bootc.git
cd bootc
git checkout <commit-or-branch>

podman build \
  --build-arg base=ghcr.io/bootc-dev/bink/node:v1.36-fedora-44 \
  -t localhost/custom-bootc-node:latest \
  .
```

This compiles bootc from source and installs it into an image derived from the bink node image, preserving all the existing packages (kubernetes, CRI-O, etc.).

### Example: Layering a Custom Package

For simpler cases (installing a different package version), write a minimal Containerfile:

```dockerfile
FROM ghcr.io/bootc-dev/bink/node:v1.36-fedora-44
RUN dnf -y install <your-package>
```

Build it:

```bash
podman build -t localhost/custom-node:latest -f Containerfile.custom .
```

### Push to the Local Registry and Switch

Bink runs a local OCI registry on port 5000. From the host, push to `localhost:5000`. Inside the VMs, the registry is reachable at `registry.cluster.local:5000`.

```bash
podman push --tls-verify=false \
  localhost/custom-bootc-node:latest \
  localhost:5000/custom-bootc-node:latest
```

SSH into the node and switch:

```bash
bink node ssh node1 --cluster-name <name>

sudo bootc switch \
  registry.cluster.local:5000/custom-bootc-node:latest
```

To switch by digest:

```bash
sudo bootc switch \
  registry.cluster.local:5000/custom-bootc-node@sha256:<digest>
```

Reboot to apply:

```bash
sudo reboot
```

### Verify After Reboot

```bash
bink node ssh node1 --cluster-name <name>

ostree admin status
sudo bootc status --json | jq '.status.booted.image'
rpm -q bootc

# Determine the deployment backend
sudo bootc status --json | jq '.status.booted.store'

# Check the origin file (ostree backend)
sudo cat /sysroot/ostree/deploy/default/deploy/*.origin

# Check the origin file (composefs backend)
sudo cat /sysroot/state/deploy/*.origin
```

### Using `--target-imgref` for New Clusters

Instead of switching after boot, set the tracked image reference at cluster creation:

```bash
bink cluster start \
  --cluster-name test \
  --target-imgref registry.cluster.local:5000/custom-bootc-node:latest
```

This rewrites the ostree origin file during cloud-init so the node tracks your custom image from the start. Running `bootc upgrade` will pull updates from your custom image reference.

## Approach 2: Rebuild the Disk Image

Required when the fix must be present from first boot (kernel, bootc, initrd changes).

### Build Pipeline Overview

The node disk image is built in two stages from `node-images/fedora/`:

```
Containerfile       ->  bootc OCI image   (bootc-base-imagectl build-rootfs)
Containerfile.disk  ->  qcow2 disk image  (bcvk to-disk)
```

- **Stage 1** (`Containerfile`): Builds a bootc OCI image from `quay.io/fedora/fedora-bootc:44`. Uses `bootc-base-imagectl build-rootfs` with `--install` flags for packages (kubernetes, CRI-O, etc.). Validated with `bootc container lint`.
- **Stage 2** (`Containerfile.disk`): Converts the bootc OCI image to a qcow2 disk using `bcvk to-disk`. The final container image contains only `/disk.qcow2` and `/images.txt`.

> **N.B.** If you need to run `podman build` directly instead of using the Makefile targets, you must pass the required privilege flags:
>
> ```bash
> podman build \
>   --cap-add=all \
>   --security-opt=label=disable \
>   --device /dev/fuse \
>   --build-arg KUBE_MINOR=1.36 \
>   -t localhost/custom-node:latest \
>   -f Containerfile \
>   .
> ```

### Scenario A: Custom Package from a COPR Repo

Modify `node-images/fedora/Containerfile` to add a COPR repo before the `build-rootfs` command:

```dockerfile
RUN dnf -y install 'dnf5-command(copr)' && \
    dnf -y copr enable <owner>/<project> fedora-44-x86_64
```

The COPR architecture string must match the base image (e.g., `fedora-44-x86_64` for Fedora 44).

Build and test:

```bash
cd node-images/fedora

make build-bootc-image BOOTC_IMAGE=localhost/custom-node:latest
make build-disk-image \
  BOOTC_IMAGE=localhost/custom-node:latest \
  NODE_IMAGE=localhost/custom-node:disk

cd ../..

bink cluster start \
  --node-image localhost/custom-node:disk \
  --cluster-name custom-test
```

### Scenario B: Local RPM File

Copy the RPM into the build context and install it into the target rootfs after `build-rootfs` completes:

```dockerfile
COPY my-package.rpm /tmp/my-package.rpm

# After the build-rootfs RUN instruction:
RUN dnf --installroot=/target-rootfs install -y /tmp/my-package.rpm
```

The `--installroot=/target-rootfs` flag is required because `build-rootfs` assembles the filesystem at `/target-rootfs`, not in the builder's own root.

Build and test with the same `make` commands as Scenario A.

### Scenario C: Custom Binary Replacement

Use a multi-stage build to compile the binary and copy it into the node image:

```dockerfile
FROM fedora:44 AS custom-build
RUN dnf -y install git golang make
COPY my-source/ /src
WORKDIR /src
RUN make build

FROM ghcr.io/bootc-dev/bink/node:v1.36-fedora-44
COPY --from=custom-build /src/my-binary /usr/bin/my-binary
```

Build and test with the same `make` commands as Scenario A.

### Composefs Variant

[Composefs](https://github.com/composefs/composefs) is an alternative to the default ostree deployment backend. It uses EROFS for filesystem metadata and overlayfs for content-addressed file mounts, with optional fs-verity integrity checking.

Build the disk image with the composefs backend:

```bash
cd node-images/fedora

make build-bootc-image BOOTC_IMAGE=localhost/custom-node:latest
make build-disk-image-composefs \
  BOOTC_IMAGE=localhost/custom-node:latest \
  NODE_IMAGE_COMPOSEFS=localhost/custom-node:disk-composefs

cd ../..

bink cluster start \
  --node-image localhost/custom-node:disk-composefs \
  --cluster-name composefs-test
```

`make build-disk-image-composefs` wraps `make build-disk-image` with `BCVK_EXTRA_ARGS="--composefs-backend"`. Override the image name via `NODE_IMAGE_COMPOSEFS` (not `NODE_IMAGE`).

Verify the node is using the composefs backend:

```bash
bink node ssh node1 --cluster-name composefs-test

# Should return "composefs"
sudo bootc status --json | jq '.status.booted.store'

# Composefs-specific metadata (verity status, boot digest)
sudo bootc status --json | jq '.status.booted.composefs'

# Origin file (composefs path)
sudo cat /sysroot/state/deploy/*.origin
```

Run the composefs-specific integration tests:

```bash
make test-integration-composefs
```

## Makefile Variable Reference

Variables in `node-images/fedora/Makefile`:

| Variable | Default | Description |
|----------|---------|-------------|
| `KUBE_MINOR` | `1.36` | Kubernetes minor version |
| `FEDORA_VERSION` | `44` | Fedora base version |
| `DISK_SIZE` | `10G` | VM disk size |
| `BUILD_MEMORY` | `4G` | Memory for bcvk build |
| `BOOTC_IMAGE` | `ghcr.io/bootc-dev/bink/node:v1.36-fedora-44` | Bootc OCI image name |
| `NODE_IMAGE` | `ghcr.io/bootc-dev/bink/node:v1.36-fedora-44-disk` | Disk image name |
| `NODE_IMAGE_COMPOSEFS` | `ghcr.io/bootc-dev/bink/node:v1.36-fedora-44-disk-composefs` | Composefs disk image name |
| `BCVK_EXTRA_ARGS` | (none) | Extra flags for `bcvk to-disk` |
