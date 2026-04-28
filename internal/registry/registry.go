package registry

import (
	"context"
	"fmt"
	"net"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/containers/podman/v5/libpod/define"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/sirupsen/logrus"
	nettypes "github.com/containers/common/libnetwork/types"
)

type Manager struct {
	podman *podman.Client
}

func NewManager() (*Manager, error) {
	client, err := podman.NewClient()
	if err != nil {
		return nil, fmt.Errorf("creating podman client: %w", err)
	}
	return &Manager{podman: client}, nil
}

func (m *Manager) EnsureRegistry(ctx context.Context) error {
	logrus.Info("Ensuring local registry is running")

	exists, err := m.podman.ContainerExists(ctx, config.RegistryContainerName)
	if err != nil {
		return fmt.Errorf("checking registry container: %w", err)
	}

	if exists {
		status, err := m.podman.ContainerStatus(ctx, config.RegistryContainerName)
		if err != nil {
			return fmt.Errorf("checking registry status: %w", err)
		}

		switch status {
		case define.ContainerStateRunning.String():
			logrus.Info("Registry already running")
			return nil
		default:
			logrus.Infof("Registry container exists but is %s, starting it", status)
			if err := m.podman.ContainerStart(ctx, config.RegistryContainerName); err != nil {
				return fmt.Errorf("starting registry: %w", err)
			}
			logrus.Info("Registry started")
			return nil
		}
	}

	logrus.Info("Creating registry container")

	if err := m.podman.VolumeCreate(ctx, config.RegistryVolume); err != nil {
		return fmt.Errorf("creating registry volume: %w", err)
	}

	opts := &podman.ContainerCreateOptions{
		Name:  config.RegistryContainerName,
		Image: config.RegistryImage,
		NetworkOptions: map[string]nettypes.PerNetworkOptions{
			config.DefaultNetworkName: {
				StaticIPs: []net.IP{net.ParseIP(config.RegistryStaticIP)},
			},
		},
		PortMappings: []nettypes.PortMapping{
			{
				HostPort:      uint16(config.RegistryPort),
				ContainerPort: uint16(config.RegistryPort),
				Protocol:      "tcp",
			},
		},
		Volumes: []*specgen.NamedVolume{
			{
				Name:    config.RegistryVolume,
				Dest:    "/var/lib/registry",
				Options: []string{"z"},
			},
		},
		Labels: map[string]string{
			"bink.component": "registry",
		},
	}

	if _, err := m.podman.ContainerCreate(ctx, opts); err != nil {
		return fmt.Errorf("creating registry container: %w", err)
	}

	logrus.Infof("Registry running at %s:%d (host: localhost:%d)",
		config.RegistryStaticIP, config.RegistryPort, config.RegistryPort)
	return nil
}

func (m *Manager) StopRegistry(ctx context.Context) error {
	exists, err := m.podman.ContainerExists(ctx, config.RegistryContainerName)
	if err != nil {
		return fmt.Errorf("checking registry container: %w", err)
	}

	if !exists {
		logrus.Info("Registry container not found")
		return nil
	}

	logrus.Info("Stopping registry container")
	if err := m.podman.ContainerStop(ctx, config.RegistryContainerName); err != nil {
		logrus.Warnf("Failed to stop registry: %v", err)
	}

	if err := m.podman.ContainerRemove(ctx, config.RegistryContainerName, true); err != nil {
		return fmt.Errorf("removing registry container: %w", err)
	}

	if err := m.podman.VolumeRemove(ctx, config.RegistryVolume); err != nil {
		logrus.Warnf("Failed to remove registry volume: %v", err)
	}

	logrus.Info("Registry stopped and removed")
	return nil
}

type RegistryStatus struct {
	Running   bool
	IP        string
	HostPort  int
	PushURL   string
	PullURL   string
}

func (m *Manager) RegistryInfo(ctx context.Context) (*RegistryStatus, error) {
	info := &RegistryStatus{
		IP:       config.RegistryStaticIP,
		HostPort: config.RegistryPort,
		PushURL:  fmt.Sprintf("localhost:%d", config.RegistryPort),
		PullURL:  fmt.Sprintf("%s.%s:%d", config.RegistryHostname, config.ClusterDomain, config.RegistryPort),
	}

	exists, err := m.podman.ContainerExists(ctx, config.RegistryContainerName)
	if err != nil {
		return info, fmt.Errorf("checking registry container: %w", err)
	}

	if !exists {
		return info, nil
	}

	status, err := m.podman.ContainerStatus(ctx, config.RegistryContainerName)
	if err != nil {
		return info, fmt.Errorf("checking registry status: %w", err)
	}

	info.Running = status == define.ContainerStateRunning.String()
	return info, nil
}
