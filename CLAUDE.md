# Bink - Containerized Kubernetes Clusters with VMs

Bink is a CLI tool that manages Kubernetes clusters where each node is a Podman container running a VM inside. Each container uses libvirt/QEMU to run a Fedora bootc VM with kubeadm-managed Kubernetes.

## Building the Bink Binary

The bink binary requires CGO and C libraries (gpgme, btrfs, device-mapper) for Podman bindings. Always use the containerized build to avoid dependency issues on the host.

### Build Steps

```bash
# Build the binary inside a container (recommended, always use this)
make build-bink-container
```

This runs a two-stage process:
1. Builds a builder image from `Containerfile` (Fedora 43 + Go + C deps)
2. Compiles with `CGO_ENABLED=1 go build -o /output/bink ./cmd/bink`
3. Extracts the binary: creates a temp container, copies `./bink` out, removes the container

The resulting `./bink` binary is placed in the workspace root.

### Build Prerequisites

- Podman must be running (`systemctl --user start podman.socket` or equivalent)
- The Podman socket is at the default location: `/run/user/<uid>/podman/podman.sock`

### Building Container Images (needed before first cluster)

```bash
make build-vm-image           # Build fedora-bootc-k8s VM image
make build-cluster-image      # Build the cluster container image (libvirt + qemu)
make build-disk               # Convert bootc image to qcow2 (requires bcvk tool)
make build-images-container   # Wrap qcow2 in a container for image-volume mounting
```

## Cluster Lifecycle

### Create a Cluster

```bash
./bink cluster start --cluster-name mycluster --api-port 0 --memory 4096
```

- `--cluster-name` names the cluster (default: `podman`). All containers are prefixed `k8s-<cluster>-<node>`.
- `--api-port 0` auto-assigns a random host port for the Kubernetes API (recommended to avoid conflicts). Use a specific port number to pin it.
- `--memory` sets VM RAM in MB (default: 8192).

This creates container `k8s-mycluster-node1`, initializes kubeadm, installs Calico CNI, and configures CoreDNS.

### Expose the API Server

```bash
./bink api expose --cluster-name mycluster
```

This:
1. Detects the auto-assigned host port mapped to container port 6443
2. Verifies the API server is reachable via passt port forwarding (container:6443 -> VM:6443)
3. Fetches kubeconfig from the VM and rewrites the server URL to `https://localhost:<host-port>`
4. Saves kubeconfig to `./kubeconfig-mycluster` (permissions 0600)

Then use:
```bash
export KUBECONFIG=./kubeconfig-mycluster
kubectl get nodes
```

### Add Worker Nodes

```bash
./bink node add node2 --cluster-name mycluster --memory 4096
```

Flags:
- `--role worker|control-plane` (default: `worker`)
- `--control-plane node1` (which node to join against, default: `node1`)
- `--memory` VM RAM in MB

### SSH into a Node

```bash
./bink node ssh node1 --cluster-name mycluster
```

### List Clusters and Nodes

```bash
./bink cluster list
./bink node list
```

### Stop and Clean Up

```bash
# Stop cluster (removes containers)
./bink cluster stop --cluster-name mycluster

# Stop and remove all data (volumes, kubeconfig, SSH keys)
./bink cluster stop --cluster-name mycluster --remove-data
```

## Interacting with the Cluster via Podman (Without Bink)

If you need to interact with cluster containers directly using podman commands (e.g., from scripts or when bink is unavailable):

### Container Naming Convention

All cluster containers follow the pattern: `k8s-<cluster-name>-<node-name>`

Example: cluster `mycluster` with nodes `node1` and `node2` creates containers `k8s-mycluster-node1` and `k8s-mycluster-node2`.

### Finding Cluster Containers

```bash
# List all bink containers
podman ps --filter "name=k8s-"

# List containers for a specific cluster
podman ps --filter "label=bink.cluster-name=mycluster"
```

### Executing Commands Inside Containers

```bash
# Run a command inside the container (container-level, not VM-level)
podman exec k8s-mycluster-node1 <command>
```

### Executing Commands Inside the VM (via SSH through the container)

The VM runs inside the container. To reach the VM, SSH through the container:

```bash
# Execute a command on the VM
podman exec k8s-mycluster-node1 ssh \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null \
  -i /var/run/cluster/cluster.key \
  -p 2222 \
  core@localhost \
  '<command>'
```

- SSH key: `/var/run/cluster/cluster.key` (inside the container, shared via volume)
- SSH port: `2222` (passt network maps container 2222 to VM 22)
- SSH user: `core`

