package dns

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/node"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/containers/podman/v5/libpod/define"
)

type Manager struct {
	podman      *podman.Client
	clusterName string
}

func NewManager(clusterName string) (*Manager, error) {
	client, err := podman.NewClient()
	if err != nil {
		return nil, fmt.Errorf("creating podman client: %w", err)
	}
	return &Manager{
		podman:      client,
		clusterName: clusterName,
	}, nil
}

func (m *Manager) containerName() string {
	return fmt.Sprintf("%s%s-%s", config.ContainerNamePrefix, m.clusterName, config.DNSContainerName)
}

func (m *Manager) networkName() string {
	if m.clusterName == "" {
		return config.DefaultNetworkName
	}
	return m.clusterName
}

// EnsureContainer creates or starts the DNS container and returns its IP.
func (m *Manager) EnsureContainer(ctx context.Context) (string, error) {
	name := m.containerName()

	logrus.Info("Ensuring DNS container is running")

	exists, err := m.podman.ContainerExists(ctx, name)
	if err != nil {
		return "", fmt.Errorf("checking DNS container: %w", err)
	}

	if exists {
		status, err := m.podman.ContainerStatus(ctx, name)
		if err != nil {
			return "", fmt.Errorf("checking DNS status: %w", err)
		}

		switch status {
		case define.ContainerStateRunning.String():
			logrus.Info("DNS container already running")
			return m.getIP(ctx)
		default:
			logrus.Infof("DNS container exists but is %s, removing and recreating", status)
			if err := m.podman.ContainerRemove(ctx, name, true); err != nil {
				return "", fmt.Errorf("removing stale DNS container: %w", err)
			}
		}
	}

	if err := m.createContainer(ctx); err != nil {
		return "", err
	}
	return m.getIP(ctx)
}

func (m *Manager) getIP(ctx context.Context) (string, error) {
	ip, err := m.podman.ContainerInspect(ctx, m.containerName(), "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}")
	if err != nil {
		return "", fmt.Errorf("getting DNS container IP: %w", err)
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", fmt.Errorf("DNS container has no IP address")
	}
	return ip, nil
}

func (m *Manager) createContainer(ctx context.Context) error {
	name := m.containerName()

	if err := m.podman.EnsureImage(ctx, config.DNSImage); err != nil {
		return fmt.Errorf("ensuring DNS image: %w", err)
	}

	opts := &podman.ContainerCreateOptions{
		Name:    name,
		Image:   config.DNSImage,
		Network: m.networkName(),
		Labels: map[string]string{
			"bink.cluster-name": m.clusterName,
			"bink.component":    "dns",
		},
	}

	containerID, err := m.podman.ContainerCreate(ctx, opts)
	if err != nil {
		if strings.Contains(err.Error(), "is already in use") {
			logrus.Info("DNS container was created concurrently, ensuring it is running")
			return nil
		}
		return fmt.Errorf("creating DNS container: %w", err)
	}

	logrus.Infof("DNS container created: %s", containerID)
	return nil
}

func (m *Manager) AddEntry(ctx context.Context, nodeName string) error {
	nodeIP := node.CalculateClusterIP(m.clusterName, nodeName)

	logrus.Infof("Adding DNS entry: %s -> %s", nodeName, nodeIP)

	entry := fmt.Sprintf("%s %s %s.%s", nodeIP, nodeName, nodeName, config.ClusterDomain)

	cmd := []string{
		"sh", "-c",
		fmt.Sprintf(
			`flock /tmp/dns.lock sh -c 'tmp=$(mktemp /tmp/cluster-hosts.XXXXXX) && grep -v "^[^#]*[[:space:]]%s[[:space:]]" %s > "$tmp" 2>/dev/null || true && echo "%s" >> "$tmp" && mv "$tmp" %s' && kill -HUP 1`,
			nodeName, config.DNSMasqHostsFile, entry, config.DNSMasqHostsFile,
		),
	}

	if _, err := m.podman.ContainerExec(ctx, m.containerName(), cmd); err != nil {
		return fmt.Errorf("adding DNS entry: %w", err)
	}

	logrus.Infof("DNS entry added: %s -> %s", nodeName, nodeIP)
	return nil
}

func (m *Manager) StopContainer(ctx context.Context) error {
	name := m.containerName()

	exists, err := m.podman.ContainerExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking DNS container: %w", err)
	}

	if !exists {
		return nil
	}

	logrus.Info("Stopping DNS container")
	if err := m.podman.ContainerStop(ctx, name); err != nil {
		logrus.Warnf("Failed to stop DNS container: %v", err)
	}

	if err := m.podman.ContainerRemove(ctx, name, true); err != nil {
		return fmt.Errorf("removing DNS container: %w", err)
	}

	logrus.Info("DNS container stopped and removed")
	return nil
}
