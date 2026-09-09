// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/sirupsen/logrus"
	nettypes "go.podman.io/common/libnetwork/types"
	"go.podman.io/podman/v6/libpod/define"
	"go.podman.io/podman/v6/pkg/errorhandling"
	"go.podman.io/podman/v6/pkg/specgen"
	"golang.org/x/crypto/bcrypt"
)

const authHtpasswdEnv = "BINK_AUTH_HTPASSWD"

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

	ensure := func(ctx context.Context) error {
		return m.ensureExistingContainer(ctx, config.RegistryContainerName, "Registry")
	}

	if exists {
		return ensure(ctx)
	}

	if err := m.createContainer(ctx); err != nil {
		return m.recoverFromConcurrentCreate(ctx, config.RegistryContainerName, err, ensure)
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
		return fmt.Errorf("creating registry container: %w", err)
	}
	return nil
}

// ensureExistingContainer starts a registry container that already exists, using label
// for user-facing log and error messages.
func (m *Manager) ensureExistingContainer(ctx context.Context, name, label string) error {
	status, err := m.podman.ContainerStatus(ctx, name)
	if err != nil {
		return fmt.Errorf("checking %s status: %w", strings.ToLower(label), err)
	}
	if status == define.ContainerStateRunning.String() {
		logrus.Infof("%s already running", label)
		return nil
	}

	logrus.Infof("%s container is %s, starting it", label, status)
	if err := m.podman.ContainerStart(ctx, name); err != nil {
		return fmt.Errorf("starting %s: %w", strings.ToLower(label), err)
	}
	logrus.Infof("%s started", label)
	return nil
}

