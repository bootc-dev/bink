package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		Long: `Expose the Kubernetes API server to localhost via passt port forwarding.

This command:
1. Detects the published API server port on the host (e.g., 6443, 6444, or auto-assigned)
2. Verifies the API server is reachable inside the container via passt port forwarding
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
	clusterName := viper.GetString("cluster.name")

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

	hostPort, err := podmanClient.GetPublishedPort(ctx, containerName, "6443/tcp")
	if err != nil {
		logger.Error("Container does not have port 6443 published")
		logger.Errorf("The container needs to be created with a published API port")
		logger.Error("This is handled automatically by 'bink cluster start'")
		return fmt.Errorf("container missing port 6443 publication: %w", err)
	}

	logger.Infof("=== Exposing API server to localhost:%d ===", hostPort)
	logger.Info("")
	logger.Infof("Container port 6443 is published on host port %d", hostPort)

	logger.Info("Checking API server reachability via passt port forwarding...")

	reachable := false
	for i := 0; i < 5; i++ {
		if checkAPIReachable(ctx, podmanClient, containerName) {
			reachable = true
			break
		}
		if i < 4 {
			time.Sleep(2 * time.Second)
		}
	}

	if reachable {
		logger.Info("API server is reachable on container port 6443")
	} else {
		logger.Warn("API server is not yet reachable on container port 6443, continuing anyway")
	}

	logger.Infof("API server exposed: localhost:%d -> container:6443 (passt) -> VM:6443", hostPort)
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

	lines := strings.Split(kubeconfigContent, "\n")
	for i, line := range lines {
		if strings.Contains(line, "server:") && strings.Contains(line, "https://") {
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

	logger.Infof("Kubeconfig generated at %s", kubeconfigPath)
	logger.Infof("   Server URL: https://localhost:%d", hostPort)
	logger.Info("")
	logger.Info("Usage:")
	logger.Infof("  export KUBECONFIG=%s", kubeconfigPath)
	logger.Info("  kubectl get nodes")
	logger.Info("")

	return nil
}

func checkAPIReachable(ctx context.Context, client *podman.Client, containerName string) bool {
	err := client.ContainerExecQuiet(ctx, containerName, []string{
		"bash", "-c", "echo > /dev/tcp/localhost/6443",
	})
	return err == nil
}
