package network

import (
	"context"
	"fmt"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/sirupsen/logrus"
)

// Remove removes a network
func (m *Manager) Remove(ctx context.Context, name string) error {
	exists, err := m.podman.NetworkExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking if network exists: %w", err)
	}

	if !exists {
		logrus.Debugf("Network '%s' does not exist, skipping removal", name)
		return nil
	}

	if err := m.podman.NetworkRemove(ctx, name); err != nil {
		return fmt.Errorf("removing network: %w", err)
	}

	logrus.Infof("Removed network '%s'", name)
	return nil
}

type Manager struct {
	podman *podman.Client
}

func NewManager() (*Manager, error) {
	client, err := podman.NewClient()
	if err != nil {
		return nil, fmt.Errorf("creating podman client: %w", err)
	}
	return &Manager{
		podman: client,
	}, nil
}

func (m *Manager) Create(ctx context.Context, name, subnet string) error {
	exists, err := m.podman.NetworkExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking if network exists: %w", err)
	}

	if exists {
		logrus.Infof("Network '%s' already exists", name)
		return nil
	}

	return m.podman.NetworkCreate(ctx, name, subnet)
}

func (m *Manager) GetSubnet(ctx context.Context, name string) (string, error) {
	subnet, err := m.podman.NetworkInspect(ctx, name, "{{range .Subnets}}{{.Subnet}}{{end}}")
	if err != nil {
		return "", fmt.Errorf("inspecting network: %w", err)
	}
	return subnet, nil
}

func (m *Manager) EnsureClusterNetwork(ctx context.Context, clusterName string) error {
	logrus.Info("Ensuring cluster network exists")

	// Use cluster-specific network name for isolation
	networkName := clusterName
	if networkName == "" {
		networkName = config.DefaultNetworkName
	}

	if err := m.Create(ctx, networkName, config.DefaultSubnet); err != nil {
		return fmt.Errorf("creating cluster network: %w", err)
	}

	subnet, err := m.GetSubnet(ctx, networkName)
	if err != nil {
		return fmt.Errorf("getting network subnet: %w", err)
	}

	logrus.Infof("Cluster network '%s' ready with subnet %s", networkName, subnet)
	return nil
}