func (m *Manager) StopRegistry(ctx context.Context) error {
	exists, err := m.podman.ContainerExists(ctx, config.RegistryContainerName)
	if err != nil {
		return fmt.Errorf("checking registry container: %w", err)
	}

	if exists {
		logrus.Info("Stopping registry container")
		if err := m.podman.ContainerStop(ctx, config.RegistryContainerName); err != nil && !isPodmanNotFound(err) {
			logrus.Warnf("Failed to stop registry: %v", err)
		}

		if err := m.podman.ContainerRemove(ctx, config.RegistryContainerName, true); err != nil && !isPodmanNotFound(err) {
			return fmt.Errorf("removing registry container: %w", err)
		}
	} else {
		logrus.Info("Registry container not found")
	}

	volumeExists, err := m.podman.VolumeExists(ctx, config.RegistryVolume)
	if err != nil {
		return fmt.Errorf("checking registry volume: %w", err)
	}
	if volumeExists {
		if err := m.podman.VolumeRemove(ctx, config.RegistryVolume); err != nil && !isPodmanNotFound(err) {
			return fmt.Errorf("removing registry volume: %w", err)
		}
	} else {
		logrus.Info("Registry volume not found")
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

// EnsureAuthRegistry starts (or creates) the authenticated registry. Credentials are not
// stored anywhere inspectable, so they cannot be compared against an already-running
// container: to change them, stop the registry and start it again.
func (m *Manager) EnsureAuthRegistry(ctx context.Context, username, password string) error {
	logrus.Info("Ensuring authenticated registry is running")
	if err := ValidateAuthCredentials(username, password); err != nil {
		return err
	}

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

	ensure := func(ctx context.Context) error {
		return m.ensureExistingContainer(ctx, config.AuthRegistryContainerName, "Authenticated registry")
	}

	if exists {
		return ensure(ctx)
	}

	if err := m.createAuthContainer(ctx, username, password); err != nil {
		return m.recoverFromConcurrentCreate(ctx, config.AuthRegistryContainerName, err, ensure)
	}

	logrus.Infof("Authenticated registry running at %s:%d (host: localhost:%d)",
		config.AuthRegistryStaticIP, config.AuthRegistryPort, config.AuthRegistryPort)
	return nil
}

func (m *Manager) createAuthContainer(ctx context.Context, username, password string) error {
	htpasswdEntry, err := generateHtpasswd(username, password)
	if err != nil {
		return fmt.Errorf("generating htpasswd: %w", err)
	}

	opts := &podman.ContainerCreateOptions{
		Name:  config.AuthRegistryContainerName,
		Image: config.RegistryImage,
		Entrypoint: []string{"/bin/sh", "-c",
			`mkdir -p /auth && printf '%s\n' "$` + authHtpasswdEnv + `" > /auth/htpasswd && exec /entrypoint.sh /etc/docker/registry/config.yml`,
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
			authHtpasswdEnv:                htpasswdEntry,
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
	if err := m.podman.ContainerStop(ctx, config.AuthRegistryContainerName); err != nil && !isPodmanNotFound(err) {
		logrus.Warnf("Failed to stop auth registry: %v", err)
	}

	if err := m.podman.ContainerRemove(ctx, config.AuthRegistryContainerName, true); err != nil && !isPodmanNotFound(err) {
		return fmt.Errorf("removing auth registry container: %w", err)
	}

	logrus.Info("Auth registry stopped and removed")
	return nil
}

type AuthRegistryStatus struct {
	Running  bool
	IP       string
	HostPort int
	PullURL  string
}

func (m *Manager) AuthRegistryInfo(ctx context.Context) (*AuthRegistryStatus, error) {
	info := &AuthRegistryStatus{
		IP:       config.AuthRegistryStaticIP,
		HostPort: config.AuthRegistryPort,
		PullURL:  fmt.Sprintf("%s.%s:%d", config.AuthRegistryHostname, config.ClusterDomain, config.AuthRegistryPort),
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

func isPodmanNotFound(err error) bool {
	var podmanErr *errorhandling.ErrorModel
	return errors.As(err, &podmanErr) && podmanErr.ResponseCode == http.StatusNotFound
}

// recoverFromConcurrentCreate handles parallel EnsureRegistry/EnsureAuthRegistry calls
// where two processes both attempt to create the same named container.
func (m *Manager) recoverFromConcurrentCreate(ctx context.Context, name string, createErr error, ensure func(context.Context) error) error {
	if isContainerAlreadyExists(createErr) {
		logrus.Infof("%s was created concurrently", name)
		return ensure(ctx)
	}

	exists, checkErr := m.podman.ContainerExists(ctx, name)
	if checkErr != nil {
		return errors.Join(createErr, fmt.Errorf("checking %s after create failure: %w", name, checkErr))
	}
	if exists {
		logrus.Infof("%s was created concurrently", name)
		return ensure(ctx)
	}
	return createErr
}

func isContainerAlreadyExists(err error) bool {
	if errors.Is(err, define.ErrCtrExists) {
		return true
	}
	var podmanErr *errorhandling.ErrorModel
	if errors.As(err, &podmanErr) && podmanErr.ResponseCode == http.StatusConflict {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "already in use") || strings.Contains(msg, define.ErrCtrExists.Error())
}

// ValidateAuthCredentials checks that credentials can be represented safely in an htpasswd file.
func ValidateAuthCredentials(username, password string) error {
	if username == "" {
		return fmt.Errorf("registry username must not be empty")
	}
	if strings.ContainsAny(username, ":\r\n") {
		return fmt.Errorf("registry username must not contain ':', carriage returns, or newlines")
	}
	if password == "" {
		return fmt.Errorf("registry password must not be empty")
	}
	return nil
}

// AuthRegistryRequested reports whether credentials request an authenticated registry.
// Supplying only one credential is rejected rather than silently disabling authentication.
func AuthRegistryRequested(username, password string) (bool, error) {
	if username == "" && password == "" {
		return false, nil
	}
	if err := ValidateAuthCredentials(username, password); err != nil {
		return false, err
	}
	return true, nil
}

func generateHtpasswd(username, password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return fmt.Sprintf("%s:%s", username, string(hash)), nil
}
