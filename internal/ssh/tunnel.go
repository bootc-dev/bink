package ssh

import (
	"context"
	"fmt"
	"strings"

	"github.com/bootc-dev/bink/internal/podman"
	"github.com/sirupsen/logrus"
)

type TunnelConfig struct {
	ContainerName string
	Host          string
	Port          string
	KeyPath       string
	User          string
	LocalPort     string
	RemotePort    string
	BindAddress   string
	Logger        *logrus.Logger
	PodmanClient  *podman.Client
}

func StartTunnel(ctx context.Context, cfg TunnelConfig) error {
	if cfg.Logger == nil {
		cfg.Logger = logrus.New()
	}

	if cfg.PodmanClient == nil {
		var err error
		cfg.PodmanClient, err = podman.NewClient()
		if err != nil {
			return fmt.Errorf("creating podman client: %w", err)
		}
	}

	bindAddr := cfg.BindAddress
	if bindAddr == "" {
		bindAddr = "0.0.0.0"
	}

	sshArgs := []string{
		"ssh",
		"-N",
		"-L", fmt.Sprintf("%s:%s:localhost:%s", bindAddr, cfg.LocalPort, cfg.RemotePort),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ServerAliveInterval=60",
		"-i", cfg.KeyPath,
		"-p", cfg.Port,
		fmt.Sprintf("%s@%s", cfg.User, cfg.Host),
	}

	cfg.Logger.Debugf("Starting SSH tunnel: podman exec %s %s", cfg.ContainerName, strings.Join(sshArgs, " "))
	cfg.Logger.Infof("Starting SSH port forwarding: %s:%s -> VM:%s", bindAddr, cfg.LocalPort, cfg.RemotePort)

	go func() {
		if err := cfg.PodmanClient.ContainerExecQuiet(context.Background(), cfg.ContainerName, sshArgs); err != nil {
			cfg.Logger.Warnf("SSH tunnel exited: %v", err)
		}
	}()

	return nil
}

func IsTunnelActive(ctx context.Context, containerName, port string) (bool, error) {
	podmanClient, err := podman.NewClient()
	if err != nil {
		return false, fmt.Errorf("creating podman client: %w", err)
	}

	output, err := podmanClient.ContainerExec(ctx, containerName, []string{"ss", "-tln"})
	if err != nil {
		return false, fmt.Errorf("checking tunnel status: %w", err)
	}

	return strings.Contains(output, fmt.Sprintf(":%s", port)), nil
}
