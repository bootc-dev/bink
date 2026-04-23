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

