package helpers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bootc-dev/bink/internal/podman"
	. "github.com/onsi/gomega"
)

// ContainerInfo holds basic container information
type ContainerInfo struct {
	ID     string
	Name   string
	State  string
	Ports  []string
	Labels map[string]string
}

// PodmanCmd executes a podman command and returns output
// Note: This is deprecated - prefer using podman.Client directly in tests
func PodmanCmd(args ...string) string {
	ctx := context.Background()
	podmanClient, err := podman.NewClient()
	Expect(err).ToNot(HaveOccurred(), "Failed to create podman client")

	output, err := podmanClient.ContainerExec(ctx, args[0], args[1:])
	Expect(err).ToNot(HaveOccurred(), "podman command failed")
	return output
}

// PodmanExec executes a command inside a container
func PodmanExec(container, command string) string {
	ctx := context.Background()
	podmanClient, err := podman.NewClient()
	Expect(err).ToNot(HaveOccurred(), "Failed to create podman client")

	output, err := podmanClient.ContainerExec(ctx, container, []string{"sh", "-c", command})
	Expect(err).ToNot(HaveOccurred(), "podman exec failed")
	return output
}

// GetContainer returns information about a container
// Returns nil if container doesn't exist
// For test usage, name should be the full container name (e.g., "k8s-test-bink-abc123-node1")
func GetContainer(name string) *ContainerInfo {
	ctx := context.Background()
	podmanClient, err := podman.NewClient()
	if err != nil {
		return nil
	}

	exists, err := podmanClient.ContainerExists(ctx, name)
	if err != nil || !exists {
		return nil
	}

	idStr, err := podmanClient.ContainerInspect(ctx, name, "{{.ID}}")
	if err != nil {
		return nil
	}

	stateStr, err := podmanClient.ContainerInspect(ctx, name, "{{.State.Status}}")
	if err != nil {
		return nil
	}

	ipStr, err := podmanClient.ContainerInspect(ctx, name, "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}")
	if err != nil {
		ipStr = ""
	}

	info := &ContainerInfo{
		ID:     strings.TrimSpace(idStr),
		Name:   name,
		State:  strings.TrimSpace(stateStr),
		Labels: make(map[string]string),
	}

	_ = ipStr

	return info
}

// GetContainerID returns the ID of a container by name
func GetContainerID(name string) string {
	ctx := context.Background()
	podmanClient, err := podman.NewClient()
	if err != nil {
		return ""
	}

	output, err := podmanClient.ContainerInspect(ctx, name, "{{.ID}}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// ContainerExists checks if a container exists
func ContainerExists(name string) bool {
	return GetContainer(name) != nil
}

// GetVolume checks if a volume exists
func GetVolume(name string) bool {
	ctx := context.Background()
	podmanClient, err := podman.NewClient()
	if err != nil {
		return false
	}

	exists, err := podmanClient.VolumeExists(ctx, name)
	if err != nil {
		return false
	}
	return exists
}
