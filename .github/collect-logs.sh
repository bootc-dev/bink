#!/bin/bash
# Collect logs from bink test containers for CI debugging
# Writes per-container log files to $LOG_DIR (default: /tmp/bink-logs)

LOG_DIR="${LOG_DIR:-/tmp/bink-logs}"
mkdir -p "$LOG_DIR"

SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -i /var/run/cluster/cluster.key -p 2222"

ssh_vm() {
  local ctr="$1" cmd="$2"
  sudo podman exec "$ctr" ssh $SSH_OPTS core@localhost "$cmd" 2>&1
}

sudo podman ps -a 2>/dev/null > "$LOG_DIR/podman-ps.txt" || true

for ctr in $(sudo podman ps -a --filter "name=k8s-test-bink" --format '{{.Names}}' 2>/dev/null); do
  echo "Collecting logs for $ctr"
  dir="$LOG_DIR/$ctr"
  mkdir -p "$dir"

  sudo podman logs "$ctr" > "$dir/container.log" 2>&1 || true
  ssh_vm "$ctr" "sudo journalctl -n 200 --no-pager" > "$dir/journal.log" || echo "(VM not reachable)" > "$dir/journal.log"
  ssh_vm "$ctr" "sudo journalctl -u kubelet -n 100 --no-pager" > "$dir/kubelet.log" || true
  ssh_vm "$ctr" "sudo journalctl -u crio -n 100 --no-pager" > "$dir/crio.log" || true
  ssh_vm "$ctr" "cloud-init status --long" > "$dir/cloud-init.log" || true
  ssh_vm "$ctr" "sudo dmesg" > "$dir/dmesg.log" || true
done

df -h | sudo tee "$LOG_DIR/disk.txt" > /dev/null || true
free -h | sudo tee "$LOG_DIR/memory.txt" > /dev/null || true
sudo dmesg | tail -100 | sudo tee "$LOG_DIR/host-dmesg.txt" > /dev/null || true

echo "Logs collected in $LOG_DIR"
ls -R "$LOG_DIR"
