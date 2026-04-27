# Implementation Plan: bink - Containerized Kubernetes Clusters

## Infrastructure Overview

**bink** manages multi-node Kubernetes clusters where each node runs as a rootless Podman container hosting a bootc-based VM. This architecture delivers VM isolation with container convenience.

### Networking
- **passt**: Each VM's first NIC (enp1s0) provides internet access via userspace networking
- **multicast**: Second NIC (enp2s0) connects to cluster network (10.0.0.0/24) for Kubernetes communication
- IP allocation: MD5 hash of node name → `10.0.0.{hash % 240 + 10}`

### Storage
- **Base VM images**: QCOW2 disks from bootc images, mounted read-only via container image volumes at `/images`
- **Overlay disks**: Per-node QCOW2 overlays in ephemeral container storage (`/workspace/{node}.qcow2`)
- **Container images**: Shared `cluster-images` volume pre-populated with Kubernetes images

### Container Image Caching
- **virtiofs**: Mounts `cluster-images` volume into each VM at `/var/lib/containers/storage` 
- Pre-pulled K8s images (apiserver, scheduler, controller-manager, etcd, coredns) available immediately
- Eliminates redundant image pulls across cluster nodes

### Architecture Diagram

**Multi-Cluster Setup**
```
┌───────────────────────────────────────────────────────────────┐
│ Host System                                                   │
│                                                               │
│  ┌──────────────────────────────────────────────────────────┐ │
│  │ Cluster "cluster1"                                       │ │
│  │                                                          │ │
│  │  ┌──────────────────┐      ┌──────────────────┐          │ │
│  │  │ Container        │      │ Container        │          │ │
│  │  │ k8s-node1        │══════│ k8s-node2        │          │ │
│  │  │                  │      │                  │          │ │
│  │  │  ┌────────────┐  │      │  ┌────────────┐  │          │ │
│  │  │  │ VM: node1  │  │      │  │ VM: node2  │  │          │ │
│  │  │  │ 10.0.0.32  │  │      │  │ 10.0.0.130 │  │          │ │
│  │  │  └────────────┘  │      │  └────────────┘  │          │ │
│  │  └──────────────────┘      └──────────────────┘          │ │
│  └──────────────────────────────────────────────────────────┘ │
│                                                               │
│  ┌────────────────────────┐                                   │
│  │ Cluster "cluster2"     │                                   │
│  │                        │                                   │
│  │  ┌──────────────────┐  │                                   │
│  │  │ Container        │  │                                   │
│  │  │ k8s-test-node1   │  │                                   │
│  │  │                  │  │                                   │
│  │  │  ┌────────────┐  │  │                                   │
│  │  │  │ VM: node1  │  │  │                                   │
│  │  │  │ 10.0.0.32  │  │  │                                   │
│  │  │  └────────────┘  │  │                                   │
│  │  └──────────────────┘  │                                   │
│  └────────────────────────┘                                   │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

## Local Registry

A shared container registry runs alongside clusters so users can push custom images and pull them from any cluster.

### Design

- **Shared across all clusters** — push once, pull from any cluster
- **Static IP** `10.88.0.2` on the Podman bridge — VMs reach it through passt
- **Auto-created** on first `cluster start` — no separate command needed
- **Fixed host port** `5000` — push with `podman push localhost:5000/myimage:tag`
- **Insecure** — CRI-O and containers/registries.conf configured to trust it without TLS
- **Survives cluster stops** — only removed via `bink registry stop`

### Network Path

```
Host (localhost:5000)
  |
  +-- Podman bridge (10.88.0.0/16)
  |     |
  |     +-- bink-registry (10.88.0.2:5000)  <-- static IP
  |     |
  |     +-- k8s-mycluster-node1 (10.88.0.X)
  |     |     +-- VM (passt NIC) --> can reach 10.88.0.2:5000
  |     |
  |     +-- k8s-mycluster-node2 (10.88.0.Y)
  |           +-- VM (passt NIC) --> can reach 10.88.0.2:5000
```

VMs reach the registry via passt (NIC1), which translates VM traffic through the container's network stack. DNS entry `registry.cluster.local -> 10.88.0.2` is added to dnsmasq on node1.

### Container Details

| Property | Value |
|----------|-------|
| Container name | `bink-registry` |
| Image | `docker.io/library/registry:2` |
| Network | `podman` (bridge, static IP `10.88.0.2`) |
| Port | `5000:5000/tcp` |
| Volume | `bink-registry-data` → `/var/lib/registry` |
| Labels | `bink.component=registry` |

### VM Configuration

CRI-O insecure registry (`/etc/crio/crio.conf.d/03-local-registry.conf`):
```ini
[crio.image]
insecure_registries = ["10.88.0.2:5000", "registry.cluster.local:5000"]
```

Containers registries.conf (`/etc/containers/registries.conf.d/10-local-registry.conf`):
```toml
[[registry]]
location = "10.88.0.2:5000"
insecure = true

[[registry]]
location = "registry.cluster.local:5000"
insecure = true
```

## Podman Integration

bink uses Podman Go bindings (`github.com/containers/podman/v6/pkg/bindings`) for all container operations.

## TODOs

### Replace virt-install with Libvirt Go Bindings

Currently, `internal/virsh/client.go` shells out to `virt-install`, `virsh`, and `qemu-img` inside the container via `podman exec`. Replace these with the libvirt Go bindings (`libvirt.org/go/libvirt`) to manage VM lifecycle (define, start, destroy, undefine) programmatically and generate domain XML as Go structs instead of building CLI argument lists.

### Replace SSH/SCP Shell-outs with Go SSH Bindings

Currently, `internal/ssh/ssh.go` shells out to `ssh` and `scp` binaries inside the container via `podman exec`. Replace these with `golang.org/x/crypto/ssh` (and `github.com/pkg/sftp` for file transfers) to eliminate the binary dependency, enable connection reuse, and get native Go error handling. The `ssh-keygen` call in `internal/node/create.go` should also be replaced with `crypto/rsa` + `golang.org/x/crypto/ssh` for pure-Go key generation.

### Decouple DNS from Node1

Currently dnsmasq runs exclusively on node1, configured via cloud-init during cluster creation. All other nodes point their DNS at node1's cluster IP (`10.0.0.x`). This means, if node1 is rebooted or temporarily unavailable, all cluster DNS resolution breaks for other nodes

The DNS service should be moved out of node1 so that cluster name resolution is not dependent on a single node's availability.

### Add Unit Tests

The project currently has no unit tests. Add comprehensive unit tests for core internal packages. Tests should use table-driven style and run via `make test-unit` (`go test -v -race ./internal/...`).

### Centralize Hardcoded Images

Image references are scattered across `internal/config/defaults.go`, `internal/cluster/init.go`, `internal/cluster/images.go`, Containerfiles, and test files. Centralize all image references (including Calico version `v3.27.0`, base Fedora `quay.io/fedora/fedora:43`, registry `docker.io/library/registry:2`, and test images like `quay.io/libpod/busybox:latest`) into a single configuration source so they can be updated in one place.

