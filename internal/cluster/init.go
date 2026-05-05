package cluster

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"text/template"
	"time"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/kube"
	"github.com/bootc-dev/bink/internal/ssh"
)

//go:embed kubeadm-config.yaml.tmpl
var kubeadmConfigTmpl string

//go:embed calico.yaml
var calicoManifest []byte

var kubeadmConfigTemplate = template.Must(template.New("kubeadm-config").Parse(kubeadmConfigTmpl))

type kubeadmConfigParams struct {
	AdvertiseAddress string
}

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

	// Initialize cluster without CoreDNS - it will be installed after Calico CNI
	// is ready so that CoreDNS pods get routable Calico IPs from the start.
	c.logger.Infof("=== Initializing Kubernetes cluster on %s ===", nodeName)
	c.logger.Info("")

	if err := sshClient.ExecWithOutput(ctx, "sudo kubeadm init --config /etc/kubernetes/kubeadm-config.yaml --skip-phases=addon/coredns"); err != nil {
		return fmt.Errorf("kubeadm init failed: %w", err)
	}

	c.logger.Info("")

	// Set up kubectl for core user
	c.logger.Info("=== Setting up kubectl for core user ===")
	if _, err := sshClient.Exec(ctx, "mkdir -p $HOME/.kube && sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config && sudo chown $(id -u):$(id -g) $HOME/.kube/config"); err != nil {
		return fmt.Errorf("failed to setup kubectl: %w", err)
	}

	c.logger.Info("")

	// Build a Kubernetes client for the remaining operations
	kubeClient, err := c.newKubeClient(ctx, sshClient, containerName)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Install Calico CNI
	c.logger.Info("=== Installing Calico CNI plugin ===")
	if err := kubeClient.Apply(ctx, calicoManifest); err != nil {
		return fmt.Errorf("failed to install Calico: %w", err)
	}

	c.logger.Info("CNI plugins will be installed to /opt/cni/bin (tmpfs overlay for bootc)")

	// Wait for Calico to write its CNI config before installing CoreDNS
	c.logger.Info("Waiting for Calico CNI configuration...")
	if err := c.waitForCalicoCNI(ctx, sshClient); err != nil {
		return fmt.Errorf("failed waiting for Calico CNI: %w", err)
	}
	c.logger.Info("✓ Calico CNI configuration ready")

	// Now install CoreDNS - Calico CNI is ready so pods get routable IPs
	c.logger.Info("")
	c.logger.Info("=== Installing CoreDNS ===")
	if err := sshClient.ExecWithOutput(ctx, "sudo kubeadm init phase addon coredns --config /etc/kubernetes/kubeadm-config.yaml"); err != nil {
		return fmt.Errorf("failed to install CoreDNS: %w", err)
	}

	// Patch CoreDNS to run as root - CRI-O doesn't set ambient capabilities
	// for non-root users, so NET_BIND_SERVICE doesn't take effect for UID 65532
	c.logger.Info("")
	c.logger.Info("=== Patching CoreDNS for CRI-O compatibility ===")
	corednsPatch := `[{"op": "replace", "path": "/spec/template/spec/containers/0/securityContext", "value": {"capabilities": {"add": ["NET_BIND_SERVICE"], "drop": ["ALL"]}, "readOnlyRootFilesystem": true, "runAsUser": 0, "runAsGroup": 0}}]`
	if err := kubeClient.PatchDeployment(ctx, "kube-system", "coredns", []byte(corednsPatch)); err != nil {
		return fmt.Errorf("failed to patch CoreDNS: %w", err)
	}

	c.logger.Info("")
	c.logger.Infof("✅ Cluster initialized on %s with Calico CNI", nodeName)
	c.logger.Info("")
	c.logger.Info("✅ Cluster DNS server already configured via cloud-init")
	c.logger.Infof("   Node %s will serve DNS on %s:53", nodeName, clusterIP)

	return nil
}

// newKubeClient fetches kubeconfig from the VM and creates a Kubernetes client
// that connects through the container's published API port.
func (c *Cluster) newKubeClient(ctx context.Context, sshClient *ssh.Client, containerName string) (*kube.Client, error) {
	kubeconfigContent, err := sshClient.Exec(ctx, "cat ~/.kube/config")
	if err != nil {
		return nil, fmt.Errorf("fetching kubeconfig: %w", err)
	}

	hostPort, err := c.podmanClient.GetPublishedPort(ctx, containerName, "6443/tcp")
	if err != nil {
		return nil, fmt.Errorf("getting API server port: %w", err)
	}

	serverURL := fmt.Sprintf("https://localhost:%d", hostPort)
	return kube.NewFromKubeconfig(ctx, []byte(kubeconfigContent), serverURL)
}

// waitForCalicoCNI polls the VM until Calico's CNI config file appears in /etc/cni/net.d.
func (c *Cluster) waitForCalicoCNI(ctx context.Context, sshClient *ssh.Client) error {
	deadline := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timed out waiting for Calico CNI config in /etc/cni/net.d")
		case <-ticker.C:
			if _, err := sshClient.Exec(ctx, "test -f /etc/cni/net.d/10-calico.conflist"); err == nil {
				return nil
			}
		}
	}
}

// createKubeadmConfig creates the kubeadm config file in the container
func (c *Cluster) createKubeadmConfig(ctx context.Context, containerName string, advertiseAddress string) error {
	var buf bytes.Buffer
	if err := kubeadmConfigTemplate.Execute(&buf, kubeadmConfigParams{
		AdvertiseAddress: advertiseAddress,
	}); err != nil {
		return fmt.Errorf("failed to render kubeadm config: %w", err)
	}

	cmd := fmt.Sprintf("podman exec %s bash -c 'cat > /tmp/kubeadm-config.yaml << \"KUBEADM\"\n%sKUBEADM\n'", containerName, buf.String())

	execCmd := exec.CommandContext(ctx, "bash", "-c", cmd)
	output, err := execCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create config: %w: %s", err, string(output))
	}

	return nil
}
