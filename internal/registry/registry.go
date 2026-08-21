// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/sirupsen/logrus"
	nettypes "go.podman.io/common/libnetwork/types"
	"go.podman.io/podman/v6/libpod/define"
	"go.podman.io/podman/v6/pkg/specgen"
	"golang.org/x/crypto/bcrypt"
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

	if err := m.podman.EnsureImage(ctx, config.RegistryImage); err != nil {
		return fmt.Errorf("ensuring registry image: %w", err)
	}

	if err := m.podman.VolumeCreate(ctx, config.RegistryVolume, nil); err != nil {
		return fmt.Errorf("creating registry volume: %w", err)
	}

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
			logrus.Infof("Registry container is %s, starting it", status)
			if err := m.podman.ContainerStart(ctx, config.RegistryContainerName); err != nil {
				return fmt.Errorf("starting registry: %w", err)
			}
			logrus.Info("Registry started")
			return nil
		}
	}

	if err := m.createContainer(ctx); err != nil {
		return err
	}

	logrus.Infof("Registry running at %s:%d (host: localhost:%d)",
		config.RegistryStaticIP, config.RegistryPort, config.RegistryPort)
	return nil
}

func (m *Manager) createContainer(ctx context.Context) error {
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
		Environment: map[string]string{
			"REGISTRY_HTTP_SECRET": config.RegistryHTTPSecret,
		},
		Labels: map[string]string{
			config.LabelComponent: "registry",
		},
	}

	_, err := m.podman.ContainerCreate(ctx, opts)
	if err != nil {
		if isContainerAlreadyExists(err) {
			logrus.Info("Registry container was created concurrently")
			return nil
		}
		return fmt.Errorf("creating registry container: %w", err)
	}
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
		if !isContainerGone(err) {
			return fmt.Errorf("removing registry container: %w", err)
		}
		logrus.Debug("Registry container already removed")
	}

	if err := m.podman.VolumeRemove(ctx, config.RegistryVolume); err != nil {
		logrus.Warnf("Failed to remove registry volume: %v", err)
	}

	logrus.Info("Registry stopped and removed")
	return nil
}

