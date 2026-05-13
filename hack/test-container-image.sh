#!/bin/bash
set -euo pipefail

BINK_IMAGE="${BINK_IMAGE:-ghcr.io/alicefr/bink/bink:latest}"
if [ -n "${CONTAINER_HOST:-}" ]; then
    PODMAN_SOCK="${CONTAINER_HOST#unix://}"
elif [ -S "/run/podman/podman.sock" ]; then
    PODMAN_SOCK="/run/podman/podman.sock"
elif [ -S "${XDG_RUNTIME_DIR:-}/podman/podman.sock" ]; then
    PODMAN_SOCK="${XDG_RUNTIME_DIR}/podman/podman.sock"
else
    PODMAN_SOCK="/run/user/$(id -u)/podman/podman.sock"
fi

# Test mode: socket, nested, or all (default)
MODE="${1:-all}"

run_test() {
    local mode="$1"
    cluster_name="test-${mode}-$(head -c4 /dev/urandom | xxd -p)"
    nested_container=""

    echo ""
    echo "=========================================="
    echo "  Testing mode: ${mode}"
    echo "=========================================="

    bink_args=()
    case "${mode}" in
        socket)
            bink_args=(
                podman run --rm --network=host --security-opt label=disable
                -e CONTAINER_HOST=unix:///run/podman/podman.sock
                -v "${PODMAN_SOCK}:/run/podman/podman.sock"
                -v "$(pwd):/output"
                "${BINK_IMAGE}"
            )
            ;;
        nested)
            nested_container="bink-nested-${cluster_name}"
            echo "Starting bink daemon container: ${nested_container}"
            podman run -d --name "${nested_container}" --privileged \
                --device /dev/kvm \
                -v "bink-test-storage:/var/lib/containers" \
                -v "$(pwd):/output" \
                "${BINK_IMAGE}"
            echo "Waiting for podman service inside container..."
            for _ in $(seq 1 30); do
                if podman exec "${nested_container}" podman info &>/dev/null; then
                    break
                fi
                sleep 0.5
            done
            bink_args=(podman exec "${nested_container}" bink)
            ;;
        *)
            echo "Unknown mode: ${mode}"
            exit 1
            ;;
    esac

    cleanup() {
        echo "--- Cleaning up ${cluster_name} ---"
        "${bink_args[@]}" cluster stop --remove-data --cluster-name "${cluster_name}" || true
        rm -f "kubeconfig-${cluster_name}"
        if [ -n "${nested_container}" ]; then
            podman rm -f "${nested_container}" 2>/dev/null || true
        fi
    }
    trap cleanup EXIT

    echo "--- bink --help ---"
    "${bink_args[@]}" --help

    echo "--- bink cluster start ---"
    "${bink_args[@]}" cluster start --cluster-name "${cluster_name}" --api-port 0

    echo "--- bink api expose ---"
    "${bink_args[@]}" api expose --cluster-name "${cluster_name}"
    test -f "kubeconfig-${cluster_name}"

    echo "--- bink cluster list ---"
    "${bink_args[@]}" cluster list | grep "${cluster_name}"

    echo "--- bink cluster stop ---"
    "${bink_args[@]}" cluster stop --remove-data --cluster-name "${cluster_name}"
    rm -f "kubeconfig-${cluster_name}"

    trap - EXIT
    echo "=== Mode ${mode}: PASSED ==="
}

echo "=== Testing bink container image: ${BINK_IMAGE} ==="

case "${MODE}" in
    all)
        run_test socket
        run_test nested
        ;;
    *)
        run_test "${MODE}"
        ;;
esac

echo ""
echo "=== All container image tests passed ==="
