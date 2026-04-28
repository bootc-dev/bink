# Bink Architecture

Bink manages multi-node Kubernetes clusters where each node runs as a rootless Podman container hosting a libvirt/QEMU virtual machine. This architecture delivers VM-level isolation with container-level convenience.

```
┌─────────────────────────────────────────────────────────────────────┐
│ Host                                                                │
│                                                                     │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │ Podman bridge network (10.88.0.0/16)                          │  │
│  │                                                               │  │
│  │  ┌─────────────────────────────┐                              │  │
│  │  │ Container: k8s-dev-node1    │                              │  │
│  │  │                             │                              │  │
│  │  │  libvirt daemons            │                              │  │
│  │  │  virtiofsd                  │                              │  │
│  │  │                             │                              │  │
│  │  │  ┌───────────────────────┐  │                              │  │
│  │  │  │ VM (Fedora bootc)     │  │                              │  │
│  │  │  │                       │  │                              │  │
│  │  │  │  kubelet + CRI-O      │  │                              │  │
│  │  │  │  kubeadm control-plane│  │                              │  │
│  │  │  │                       │  │                              │  │
│  │  │  │  NIC1 (passt)         │──┼── host:random-port ←→ 6443   │  │
│  │  │  │  NIC2 (multicast)     │──┼── 10.0.0.0/24 cluster net    │  │
│  │  │  └───────────────────────┘  │                              │  │
│  │  └─────────────────────────────┘                              │  │
│  │                  ║ multicast 230.0.0.1:5558                   │  │
│  │  ┌─────────────────────────────┐                              │  │
│  │  │ Container: k8s-dev-node2    │                              │  │
│  │  │                             │                              │  │
│  │  │  ┌───────────────────────┐  │                              │  │
│  │  │  │ VM (Fedora bootc)     │  │                              │  │
│  │  │  │  kubelet + CRI-O      │  │                              │  │
│  │  │  │  worker node          │  │                              │  │
│  │  │  │  NIC1 (passt)         │──┼── internet access            │  │
│  │  │  │  NIC2 (multicast)     │──┼── 10.0.0.0/24 cluster net    │  │
│  │  │  └───────────────────────┘  │                              │  │
│  │  └─────────────────────────────┘                              │  │
│  │                                                               │  │
│  │  ┌─────────────────────────────┐                              │  │
│  │  │ bink-registry (10.88.0.2)   │                              │  │
│  │  │ registry:2 on port 5000     │                              │  │
│  │  └─────────────────────────────┘                              │  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

## Components

### Container

Each Kubernetes node is a Podman container running the `localhost/cluster:latest` image (Fedora 43 with libvirt, QEMU, and virtiofsd). Containers are named `k8s-<cluster>-<node>` (e.g., `k8s-dev-node1`) and labeled with `bink.cluster-name` and `bink.node-name` for discovery.

The container runs four libvirt daemons (`virtlogd`, `virtstoraged`, `virtnetworkd`, `virtqemud`) and a `virtiofsd` instance. It requires `/dev/kvm` for hardware virtualization and `/dev/fuse` for virtiofs, plus `SYS_ADMIN` capability. SELinux is disabled inside the container.

Control-plane containers publish port 6443 to a random host port (or a user-specified port) for Kubernetes API access from the host.

### Virtual Machine

Inside each container, a Fedora bootc VM runs via libvirt/QEMU. The VM boots from a qcow2 overlay disk backed by a shared read-only base image (`fedora-bootc-k8s.qcow2`). Cloud-init configures the VM on first boot: hostname, networking, SSH keys, CRI-O, kubelet, and kernel parameters.

The VM runs:
- **CRI-O** as the container runtime
- **kubelet** managed by kubeadm
- **dnsmasq** (node1 only) for cluster DNS

Resources per VM default to 4 vCPUs and 8192 MB RAM, configurable via `--vcpus` and `--memory`.

### Local Registry

A shared container registry (`bink-registry`) runs alongside clusters on the Podman bridge network at a static IP (`10.88.0.2:5000`). It is auto-created on first cluster start and shared across all clusters.

VMs reach the registry through passt networking since passt translates VM traffic through the container's network stack, which sits on the Podman bridge. DNS entry `registry.cluster.local` points to `10.88.0.2` via dnsmasq on node1. CRI-O and containers/registries.conf are configured to trust the registry without TLS.

The registry survives cluster stops and is only removed via `bink registry stop`. Data is persisted in the `bink-registry-data` volume.

## Networking

Each VM has two network interfaces providing internet access and cluster communication.

### NIC1: passt (Internet and Host Access)

The first NIC uses passt user-mode networking, which translates VM traffic through the container's network namespace. This gives VMs internet access without requiring elevated privileges or a bridge inside the container.

Port forwarding rules map container ports to VM ports:
- `2222 → 22` (SSH) on all nodes
- `6443 → 6443` (Kubernetes API) on control-plane nodes only

The SSH access path from the host: `podman exec <container>` → `ssh -p 2222 core@localhost` → VM.

The API access path: `host:random-port` → `container:6443` (passt) → `VM:6443`.

### NIC2: Multicast (Cluster Network)

The second NIC connects VMs to the cluster-internal network (`10.0.0.0/24`) using UDP multicast at address `230.0.0.1:5558`. Multicast frames are bridged by Podman across all containers on the same bridge network, allowing VMs to communicate directly.

Each node gets a deterministic IP and MAC address derived from an MD5 hash of `<cluster-name>/<node-name>`:

```
IP:  10.0.0.{hash[0] % 240 + 10}     → range 10.0.0.10 to 10.0.0.250
MAC: 52:54:01:{hash[0]}:{hash[1]}:{hash[2]}
```

Including the cluster name in the hash avoids IP and MAC collisions when multiple clusters run on the same host. The scheme supports up to 240 nodes per cluster.

### DNS

dnsmasq runs on node1 and provides name resolution for the cluster network. All other nodes are configured (via cloud-init) to use node1's cluster IP as their DNS server.

When a new node joins, its hostname and cluster IP are added to `/var/lib/dnsmasq/cluster-hosts` on node1, and dnsmasq is restarted. Entries follow the format: `<cluster-ip> <node-name> <node-name>.cluster.local`.

Upstream DNS servers (`8.8.8.8`, `8.8.4.4`) handle external resolution. The search domain is `cluster.local`.

### Network Diagram

```
Host
  │
  ├── localhost:<random-port> ──→ container:6443 (passt) ──→ VM:6443  [API]
  │
  └── podman exec ──→ container ssh -p 2222 ──→ VM:22                 [SSH]

