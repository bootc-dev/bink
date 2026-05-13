#!/bin/bash
# If podman is connected to a remote service, run bink directly
if [ "$(podman info --format '{{.Host.ServiceIsRemote}}' 2>/dev/null)" = "true" ]; then
    exec /usr/local/bin/bink "$@"
fi

echo "Starting podman service; use 'podman exec' to run bink commands."
exec podman --log-level=info system service --time=0
