package ssh

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bootc-dev/bink/internal/podman"
	"github.com/sirupsen/logrus"
)

const (
	// DefaultKeyPath is the path to the cluster SSH private key inside containers
	DefaultKeyPath = "/var/run/cluster/cluster.key"

	// DefaultSSHPort is the port-forwarded SSH port for VMs
	DefaultSSHPort = "2222"

	// DefaultSSHUser is the default user for SSH connections
	DefaultSSHUser = "core"

	// DefaultSSHHost is the host for port-forwarded SSH connections
	DefaultSSHHost = "localhost"
)

// EnsureHostKeys checks that SSH keys exist on the host
func EnsureHostKeys(keyDir string) error {
	privateKey := filepath.Join(keyDir, "cluster.key")
	publicKey := filepath.Join(keyDir, "cluster.key.pub")

	// Check if keys exist
	if _, err := os.Stat(privateKey); os.IsNotExist(err) {
		return fmt.Errorf("SSH private key not found at %s", privateKey)
	}

	if _, err := os.Stat(publicKey); os.IsNotExist(err) {
		return fmt.Errorf("SSH public key not found at %s", publicKey)
	}

	return nil
}

// NewClientForNode creates a new SSH client for a given node
func NewClientForNode(clusterName, nodeName string, logger interface{}) *Client {
	if clusterName == "" {
		clusterName = "podman"
	}
	containerName := fmt.Sprintf("k8s-%s-%s", clusterName, nodeName)

	podmanClient, err := podman.NewClient()
	if err != nil {
		if l, ok := logger.(*logrus.Logger); ok {
			l.Warnf("Failed to create podman client: %v", err)
		}
	}

	return NewClient(Config{
		ContainerName: containerName,
		Host:          DefaultSSHHost,
		Port:          DefaultSSHPort,
		KeyPath:       DefaultKeyPath,
		User:          DefaultSSHUser,
		Logger:        logger.(*logrus.Logger),
		PodmanClient:  podmanClient,
	})
}