type RegistryStatus struct {
	Running  bool
	IP       string
	HostPort int
	PushURL  string
	PullURL  string
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

func (m *Manager) EnsureAuthRegistry(ctx context.Context) error {
	logrus.Info("Ensuring authenticated registry is running")

	if err := m.podman.EnsureImage(ctx, config.RegistryImage); err != nil {
		return fmt.Errorf("ensuring registry image: %w", err)
	}

	if err := m.podman.VolumeCreate(ctx, config.RegistryVolume, nil); err != nil {
		return fmt.Errorf("creating registry volume: %w", err)
	}

	exists, err := m.podman.ContainerExists(ctx, config.AuthRegistryContainerName)
	if err != nil {
		return fmt.Errorf("checking auth registry container: %w", err)
	}

	if exists {
		status, err := m.podman.ContainerStatus(ctx, config.AuthRegistryContainerName)
		if err != nil {
			return fmt.Errorf("checking auth registry status: %w", err)
		}
		switch status {
		case define.ContainerStateRunning.String():
			logrus.Info("Authenticated registry already running")
			return nil
		default:
			logrus.Infof("Auth registry container is %s, starting it", status)
			if err := m.podman.ContainerStart(ctx, config.AuthRegistryContainerName); err != nil {
				return fmt.Errorf("starting auth registry: %w", err)
			}
			logrus.Info("Authenticated registry started")
			return nil
		}
	}

	if err := m.createAuthContainer(ctx); err != nil {
		return err
	}

	logrus.Infof("Authenticated registry running at %s:%d (host: localhost:%d)",
		config.AuthRegistryStaticIP, config.AuthRegistryPort, config.AuthRegistryPort)
	return nil
}

func (m *Manager) createAuthContainer(ctx context.Context) error {
	htpasswdEntry, err := generateHtpasswd(config.AuthRegistryUsername, config.AuthRegistryPassword)
	if err != nil {
		return fmt.Errorf("generating htpasswd: %w", err)
	}

	opts := &podman.ContainerCreateOptions{
		Name:  config.AuthRegistryContainerName,
		Image: config.RegistryImage,
		Entrypoint: []string{"/bin/sh", "-c",
			fmt.Sprintf("mkdir -p /auth && printf '%s\\n' > /auth/htpasswd && /entrypoint.sh /etc/docker/registry/config.yml", htpasswdEntry),
		},
		NetworkOptions: map[string]nettypes.PerNetworkOptions{
			config.DefaultNetworkName: {
				StaticIPs: []net.IP{net.ParseIP(config.AuthRegistryStaticIP)},
			},
		},
		PortMappings: []nettypes.PortMapping{
			{
				HostPort:      uint16(config.AuthRegistryPort),
				ContainerPort: uint16(config.AuthRegistryPort),
				Protocol:      "tcp",
			},
		},
		Volumes: []*specgen.NamedVolume{
			{
				Name:    config.RegistryVolume,
				Dest:    "/var/lib/registry",
				Options: []string{"ro", "z"},
			},
		},
		Environment: map[string]string{
			"REGISTRY_HTTP_ADDR":           fmt.Sprintf("0.0.0.0:%d", config.AuthRegistryPort),
			"REGISTRY_AUTH":                "htpasswd",
			"REGISTRY_AUTH_HTPASSWD_REALM": "Registry Realm",
			"REGISTRY_AUTH_HTPASSWD_PATH":  "/auth/htpasswd",
			"REGISTRY_HTTP_SECRET":         config.RegistryHTTPSecret,
		},
		Labels: map[string]string{
			config.LabelComponent: "auth-registry",
		},
	}

	_, err = m.podman.ContainerCreate(ctx, opts)
	if err != nil {
		if isContainerAlreadyExists(err) {
			logrus.Info("Auth registry container was created concurrently")
			return nil
		}
		return fmt.Errorf("creating auth registry container: %w", err)
	}
	return nil
}

func (m *Manager) StopAuthRegistry(ctx context.Context) error {
	exists, err := m.podman.ContainerExists(ctx, config.AuthRegistryContainerName)
	if err != nil {
		return fmt.Errorf("checking auth registry container: %w", err)
	}

	if !exists {
		logrus.Info("Auth registry container not found")
		return nil
	}

	logrus.Info("Stopping auth registry container")
	if err := m.podman.ContainerStop(ctx, config.AuthRegistryContainerName); err != nil {
		logrus.Warnf("Failed to stop auth registry: %v", err)
	}

	if err := m.podman.ContainerRemove(ctx, config.AuthRegistryContainerName, true); err != nil {
		if !isContainerGone(err) {
			return fmt.Errorf("removing auth registry container: %w", err)
		}
		logrus.Debug("Auth registry container already removed")
	}

	logrus.Info("Auth registry stopped and removed")
	return nil
}

type AuthRegistryStatus struct {
	Running  bool
	IP       string
	HostPort int
	PullURL  string
	Username string
	Password string
}

func (m *Manager) AuthRegistryInfo(ctx context.Context) (*AuthRegistryStatus, error) {
	info := &AuthRegistryStatus{
		IP:       config.AuthRegistryStaticIP,
		HostPort: config.AuthRegistryPort,
		PullURL:  fmt.Sprintf("%s.%s:%d", config.AuthRegistryHostname, config.ClusterDomain, config.AuthRegistryPort),
		Username: config.AuthRegistryUsername,
		Password: config.AuthRegistryPassword,
	}

	exists, err := m.podman.ContainerExists(ctx, config.AuthRegistryContainerName)
	if err != nil {
		return info, fmt.Errorf("checking auth registry container: %w", err)
	}

	if !exists {
		return info, nil
	}

	status, err := m.podman.ContainerStatus(ctx, config.AuthRegistryContainerName)
	if err != nil {
		return info, fmt.Errorf("checking auth registry status: %w", err)
	}

	info.Running = status == define.ContainerStateRunning.String()
	return info, nil
}

func isContainerGone(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, define.ErrNoSuchCtr.Error()) || strings.Contains(msg, "not found")
}

func isContainerAlreadyExists(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, define.ErrCtrExists.Error()) || strings.Contains(msg, "already in use")
}

func generateHtpasswd(username, password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return fmt.Sprintf("%s:%s", username, string(hash)), nil
}