Example - run kubectl on the VM:
```bash
podman exec k8s-mycluster-node1 ssh \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null \
  -i /var/run/cluster/cluster.key \
  -p 2222 \
  core@localhost \
  'sudo kubectl get nodes --kubeconfig=/etc/kubernetes/admin.conf'
```

### Inspecting Container State

```bash
# Check container status
podman inspect k8s-mycluster-node1 --format '{{.State.Status}}'

# Get published API port
podman inspect k8s-mycluster-node1 --format '{{json .NetworkSettings.Ports}}'

# Get cluster name label
podman inspect k8s-mycluster-node1 --format '{{index .Config.Labels "bink.cluster-name"}}'

# Get node name label
podman inspect k8s-mycluster-node1 --format '{{index .Config.Labels "bink.node-name"}}'
```

### Fetching Kubeconfig Manually

```bash
# Get the published host port for 6443/tcp
HOST_PORT=$(podman inspect k8s-mycluster-node1 --format '{{(index (index .NetworkSettings.Ports "6443/tcp") 0).HostPort}}')

# Fetch kubeconfig from the VM
podman exec k8s-mycluster-node1 ssh \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null \
  -i /var/run/cluster/cluster.key \
  -p 2222 \
  core@localhost \
  'cat ~/.kube/config' > kubeconfig-mycluster

# Replace the server URL with the published port
sed -i "s|server: https://.*|server: https://localhost:${HOST_PORT}|" kubeconfig-mycluster

export KUBECONFIG=./kubeconfig-mycluster
kubectl get nodes
```

### Container Labels

Every bink container has these labels:
- `bink.cluster-name` - the cluster name (e.g., `mycluster`)
- `bink.node-name` - the node name (e.g., `node1`)

## Architecture Overview

```
Host (podman)
  |
  +-- Container: k8s-mycluster-node1 (localhost/cluster:latest)
  |     |-- libvirt + qemu
  |     |-- VM: Fedora bootc (kubeadm, crio, k8s 1.35)
  |     |     |-- NIC1 (passt): internet + SSH (2222) + API (6443, control-plane only)
  |     |     |-- NIC2 (multicast 230.0.0.1:5558): cluster network 10.0.0.0/24
  |     |     +-- Kubernetes control-plane
  |     |-- Port 6443/tcp published to host (API server, via passt port forward)
  |     +-- Volumes: cluster-keys, cluster-images (read-only)
  |
  +-- Container: k8s-mycluster-node2
        |-- Same structure as above
        +-- Kubernetes worker node (joined via kubeadm)
```

### Networking

- **Container network**: Podman bridge (`10.88.0.0/16`)
- **VM cluster network**: Multicast on `230.0.0.1:5558`, IPs in `10.0.0.0/24` (deterministic from node name hash)
- **VM internet**: passt user-mode networking (NIC1)
- **SSH path**: host -> `podman exec` -> container SSH -> VM:2222
- **API path**: host:random-port -> container:6443 (passt) -> VM:6443

### Key Paths Inside Containers

| Path | Purpose |
|------|---------|
| `/var/run/cluster/cluster.key` | SSH private key (shared volume) |
| `/var/run/cluster/cluster.key.pub` | SSH public key |
| `/images/fedora-bootc-k8s.qcow2` | Base VM disk (from images volume) |
| `/var/lib/cluster-images/` | Shared filesystem (virtiofs, read-only) |

### Key Paths Inside VMs

| Path | Purpose |
|------|---------|
| `/etc/kubernetes/admin.conf` | Kubernetes admin kubeconfig |
| `~/.kube/config` | User kubeconfig (core user) |
| `/var/lib/dnsmasq/cluster-hosts` | DNS host entries (node1 only) |

## Running Tests

```bash
# Integration tests (requires built bink binary and container images)
make build-bink-container    # build binary first
make test-integration        # full suite
make test-integration-quick  # quick tests only
```

Integration tests compile separately inside the builder container due to CGO dependencies. The test binary is at `test/integration/integration.test`.

## Configuration

Bink reads config from (in order):
1. `$HOME/.bink/config.yaml`
2. `./.bink/config.yaml`
3. CLI flags
4. Environment variables with `BINK_` prefix (e.g., `BINK_CLUSTER_NAME`)

Global flags available on all commands:
- `--cluster-name <name>` (default: `podman`)
- `--verbose` / `-v`
- `--debug`
- `--config <path>`
