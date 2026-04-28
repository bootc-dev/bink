# Implementation Plan: bink

See [ARCHITECTURE.md](ARCHITECTURE.md) for the high-level architecture, networking, storage, and multi-cluster design.

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

### Replace entrypoint.sh with a Go Supervisor Binary

Currently, `containerfiles/vm/entrypoint.sh` is a bash script that starts `virtlogd`, `virtstoraged`, and `virtnetworkd` as background processes, then runs `virtqemud` in the foreground. Replace this with a small Go binary that starts and monitors all four libvirt daemons, restarts any that crash, and reports health status. This gives proper process supervision inside the container (restart policies, structured logging, health checks) without pulling in a full init system.

