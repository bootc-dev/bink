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
go install ./cmd/bink
```

(We're working on making bink run as a container as well!)

## Running via Container

Instead of building the binary locally, you can run bink directly from a container image:

```bash
# Start a cluster (mount host Podman socket)
podman run --rm -ti --network=host --security-opt label=disable \
  -v $XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock \
  -v $(pwd):/output \
  ghcr.io/alicefr/bink/bink:latest \
  cluster start

# Expose the API (kubeconfig is written to the mounted directory)
podman run --rm -ti --network=host --security-opt label=disable \
  -v $XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock \
  -v $(pwd):/output \
  ghcr.io/alicefr/bink/bink:latest \
  api expose
```

For convenience, create a shell alias:
```bash
alias bink='podman run --rm -ti --network=host --security-opt label=disable \
  -v $XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock \
  -v $(pwd):/output \
  ghcr.io/alicefr/bink/bink:latest'
```

Then use it like the native binary: `bink cluster start`, `bink api expose`, etc.

## Create a Cluster

```bash
# Create cluster with control plane
bink cluster start

# Access the cluster
bink api expose
export KUBECONFIG=$PWD/kubeconfig-podman
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
