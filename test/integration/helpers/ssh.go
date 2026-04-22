package helpers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bootc-dev/bink/internal/podman"
	. "github.com/onsi/gomega"
)

// SSHExec executes a command on a node via SSH through the container
func SSHExec(clusterName, nodeName, command string) string {
	ctx := context.Background()
	podmanClient, err := podman.NewClient()
	Expect(err).ToNot(HaveOccurred(), "Failed to create podman client")

	if clusterName == "" {
		clusterName = "podman"
	}
	containerName := fmt.Sprintf("k8s-%s-%s", clusterName, nodeName)

	sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i /var/run/cluster/cluster.key -p 2222 core@localhost '%s'", command)

	output, err := podmanClient.ContainerExec(ctx, containerName, []string{"sh", "-c", sshCmd})

	lines := strings.Split(output, "\n")
	var filtered []string
	for _, line := range lines {
		if !strings.Contains(line, "Warning:") {
			filtered = append(filtered, line)
		}
	}
	cleanOutput := strings.TrimSpace(strings.Join(filtered, "\n"))

	Expect(err).ToNot(HaveOccurred(), "SSH command failed: %s", cleanOutput)
	return cleanOutput
}

// SSHExecQuiet executes a command but doesn't fail on errors
// Returns output and error
func SSHExecQuiet(clusterName, nodeName, command string) (string, error) {
	ctx := context.Background()
	podmanClient, err := podman.NewClient()
	if err != nil {
		return "", err
	}

	if clusterName == "" {
		clusterName = "podman"
	}
	containerName := fmt.Sprintf("k8s-%s-%s", clusterName, nodeName)

	sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i /var/run/cluster/cluster.key -p 2222 core@localhost '%s'", command)

	output, err := podmanClient.ContainerExec(ctx, containerName, []string{"sh", "-c", sshCmd})

	lines := strings.Split(output, "\n")
	var filtered []string
	for _, line := range lines {
		if !strings.Contains(line, "Warning:") {
			filtered = append(filtered, line)
		}
	}
	cleanOutput := strings.TrimSpace(strings.Join(filtered, "\n"))

	return cleanOutput, err
}
