package cluster

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/ssh"
)

const kubeadmConfigTemplate = `apiVersion: kubeadm.k8s.io/v1beta3
kind: InitConfiguration
localAPIEndpoint:
  advertiseAddress: "%s"
  bindPort: 6443
nodeRegistration:
  criSocket: "unix:///var/run/crio/crio.sock"
---
apiVersion: kubeadm.k8s.io/v1beta3
kind: ClusterConfiguration
kubernetesVersion: "%s"
apiServer:
  certSANs:
  - "localhost"
  - "127.0.0.1"
controllerManager:
  extraArgs:
    flex-volume-plugin-dir: "/var/lib/kubelet/volumeplugins"
  extraVolumes:
  - name: flexvolume-dir
    hostPath: "/var/lib/kubelet/volumeplugins"
    mountPath: "/var/lib/kubelet/volumeplugins"
    readOnly: false
---
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
volumePluginDir: "/var/lib/kubelet/volumeplugins"
`

const calicoVersion = "v3.27.0"

// InitOptions holds options for cluster initialization
type InitOptions struct {
	NodeName string
	Timeout  time.Duration
}

// Init initializes a Kubernetes cluster on the control plane node
func (c *Cluster) Init(ctx context.Context, opts InitOptions) error {
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Minute
	}

	nodeName := opts.NodeName
	if nodeName == "" {
		nodeName = c.controlPlane
	}

	c.logger.Infof("=== Setting up kubeadm config on %s ===", nodeName)

	// Get cluster IP for display
	clusterIP := c.GetNodeClusterIP(nodeName)
	c.logger.Infof("SSH endpoint: %s:%s (for SSH from container)", ssh.DefaultSSHHost, ssh.DefaultSSHPort)
	c.logger.Infof("VM cluster IP: %s (for Kubernetes communication)", clusterIP)

	// Wait for cloud-init to complete
	if err := c.WaitForCloudInit(ctx, nodeName, opts.Timeout); err != nil {
		return err
	}

	// Create SSH client
	sshClient := ssh.NewClientForNode(c.name, nodeName, c.logger)

	// Create kubeadm config in container
	c.logger.Info("Creating kubeadm config file...")
	clusterLabel := c.name
	if clusterLabel == "" {
		clusterLabel = config.DefaultNetworkName
	}
	containerName := fmt.Sprintf("k8s-%s-%s", clusterLabel, nodeName)
	if err := c.createKubeadmConfig(ctx, containerName, clusterIP); err != nil {
		return fmt.Errorf("failed to create kubeadm config: %w", err)
	}

	// Copy config to VM
	c.logger.Info("Copying kubeadm config to VM...")
	if err := sshClient.CopyTo(ctx, "/tmp/kubeadm-config.yaml", "/tmp/kubeadm-config.yaml"); err != nil {
		return fmt.Errorf("failed to copy kubeadm config: %w", err)
	}

	// Move to proper location
	if _, err := sshClient.Exec(ctx, "sudo mkdir -p /etc/kubernetes && sudo mv /tmp/kubeadm-config.yaml /etc/kubernetes/kubeadm-config.yaml"); err != nil {
		return fmt.Errorf("failed to move kubeadm config: %w", err)
	}

	c.logger.Info("✓ kubeadm config created at /etc/kubernetes/kubeadm-config.yaml")
	c.logger.Info("")

	// Initialize cluster
	c.logger.Infof("=== Initializing Kubernetes cluster on %s ===", nodeName)
	c.logger.Info("")

	if err := sshClient.ExecWithOutput(ctx, "sudo kubeadm init --config /etc/kubernetes/kubeadm-config.yaml"); err != nil {
		return fmt.Errorf("kubeadm init failed: %w", err)
	}

	c.logger.Info("")

	// Set up kubectl for core user
	c.logger.Info("=== Setting up kubectl for core user ===")
	if _, err := sshClient.Exec(ctx, "mkdir -p $HOME/.kube && sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config && sudo chown $(id -u):$(id -g) $HOME/.kube/config"); err != nil {
		return fmt.Errorf("failed to setup kubectl: %w", err)
	}

	c.logger.Info("")

	// Install Calico CNI (use quay.io images instead of docker.io to match pre-pulled images)
	c.logger.Info("=== Installing Calico CNI plugin ===")
	calicoURL := fmt.Sprintf("https://raw.githubusercontent.com/projectcalico/calico/%s/manifests/calico.yaml", calicoVersion)
	calicoApplyCmd := fmt.Sprintf("curl -sL %s | sed 's|docker.io/calico/|quay.io/calico/|g' | kubectl apply -f -", calicoURL)
	if _, err := sshClient.Exec(ctx, calicoApplyCmd); err != nil {
		return fmt.Errorf("failed to install Calico: %w", err)
	}

	c.logger.Info("CNI plugins will be installed to /opt/cni/bin (tmpfs overlay for bootc)")

	// Patch CoreDNS to run as root - CRI-O doesn't set ambient capabilities
	// for non-root users, so NET_BIND_SERVICE doesn't take effect for UID 65532
	c.logger.Info("")
	c.logger.Info("=== Patching CoreDNS for CRI-O compatibility ===")
	corednsPatch := `kubectl patch deployment -n kube-system coredns --type=json -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/securityContext", "value": {"capabilities": {"add": ["NET_BIND_SERVICE"], "drop": ["ALL"]}, "readOnlyRootFilesystem": true, "runAsUser": 0, "runAsGroup": 0}}]'`
	if _, err := sshClient.Exec(ctx, corednsPatch); err != nil {
		return fmt.Errorf("failed to patch CoreDNS: %w", err)
	}

	c.logger.Info("")
	c.logger.Infof("✅ Cluster initialized on %s with Calico CNI", nodeName)
	c.logger.Info("")
	c.logger.Info("✅ Cluster DNS server already configured via cloud-init")
	c.logger.Infof("   Node %s will serve DNS on %s:53", nodeName, clusterIP)

	return nil
}

// createKubeadmConfig creates the kubeadm config file in the container
func (c *Cluster) createKubeadmConfig(ctx context.Context, containerName string, advertiseAddress string) error {
	kubeadmConfig := fmt.Sprintf(kubeadmConfigTemplate, advertiseAddress, config.KubernetesVersion)
	cmd := fmt.Sprintf("podman exec %s bash -c 'cat > /tmp/kubeadm-config.yaml << \"KUBEADM\"\n%sKUBEADM\n'", containerName, kubeadmConfig)

	execCmd := exec.CommandContext(ctx, "bash", "-c", cmd)
	output, err := execCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create config: %w: %s", err, string(output))
	}

	return nil
}