Podman bridge (10.88.0.0/16)
  │
  ├── k8s-dev-node1 ──→ VM NIC1 (passt): internet + port fwd
  │                  ──→ VM NIC2 (mcast): 10.0.0.x/24
  │
  ├── k8s-dev-node2 ──→ VM NIC1 (passt): internet + port fwd
  │                  ──→ VM NIC2 (mcast): 10.0.0.y/24
  │
  └── bink-registry (10.88.0.2:5000) ──→ reachable from all VMs via passt

Multicast (230.0.0.1:5558)
  │
  └── VM-to-VM cluster traffic on 10.0.0.0/24 (Kubernetes pods, services, etcd)
```

## Storage

### Base VM Image

The base disk (`fedora-bootc-k8s.qcow2`) is built from a Fedora bootc container image and converted to qcow2 format. It is packaged into a container image (`localhost/fedora-bootc-k8s-image:latest`) and mounted as a Podman image volume at `/images` (read-only) in every node container.

All nodes across all clusters share the same base image. Per-node state lives in overlay disks.

### Overlay Disks

Each node gets a qcow2 overlay disk at `/workspace/<node-name>.qcow2` inside the container. The overlay uses the base image as its backing file, so it only stores changes (copy-on-write). The overlay lives in the container's ephemeral storage and is destroyed when the container is removed.

### Shared Container Images (virtiofs)

Kubernetes container images (apiserver, scheduler, controller-manager, etcd, coredns) and Calico CNI images are pre-pulled into a global Podman volume (`cluster-images`). This volume is populated once by a `cluster-images-populator` container using skopeo.

The volume is mounted at `/var/lib/cluster-images` in every node container. Inside the container, virtiofsd exposes this directory to the VM over a virtio filesystem socket. The VM mounts it at `/var/mnt/cluster_images` via a systemd mount unit, and CRI-O is configured with this path as an additional image store.

This means Kubernetes images are immediately available on every node without pulling from the network, and all nodes across all clusters share a single copy.

```
cluster-images volume (global)
  │
  └── mounted at /var/lib/cluster-images (container)
        │
        └── virtiofsd socket → VM mounts at /var/mnt/cluster_images
              │
              └── CRI-O reads images from additionalimagestores
