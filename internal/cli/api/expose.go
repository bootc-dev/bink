package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/internal/ssh"
)

func newExposeCmd() *cobra.Command {
	var nodeName string
	var kubeconfigPath string

	cmd := &cobra.Command{
		Use:   "expose",
		Short: "Expose API server to localhost via published port",
		Long: `Expose the Kubernetes API server to localhost via SSH tunnel.

This command:
1. Detects the published API server port on the host (e.g., 6443, 6444, or auto-assigned)
2. Sets up an SSH tunnel from container:6443 to VM:6443
3. Generates a kubeconfig file configured to use localhost:<published-port>
4. Requires the container to have port 6443 published (handled by 'bink cluster start')`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			return runExpose(cmd.Context(), logger, nodeName, kubeconfigPath)
		},
	}

	cmd.Flags().StringVarP(&nodeName, "node", "n", "node1", "Node name (control plane)")
	cmd.Flags().StringVarP(&kubeconfigPath, "kubeconfig", "k", filepath.Join(config.DefaultKubeconfigDir, "kubeconfig"), "Path to save kubeconfig")

	return cmd
}

func runExpose(ctx context.Context, logger *logrus.Logger, nodeName, kubeconfigPath string) error {
	// Build cluster-aware container name
	clusterName := viper.GetString("cluster.name")

	// Use cluster-specific kubeconfig path
	defaultPath := filepath.Join(config.DefaultKubeconfigDir, "kubeconfig")
	if kubeconfigPath == defaultPath {
		kubeconfigPath = filepath.Join(config.DefaultKubeconfigDir, fmt.Sprintf("kubeconfig-%s", clusterName))
	}

	containerName := fmt.Sprintf("%s%s-%s", config.ContainerNamePrefix, clusterName, nodeName)

	podmanClient, err := podman.NewClient()
	if err != nil {
		return fmt.Errorf("creating podman client: %w", err)
	}

	exists, err := podmanClient.ContainerExists(ctx, containerName)
	if err != nil {
		return fmt.Errorf("checking container existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("container %s does not exist", containerName)
	}

	// Get the published host port for the API server (6443/tcp inside container)
	hostPort, err := podmanClient.GetPublishedPort(ctx, containerName, "6443/tcp")
	if err != nil {
		logger.Error("❌ Container does not have port 6443 published")
		logger.Error("")
		logger.Errorf("The container needs to be created with a published API port")
		logger.Error("This is handled automatically by 'bink cluster start'")
		return fmt.Errorf("container missing port 6443 publication: %w", err)
	}

	logger.Infof("=== Exposing API server to localhost:%d ===", hostPort)
	logger.Info("")
	logger.Infof("Container port 6443 is published on host port %d", hostPort)
	logger.Infof("SSH endpoint: localhost:%d (inside container)", config.DefaultSSHPort)

	active, err := ssh.IsTunnelActive(ctx, containerName, "6443")
	if err != nil {
		return fmt.Errorf("checking tunnel status: %w", err)
	}

	if active {
		logger.Info("✓ Port 6443 is already being forwarded in container")
	} else {
		logger.Info("Starting SSH port forwarding inside container: 6443 -> VM:6443")

		tunnelCfg := ssh.TunnelConfig{
			ContainerName: containerName,
			Host:          "localhost",
			Port:          fmt.Sprintf("%d", config.DefaultSSHPort),
			KeyPath:       config.ClusterKeyPath,
			User:          config.DefaultSSHUser,
			LocalPort:     "6443",
			RemotePort:    "6443",
			BindAddress:   "0.0.0.0",
			Logger:        logger,
			PodmanClient:  podmanClient,
		}

		if err := ssh.StartTunnel(ctx, tunnelCfg); err != nil {
			return fmt.Errorf("starting SSH tunnel: %w", err)
		}

		logger.Info("Waiting for tunnel to establish...")
		for i := 0; i < 5; i++ {
			active, err := ssh.IsTunnelActive(ctx, containerName, "6443")
			if err != nil {
				return fmt.Errorf("verifying tunnel: %w", err)
			}
			if active {
				break
			}
			if i == 4 {
				return fmt.Errorf("tunnel did not establish after retries")
			}
		}
	}

	active, err = ssh.IsTunnelActive(ctx, containerName, "6443")
	if err != nil {
		return fmt.Errorf("verifying tunnel: %w", err)
	}
	if !active {
		return fmt.Errorf("SSH tunnel is not active on port 6443")
	}

	logger.Infof("✅ API server exposed: localhost:%d -> container:6443 -> VM:6443", hostPort)
	logger.Info("")

	logger.Infof("Generating kubeconfig at %s...", kubeconfigPath)

	sshClient := ssh.NewClient(ssh.Config{
		ContainerName: containerName,
		Host:          "localhost",
		Port:          fmt.Sprintf("%d", config.DefaultSSHPort),
		KeyPath:       config.ClusterKeyPath,
		User:          config.DefaultSSHUser,
		Logger:        logger,
		PodmanClient:  podmanClient,
	})

	kubeconfigContent, err := sshClient.Exec(ctx, "cat ~/.kube/config")
	if err != nil {
		return fmt.Errorf("fetching kubeconfig from VM: %w", err)
	}

	// Replace the server URL with the actual published host port
	lines := strings.Split(kubeconfigContent, "\n")
	for i, line := range lines {
		if strings.Contains(line, "server:") && strings.Contains(line, "https://") {
			// Find where "server:" starts in the line to preserve indentation
			serverIndex := strings.Index(line, "server:")
			indent := line[:serverIndex]
			lines[i] = fmt.Sprintf("%sserver: https://localhost:%d", indent, hostPort)
		}
	}
	kubeconfigContent = strings.Join(lines, "\n")

	if err := os.MkdirAll(filepath.Dir(kubeconfigPath), 0755); err != nil {
		return fmt.Errorf("creating kubeconfig directory: %w", err)
	}

	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfigContent), 0600); err != nil {
		return fmt.Errorf("writing kubeconfig: %w", err)
	}

	logger.Infof("✅ Kubeconfig generated at %s", kubeconfigPath)
	logger.Infof("   Server URL: https://localhost:%d", hostPort)
	logger.Info("")
	logger.Info("Usage:")
	logger.Infof("  export KUBECONFIG=%s", kubeconfigPath)
	logger.Info("  kubectl get nodes")
	logger.Info("")

	return nil
}
