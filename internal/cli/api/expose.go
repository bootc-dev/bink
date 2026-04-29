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
	"github.com/bootc-dev/bink/internal/haproxy"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/internal/ssh"
)

func newExposeCmd() *cobra.Command {
	var nodeName string
	var kubeconfigPath string

	cmd := &cobra.Command{
		Use:   "expose",
		Short: "Expose API server to localhost via HAProxy load balancer",
		Long: `Expose the Kubernetes API server to localhost via the HAProxy load balancer.

This command:
1. Detects the HAProxy load balancer container for the cluster
2. Gets the published port on the host
3. Verifies API server reachability through HAProxy
4. Generates a kubeconfig file configured to use localhost:<haproxy-port>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			return runExpose(cmd.Context(), logger, nodeName, kubeconfigPath)
		},
	}

	cmd.Flags().StringVarP(&nodeName, "node", "n", "", "Node name to fetch kubeconfig from (auto-detected if not set)")
	cmd.Flags().StringVarP(&kubeconfigPath, "kubeconfig", "k", filepath.Join(config.DefaultKubeconfigDir, "kubeconfig"), "Path to save kubeconfig")

	return cmd
}

func runExpose(ctx context.Context, logger *logrus.Logger, nodeName, kubeconfigPath string) error {
	clusterName := viper.GetString("cluster.name")

	defaultPath := filepath.Join(config.DefaultKubeconfigDir, "kubeconfig")
	if kubeconfigPath == defaultPath {
		kubeconfigPath = filepath.Join(config.DefaultKubeconfigDir, fmt.Sprintf("kubeconfig-%s", clusterName))
	}

	podmanClient, err := podman.NewClient()
	if err != nil {
		return fmt.Errorf("creating podman client: %w", err)
	}

	// Get the HAProxy published port
	haproxyMgr, err := haproxy.NewManager(clusterName)
	if err != nil {
		return fmt.Errorf("creating haproxy manager: %w", err)
	}

	hostPort, err := haproxyMgr.GetPublishedPort(ctx)
	if err != nil {
		logger.Error("HAProxy load balancer not found or port not published")
		logger.Error("This is created automatically by 'bink cluster start'")
		return fmt.Errorf("HAProxy missing or port not published: %w", err)
	}

	logger.Infof("=== Exposing API server to localhost:%d via HAProxy ===", hostPort)
	logger.Info("")

	// Find a reachable control-plane node to fetch kubeconfig from
	containerName, err := findReachableNode(ctx, podmanClient, clusterName, nodeName, logger)
	if err != nil {
		return err
	}

	logger.Infof("API server exposed: localhost:%d -> HAProxy -> control-plane nodes:6443", hostPort)
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

// findReachableNode finds a control-plane node that is reachable via SSH.
// If nodeName is specified, it tries that node first. Otherwise it tries all
// control-plane nodes in the cluster.
func findReachableNode(ctx context.Context, podmanClient *podman.Client, clusterName, nodeName string, logger *logrus.Logger) (string, error) {
	// Build candidate list
	var candidates []string
	if nodeName != "" {
		candidates = append(candidates, fmt.Sprintf("%s%s-%s", config.ContainerNamePrefix, clusterName, nodeName))
	}

	// Discover all control-plane nodes
	filter := fmt.Sprintf("label=bink.cluster-name=%s", clusterName)
	containers, err := podmanClient.ContainerList(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("listing cluster containers: %w", err)
	}

	for _, name := range containers {
		component, _ := podmanClient.ContainerInspect(ctx, name, "{{index .Config.Labels \"bink.component\"}}")
		if component != "" {
			continue
		}
		_, err := podmanClient.GetPublishedPort(ctx, name, "6443/tcp")
		if err != nil {
			continue
		}
		// Avoid duplicating the explicitly requested node
		if nodeName != "" {
			explicit := fmt.Sprintf("%s%s-%s", config.ContainerNamePrefix, clusterName, nodeName)
			if name == explicit {
				continue
			}
		}
		candidates = append(candidates, name)
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no control-plane nodes found for cluster %s", clusterName)
	}

	// Try each candidate
	for _, containerName := range candidates {
		exists, err := podmanClient.ContainerExists(ctx, containerName)
		if err != nil || !exists {
			continue
		}

		reachable := false
		for i := 0; i < 3; i++ {
			if checkAPIReachable(ctx, podmanClient, containerName) {
				reachable = true
				break
			}
			if i < 2 {
				time.Sleep(2 * time.Second)
			}
		}

		if reachable {
			node, _ := podmanClient.ContainerInspect(ctx, containerName, "{{index .Config.Labels \"bink.node-name\"}}")
			logger.Infof("Using control-plane node %s to fetch kubeconfig", node)
			return containerName, nil
		}
	}

	return "", fmt.Errorf("no reachable control-plane node found")
}

func checkAPIReachable(ctx context.Context, client *podman.Client, containerName string) bool {
	err := client.ContainerExecQuiet(ctx, containerName, []string{
		"bash", "-c", "echo > /dev/tcp/localhost/6443",
	})
	return err == nil
}