```

### Volumes Summary

| Volume | Scope | Mount Path (Container) | Purpose |
|--------|-------|----------------------|---------|
| `cluster-images` | Global | `/var/lib/cluster-images` | Pre-pulled K8s/CNI images, shared via virtiofs |
| `<cluster>-cluster-keys` | Per cluster | `/var/run/cluster` | SSH key pair for VM access |
| `bink-registry-data` | Global | `/var/lib/registry` | Local registry storage |
| Image volume (`fedora-bootc-k8s-image`) | Global | `/images` (read-only) | Base qcow2 disk |

## Multi-Node Clusters

A cluster starts with a single control-plane node (`node1`) and can grow by adding worker or additional control-plane nodes.

### Cluster Initialization

1. Create the Podman bridge network for the cluster
2. Create the `cluster-keys` volume and generate an SSH key pair (RSA 4096-bit)
3. Ensure the global `cluster-images` volume is populated
4. Create the node1 container with libvirt daemons
5. Create a qcow2 overlay disk and a cloud-init ISO
6. Boot the VM via virt-install with dual NICs and virtiofs
7. Wait for cloud-init to complete (configures networking, CRI-O, kubelet)
8. Run `kubeadm init` with the node's cluster IP as the advertise address
9. Install Calico CNI and patch CoreDNS for CRI-O compatibility

### Adding Nodes

1. Create a new container and VM (same process as node1)
2. Register the new node's hostname and IP in dnsmasq on node1
3. Generate a join token on the control-plane: `kubeadm token create --print-join-command`
4. For control-plane nodes: upload certificates and join with `--control-plane`
5. For worker nodes: join and label with `node-role.kubernetes.io/worker=worker`

All nodes share the same `cluster-keys` volume so they can SSH to each other using the same key pair. The cluster network (NIC2) provides direct VM-to-VM connectivity for Kubernetes traffic.

## Multi-Cluster

Multiple clusters run independently on the same host. Isolation is achieved through:

### Container Isolation

Each cluster uses a separate Podman bridge network named after the cluster. Containers are prefixed with the cluster name (`k8s-<cluster>-<node>`), so there are no naming conflicts.

### Network Isolation

- **Bridge network**: Each cluster gets its own Podman bridge, preventing container-level cross-talk
- **Cluster IPs**: The MD5 hash includes the cluster name (`<cluster>/<node>`), so `node1` in `cluster-a` gets a different IP and MAC than `node1` in `cluster-b`
- **Multicast**: Since each cluster runs on its own bridge network, multicast frames from one cluster do not reach another

### Volume Isolation

- **cluster-keys**: Per-cluster (`<cluster>-cluster-keys`), so each cluster has its own SSH keys
- **cluster-images**: Global and shared across all clusters (optimization — same K8s images are reused)
- **Registry**: Global and shared — push once, pull from any cluster

### Example: Two Clusters

```
Cluster "dev"                          Cluster "staging"
├── Network: dev (bridge)              ├── Network: staging (bridge)
├── Keys: dev-cluster-keys             ├── Keys: staging-cluster-keys
├── k8s-dev-node1                      ├── k8s-staging-node1
│   └── VM 10.0.0.x (hash dev/node1)  │   └── VM 10.0.0.y (hash staging/node1)
├── k8s-dev-node2                      └── k8s-staging-node2
│   └── VM 10.0.0.z (hash dev/node2)      └── VM 10.0.0.w (hash staging/node2)
│
└── Shared: cluster-images, bink-registry
```

## Cloud-Init

VMs are configured on first boot via cloud-init. An ISO (`cidata`) is generated per node containing three files:

- **meta-data**: Instance ID and hostname
- **network-config** (v2 format): `enp2s0` (DHCP via passt), `enp3s0` (static cluster IP)
- **user-data**: User setup (`core` with SSH key), system configuration, and startup commands

The user-data configures:
- CRI-O with insecure local registry and virtiofs additional image store
- kubelet with volume plugin directory
- Kernel parameters (IP forwarding, `br_netfilter`)
- virtiofs mount unit for shared images
- dnsmasq (node1 only) with initial host entries
- ostree overlay for `/opt` (CNI plugin binaries)

## Key Paths

### Inside Containers

| Path | Purpose |
|------|---------|
| `/var/run/cluster/cluster.key` | SSH private key |
| `/var/run/cluster/cluster.key.pub` | SSH public key |
| `/images/fedora-bootc-k8s.qcow2` | Base VM disk (read-only) |
| `/var/lib/cluster-images/` | Pre-pulled images (virtiofs source) |
| `/workspace/<node>.qcow2` | Node overlay disk |
| `/workspace/<node>-cloud-init.iso` | Cloud-init ISO |
| `/var/lib/libvirt/virtiofsd/virtiofsd.sock` | Virtiofs socket |

### Inside VMs

| Path | Purpose |
|------|---------|
| `/etc/kubernetes/admin.conf` | Kubernetes admin kubeconfig |
| `~/.kube/config` | User kubeconfig (core user) |
| `/var/mnt/cluster_images` | Virtiofs mount (shared K8s images) |
| `/var/lib/dnsmasq/cluster-hosts` | DNS entries (node1 only) |
| `/opt/cni/bin` | CNI plugin binaries (tmpfs overlay) |
