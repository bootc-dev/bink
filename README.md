# bink

A CLI tool for creating Kubernetes dev clusters from bootc images.

Its primary audience is K8s developers who want a "real enough" Kubernetes
cluster running on real bootc hosts for hacking and CI. It's easy to use, fast,
and runs unprivileged. It includes a local OCI registry for sharing images with
the cluster, networking for seamless cross-node communications, and HAProxy for
the API server.

## Prerequisites

Bink uses the Podman client API. Make sure you have a Podman socket (e.g.
`systemctl --user start podman.socket`) or remote connection available (via
`CONTAINER_HOST`).

## Installation

```bash
make build-bink
```
## Running via Container

Instead of building the binary locally, you can run bink directly from a container image:

```bash
# Start a cluster (mount host Podman socket)
podman run --rm -ti --network=host --security-opt label=disable \
  -v $XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock \
  -e CONTAINER_HOST=unix:///run/podman/podman.sock \
  -v $(pwd):/output \
  ghcr.io/alicefr/bink/bink:latest \
  cluster start

# Expose the API (kubeconfig is written to the mounted directory)
podman run --rm -ti --network=host --security-opt label=disable \
  -v $XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock \
  -e CONTAINER_HOST=unix:///run/podman/podman.sock \
  -v $(pwd):/output \
  ghcr.io/alicefr/bink/bink:latest \
  api expose
```

For convenience, create a shell alias:
```bash
alias bink='podman run --rm -ti --network=host --security-opt label=disable \
  -v $XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock \
  -e CONTAINER_HOST=unix:///run/podman/podman.sock \
  -v $(pwd):/output \
  ghcr.io/alicefr/bink/bink:latest'
```

Then use it like the native binary: `bink cluster start`, `bink api expose`, etc.

### Nested containerization (no socket mount)

If you don't want to mount the host podman socket, bink can run podman inside the container. The container starts a podman service and all bink commands are run via `podman exec`:

```bash
# Start the bink container (runs podman service in the background)
podman run -d --name bink --privileged \
  --device /dev/kvm \
  -v bink-storage:/var/lib/containers \
  -v $(pwd):/output \
  ghcr.io/alicefr/bink/bink:latest

# Wait for podman service to be ready inside the container
until podman exec bink podman info &>/dev/null; do sleep 0.5; done

# Run bink commands
podman exec bink bink cluster start
podman exec bink bink api expose
podman exec bink bink cluster list

# Use kubectl from inside the bink container
podman exec bink kubectl --kubeconfig /output/kubeconfig-podman get nodes

# Stop and remove when done
podman exec bink bink cluster stop --remove-data
podman rm -f bink
```

The `bink-storage` volume persists container images across runs so they don't need to be re-downloaded each time.

**Note:** In nested mode the cluster and its API ports live inside the bink container. Use `kubectl` from inside the container, or use the socket-mount mode for host-level access.

## Create a Cluster

```bash
# Create cluster with control plane
bink cluster start

# Create and export KUBECONFIG
eval $(bink api expose)
kubectl get pods -A

# Add worker nodes (optional)
bink node add node2
bink node add node3
kubectl get nodes
```

## List Clusters

```bash
# List all running clusters
bink cluster list
```

## Delete a Cluster

```bash
# Stop and remove all nodes
bink cluster stop

# Stop and also remove persistent data (SSH keys, kubeconfig)
bink cluster stop --remove-data
```
