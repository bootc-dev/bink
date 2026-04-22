package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/sirupsen/logrus"
)

func (n *Node) generateCloudInit(ctx context.Context) error {
	logrus.Infof("Creating cloud-init ISO for %s", n.Name)

	tmpDir, err := os.MkdirTemp("", "cloud-init-*")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Read public key from container volume
	pubKey, err := n.podman.ContainerExec(ctx, n.ContainerName, []string{"cat", config.ClusterKeyPubPath})
	if err != nil {
		return fmt.Errorf("reading public key: %w", err)
	}

	if err := n.writeMetaData(tmpDir); err != nil {
		return err
	}

	if err := n.writeNetworkConfig(tmpDir); err != nil {
		return err
	}

	if err := n.writeUserData(tmpDir, string(pubKey)); err != nil {
		return err
	}

	isoPath := fmt.Sprintf("/workspace/%s-cloud-init.iso", n.Name)
	files := []string{
		"/tmp/meta-data",
		"/tmp/user-data",
		"/tmp/network-config",
	}

	if err := n.podman.ContainerCopy(ctx, filepath.Join(tmpDir, "meta-data"), n.ContainerName, "/tmp/meta-data"); err != nil {
		return fmt.Errorf("copying meta-data: %w", err)
	}
	if err := n.podman.ContainerCopy(ctx, filepath.Join(tmpDir, "user-data"), n.ContainerName, "/tmp/user-data"); err != nil {
		return fmt.Errorf("copying user-data: %w", err)
	}
	if err := n.podman.ContainerCopy(ctx, filepath.Join(tmpDir, "network-config"), n.ContainerName, "/tmp/network-config"); err != nil {
		return fmt.Errorf("copying network-config: %w", err)
	}

	if err := n.virsh.Genisoimage(ctx, isoPath, config.CloudInitVolID, files); err != nil {
		return fmt.Errorf("creating ISO: %w", err)
	}

	logrus.Infof("Cloud-init ISO created at %s", isoPath)
	return nil
}

func (n *Node) writeMetaData(dir string) error {
	content := fmt.Sprintf(`instance-id: %s
local-hostname: %s
`, n.Name, n.Name)

	return os.WriteFile(filepath.Join(dir, "meta-data"), []byte(content), 0644)
}

func (n *Node) writeNetworkConfig(dir string) error {
	node1IP := CalculateClusterIP("node1")

	content := fmt.Sprintf(`version: 2
ethernets:
  enp2s0:
    dhcp4: true
  enp3s0:
    dhcp4: false
    dhcp6: false
    addresses:
      - %s/24
    nameservers:
      search: [%s]
      addresses: [%s, %s]
    optional: true
`, n.ClusterIP, config.ClusterDomain, node1IP, config.UpstreamDNS1)

	return os.WriteFile(filepath.Join(dir, "network-config"), []byte(content), 0644)
}

func (n *Node) writeUserData(dir, sshPubKey string) error {
	dnsmasqConfig := ""
	dnsmasqRuncmd := ""

	if n.Name == "node1" {
		dnsmasqConfig = fmt.Sprintf(`  - path: /etc/dnsmasq.d/cluster.conf
    content: |
      listen-address=%s
      bind-interfaces
      no-hosts
      addn-hosts=/var/lib/dnsmasq/cluster-hosts
      domain=%s
      domain-needed
      bogus-priv
      server=%s
      server=%s
      cache-size=1000
  - path: /var/lib/dnsmasq/cluster-hosts
    owner: dnsmasq:dnsmasq
    permissions: '0644'
    content: |
      %s %s %s.%s
  - path: /etc/systemd/system/dnsmasq.service.d/wait-for-network.conf
    content: |
      [Unit]
      After=network-online.target
      Wants=network-online.target
`, n.ClusterIP, config.ClusterDomain, config.UpstreamDNS1, config.UpstreamDNS2,
			n.ClusterIP, n.Name, n.Name, config.ClusterDomain)

		dnsmasqRuncmd = `  - chown dnsmasq:dnsmasq /var/lib/dnsmasq/cluster-hosts
  - restorecon -v /var/lib/dnsmasq/cluster-hosts || true
  - systemctl daemon-reload
  - |
    # Wait for enp3s0 to be up before starting dnsmasq
    for i in {1..30}; do
      if ip link show enp3s0 | grep -q "state UP"; then
        echo "enp3s0 is up"
        break
      fi
      echo "Waiting for enp3s0... ($i/30)"
      sleep 1
    done
  - systemctl enable --now dnsmasq`
	}

	content := fmt.Sprintf(`#cloud-config
hostname: %s
users:
  - name: %s
    ssh_authorized_keys:
      - %s
    sudo: ALL=(ALL) NOPASSWD:ALL
    groups: wheel
    shell: /bin/bash

write_files:
  - path: /etc/kubernetes/kubelet-config.yaml
    content: |
      apiVersion: kubelet.config.k8s.io/v1beta1
      kind: KubeletConfiguration
      volumePluginDir: /var/lib/kubelet/volumeplugins
  - path: /etc/sysconfig/kubelet
    content: |
      KUBELET_EXTRA_ARGS=--volume-plugin-dir=/var/lib/kubelet/volumeplugins
  - path: /etc/containers/storage.conf
    content: |
      [storage]
      driver = "overlay"
      runroot = "/run/containers/storage"
      graphroot = "/var/lib/containers/storage"

      [storage.options]
      additionalimagestores = [
        "/var/mnt/cluster_images",
      ]
  - path: /etc/crio/crio.conf.d/01-cni-plugins.conf
    content: |
      [crio.network]
      plugin_dirs = [
        "/opt/cni/bin",
        "/var/lib/cni/bin",
        "/usr/libexec/cni",
      ]
  - path: /etc/crio/crio.conf.d/02-capabilities.conf
    content: |
      [crio.runtime]
      add_inheritable_capabilities = true
  - path: /etc/systemd/system/var-mnt-cluster_images.mount
    content: |
      [Unit]
      Description=Mount cluster images via virtiofs
      Before=crio.service

      [Mount]
      What=cluster_images
      Where=/var/mnt/cluster_images
      Type=virtiofs

      [Install]
      WantedBy=multi-user.target
%s
runcmd:
  - swapoff -a
  - sed -i '/swap/d' /etc/fstab
  - modprobe br_netfilter
  - echo 'br_netfilter' > /etc/modules-load.d/k8s-bridge.conf
  - sysctl -w net.ipv4.ip_forward=1
  - echo 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-kubernetes.conf
  - mkdir -p /var/lib/kubelet/volumeplugins
  - mkdir -p /var/mnt/cluster_images
  - mkdir -p /var/lib/containers/storage
  - systemctl daemon-reload
  - systemctl enable --now var-mnt-cluster_images.mount
  - systemctl enable --now ostree-state-overlay@opt.service
  - systemctl enable --now qemu-guest-agent
  - nmcli connection modify "cloud-init enp3s0" ipv4.dns-search "~%s %s"
  - nmcli connection up "cloud-init enp3s0"
%s
  - systemctl enable --now crio
  - systemctl enable kubelet
`, n.Name, config.DefaultSSHUser, sshPubKey, dnsmasqConfig,
		config.ClusterDomain, config.ClusterDomain, dnsmasqRuncmd)

	return os.WriteFile(filepath.Join(dir, "user-data"), []byte(content), 0644)
}
