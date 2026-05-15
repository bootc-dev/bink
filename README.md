# bink

A CLI tool for managing containerized Kubernetes clusters where each node is a Podman container running a VM inside.

## Prerequisites

Bink uses the Podman client API. Make sure you have a Podman socket (e.g.
`systemctl --user start podman.socket`) or remote connection available (via
`CONTAINER_HOST`).

## Installation

```bash
go install ./cmd/bink
```

(We're working on making bink run as a container as well!)

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
