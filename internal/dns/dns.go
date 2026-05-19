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

const dnsLockPath = "/tmp/dns.lock"

func (m *Manager) AddEntry(ctx context.Context, nodeName string) error {
	nodeIP := node.CalculateClusterIP(m.clusterName, nodeName)
	containerName := m.containerName()

	logrus.Infof("Adding DNS entry: %s -> %s", nodeName, nodeIP)

	if _, err := m.podman.ContainerExec(ctx, containerName, []string{"mkdir", dnsLockPath}); err != nil {
		return fmt.Errorf("DNS update already in progress: %w", err)
	}
	defer func() {
		_, _ = m.podman.ContainerExec(ctx, containerName, []string{"rmdir", dnsLockPath})
	}()

	current, err := m.podman.ContainerExec(ctx, containerName, []string{"cat", config.DNSMasqHostsFile})
	if err != nil {
		if !strings.Contains(err.Error(), "No such file") {
			return fmt.Errorf("reading DNS hosts file: %w", err)
		}
		current = ""
	}

	var lines []string
	for _, line := range strings.Split(current, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == nodeName {
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	lines = append(lines, fmt.Sprintf("%s %s %s.%s", nodeIP, nodeName, nodeName, config.ClusterDomain))

	content := strings.Join(lines, "\n") + "\n"
	if err := m.podman.ContainerCopyContent(ctx, []byte(content), containerName, config.DNSMasqHostsFile, 0644); err != nil {
		return fmt.Errorf("writing DNS hosts file: %w", err)
	}

	if _, err := m.podman.ContainerExec(ctx, containerName, []string{"kill", "-HUP", "1"}); err != nil {
		return fmt.Errorf("reloading dnsmasq: %w", err)
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
