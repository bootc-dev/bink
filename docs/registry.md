# Registry Usage

## Overview

Bink provides two local registry endpoints backed by one shared image store.
This keeps the normal development workflow unauthenticated while providing an
authenticated, opt-in endpoint for testing image-pull credentials.

Images are written through the standard registry and can then be read through
either endpoint. The authenticated registry mounts the shared storage
read-only, so images are stored once and it cannot accept pushes.

The standard endpoint is always created with a cluster. The authenticated
endpoint is created only when both registry credential flags are supplied.
Omitting the credential flags does not stop an authenticated registry that is
already running.

## Prerequisites

Start a bink cluster as described in the [README](../README.md#create-a-cluster).
To use the authenticated registry, provide both `--registry-user` and
`--registry-password` to `bink cluster start`. There are no default
credentials. The examples use `integration-user` and `integration-password`;
use the same credentials when starting the cluster and creating the pull
secret.

```bash
bink cluster start \
  --registry-user integration-user \
  --registry-password integration-password
```

The examples below assume that `KUBECONFIG` is configured for the cluster.

## Registry Endpoints

Bink provides two registry endpoints backed by the same image storage:

| Purpose | Host endpoint | In-cluster endpoint | Access |
| --- | --- | --- | --- |
| Standard registry | `localhost:5000` | `registry.cluster.local:5000` | Unauthenticated push and pull |
| Authenticated registry | `localhost:5001` | `auth-registry.cluster.local:5001` | Authenticated pull only |

Use host endpoints from the development machine and in-cluster endpoints from
Pods or bink nodes. Do not use `localhost:5001` in a Kubernetes pull secret:
there, `localhost` would refer to the node rather than the host running bink.

Both host endpoints use plain HTTP, so Podman commands require
`--tls-verify=false`.

## Populate the Shared Image Store

Push images through the standard endpoint. The authenticated endpoint mounts
the same storage read-only, so the image is available for authenticated pulls
without a second copy:

```bash
podman pull quay.io/libpod/busybox:latest
podman tag quay.io/libpod/busybox:latest localhost:5000/busybox:test
podman push --tls-verify=false localhost:5000/busybox:test
```

The standard endpoint can also pull the image without credentials:

```bash
podman pull --tls-verify=false localhost:5000/busybox:test
```

Check that the authenticated endpoint accepts the configured credentials:

```bash
podman pull --tls-verify=false \
  --creds integration-user:integration-password \
  localhost:5001/busybox:test
```

Do not push to port 5001. A push reaches the registry but fails because its
storage is read-only; use `localhost:5000` instead.

## Pulling an Authenticated Image in Kubernetes

Create a pull secret in the same namespace as the Pod. For this example, set
its registry server to the authenticated in-cluster endpoint:

```bash
kubectl create secret docker-registry bink-registry-credentials \
  --docker-server=auth-registry.cluster.local:5001 \
  --docker-username=integration-user \
  --docker-password=integration-password
```

Save the following Pod manifest as `registry-test.yaml`:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: registry-test
spec:
  restartPolicy: Never
  imagePullSecrets:
    - name: bink-registry-credentials
  containers:
    - name: busybox
      image: auth-registry.cluster.local:5001/busybox:test
      imagePullPolicy: Always
      command: ["sleep", "3600"]
```

Apply the manifest and verify that Kubernetes pulled the image:

```bash
kubectl apply -f registry-test.yaml
kubectl get pod registry-test
```

The Pod should reach `Running`. If the Secret has incorrect credentials or
does not provide credentials matching the image registry, Kubernetes reports
an image-pull authentication failure. Kubernetes also supports glob and path
prefix matching for registry credentials; see its documentation on
[interpreting `config.json`](https://kubernetes.io/docs/concepts/containers/images/#interpretation-of-config-json).

You can also attach the pull secret to a ServiceAccount when several Pods in
the same namespace need it. The Pod-level `imagePullSecrets` field above is
the most direct way to test one pull.

This validates Kubernetes/CRI-O pulling a protected image. It does not itself
implement bootc-operator's separate pull-secret propagation to the host.

## Verify the Authenticated Endpoint

Use `bink registry info` to show the state and connection details for both
endpoints. It displays the configured authenticated-registry username but
never prints its password.

An anonymous request to the authenticated endpoint must be rejected:

```bash
curl -i http://localhost:5001/v2/
```

The response should contain `401 Unauthorized`. The authenticated `podman pull`
above and the Kubernetes Pod then demonstrate that the same stored image is
available when valid credentials are supplied.

## Lifecycle and Shared Use

The registries have fixed container names, host ports, and a shared Podman
volume. They are shared infrastructure for every bink cluster using the same
Podman instance, rather than resources owned by one cluster.

| Action | Result |
| --- | --- |
| `bink registry start` | Ensures the standard registry is running without creating the authenticated registry. |
| `bink registry start --registry-user USER --registry-password PASSWORD` | Ensures both registries are running with the supplied credentials. |
| `bink registry start --auth --registry-user USER --registry-password PASSWORD` | Ensures the authenticated registry is running without creating the standard registry. |
| `bink cluster stop` | Removes cluster resources but preserves both registry containers and images. |
| `bink cluster stop --remove-data` | Also preserves the registries and their image data. |
| `bink registry stop --auth` | Removes only the authenticated registry; port 5000 and shared images remain. |
| `bink registry stop` | Removes both registries and the shared image volume. Registry-hosted images are deleted. |

The start commands are ensure operations: they do not stop an endpoint that is
already running. If the authenticated registry already exists, the supplied
credentials must match its configuration. To use different credentials, stop
only that endpoint with `bink registry stop --auth`, then start it again.

Starting only the authenticated registry against an empty shared volume gives
it no images to serve. Populate the volume through port 5000 first.

Because the authenticated endpoint is shared, do not change its credentials
while another cluster or test using that Podman instance relies on it. Reusing
it with the same credentials is supported.

## Limitations

- The authenticated endpoint supports pulls only. Push images through
  `localhost:5000`.
- To change authenticated-registry credentials, run
  `bink registry stop --auth`, then recreate the endpoint with the new
  username and password.
- Usernames and passwords must both be non-empty. Usernames cannot contain a
  colon, carriage return, or newline.
- Credentials supplied as command-line flags can be visible in shell history
  and process listings. This feature is intended for development and testing,
  not production credential management.

## Development Notes

If bink cannot connect to Podman, start the user socket:

```bash
systemctl --user start podman.socket
```

When changing bink source, rebuild the native CLI with `make build-bink`
before testing. Contributors who change `containerfiles/dns/cluster-hosts`
must also rebuild the DNS image with `make build-dns-image`; otherwise an
existing DNS image will not resolve `auth-registry.cluster.local`.
