package ssh

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bootc-dev/bink/internal/podman"
	"github.com/sirupsen/logrus"
)

// Client provides SSH operations for connecting to VMs via containers
type Client struct {
	containerName string
	host          string
	port          string
	keyPath       string
	user          string
	logger        *logrus.Logger
	podmanClient  *podman.Client
}

// Config holds SSH client configuration
type Config struct {
	ContainerName string         // Podman container name
	Host          string         // SSH host (usually "localhost" for port-forwarded VMs)
	Port          string         // SSH port (usually "2222" for port-forwarded VMs)
	KeyPath       string         // Path to SSH private key
	User          string         // SSH user (usually "core")
	Logger        *logrus.Logger
	PodmanClient  *podman.Client // Podman client for container operations
}

// NewClient creates a new SSH client
func NewClient(cfg Config) *Client {
	if cfg.Logger == nil {
		cfg.Logger = logrus.New()
	}
	return &Client{
		containerName: cfg.ContainerName,
		host:          cfg.Host,
		port:          cfg.Port,
		keyPath:       cfg.KeyPath,
		user:          cfg.User,
		logger:        cfg.Logger,
		podmanClient:  cfg.PodmanClient,
	}
}

// Exec executes a command via SSH and returns stdout
func (c *Client) Exec(ctx context.Context, command string) (string, error) {
	sshArgs := c.buildSSHArgs(command)
	execCmd := append([]string{"ssh"}, sshArgs...)

	c.logger.Debugf("Running: podman exec %s %s", c.containerName, strings.Join(execCmd, " "))

	output, err := c.podmanClient.ContainerExec(ctx, c.containerName, execCmd)
	if err != nil {
		return "", fmt.Errorf("ssh exec failed: %w", err)
	}

	return output, nil
}

// ExecWithOutput executes a command via SSH, streaming output to stdout/stderr
func (c *Client) ExecWithOutput(ctx context.Context, command string) error {
	sshArgs := c.buildSSHArgs(command)
	execCmd := append([]string{"ssh"}, sshArgs...)

	c.logger.Debugf("Running: podman exec %s %s", c.containerName, strings.Join(execCmd, " "))

	output, err := c.podmanClient.ContainerExec(ctx, c.containerName, execCmd)
	if err != nil {
		return fmt.Errorf("ssh exec failed: %w", err)
	}

	// Write output to stdout (ContainerExec buffers output, so we write it here)
	fmt.Fprint(os.Stdout, output)

	return nil
}

// Interactive starts an interactive SSH session
func (c *Client) Interactive(ctx context.Context) error {
	sshArgs := c.buildSSHArgs("")
	execCmd := append([]string{"ssh"}, sshArgs...)

	c.logger.Infof("Connecting to %s (SSH: %s:%s, cluster IP) as user %s",
		c.containerName, c.host, c.port, c.user)

	if err := c.podmanClient.ContainerExecInteractive(ctx, c.containerName, execCmd); err != nil {
		return fmt.Errorf("interactive ssh failed: %w", err)
	}

	return nil
}

// CopyTo copies a file to the remote host via SCP
func (c *Client) CopyTo(ctx context.Context, localPath, remotePath string) error {
	scpArgs := []string{
		"scp",
		"-P", c.port,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-i", c.keyPath,
		localPath,
		fmt.Sprintf("%s@%s:%s", c.user, c.host, remotePath),
	}

	c.logger.Debugf("Running: podman exec %s %s", c.containerName, strings.Join(scpArgs, " "))

	_, err := c.podmanClient.ContainerExec(ctx, c.containerName, scpArgs)
	if err != nil {
		return fmt.Errorf("scp failed: %w", err)
	}

	return nil
}

// WaitForSSH waits for SSH to become available using exponential backoff
func (c *Client) WaitForSSH(ctx context.Context, maxRetries int) error {
	c.logger.Infof("Waiting for SSH to be ready on %s...", c.host)

	// Exponential backoff: 1s, 2s, 4s, 8s, 10s (capped)
	backoff := 1 * time.Second
	const maxBackoff = 10 * time.Second

	for i := 1; i <= maxRetries; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		sshArgs := c.buildSSHArgs("true")
		sshArgs = append(sshArgs, "-o", "ConnectTimeout=2")
		execCmd := append([]string{"ssh"}, sshArgs...)

		c.logger.Debugf("SSH check attempt %d/%d (backoff: %v)", i, maxRetries, backoff)

		if err := c.podmanClient.ContainerExecQuiet(ctx, c.containerName, execCmd); err == nil {
			c.logger.Info("✓ SSH is ready")
			return nil
		}

		if i == maxRetries {
			return fmt.Errorf("timeout waiting for SSH to be ready after %d attempts", maxRetries)
		}

		// Exponential backoff with cap
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	return nil
}

// buildSSHArgs constructs the SSH command arguments
func (c *Client) buildSSHArgs(command string) []string {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-i", c.keyPath,
		"-p", c.port,
		fmt.Sprintf("%s@%s", c.user, c.host),
	}

	if command != "" {
		args = append(args, command)
	}

	return args
}
