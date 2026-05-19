#!/bin/bash
# If podman is connected to a remote service, run bink directly
if [ "$(podman info --format '{{.Host.ServiceIsRemote}}' 2>/dev/null)" = "true" ]; then
    exec /usr/local/bin/bink "$@"
fi

# Set up cgroup v2 delegation for nested containers.
# Move our process to a child cgroup so we can enable controllers on the root.
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    mkdir -p /sys/fs/cgroup/init
    echo $$ > /sys/fs/cgroup/init/cgroup.procs 2>/dev/null || true
    for controller in $(cat /sys/fs/cgroup/cgroup.controllers 2>/dev/null); do
        echo "+${controller}" > /sys/fs/cgroup/cgroup.subtree_control 2>/dev/null || true
    done
fi

echo "Starting podman service; use 'podman exec' to run bink commands."
exec podman --log-level=info system service --time=0
