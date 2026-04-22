package helpers

import (
	"context"
	"os"
	"strings"

	"github.com/bootc-dev/bink/internal/podman"
	. "github.com/onsi/gomega"
)

// RequireCommand verifies that a command exists on the system
func RequireCommand(cmd string) {
	_, err := exec.LookPath(cmd)
	Expect(err).ToNot(HaveOccurred(), "%s command not found in PATH", cmd)
}

// RequireBink verifies that the bink binary exists in the project root
func RequireBink() {
	// Check from project root (two levels up from test/integration)
	_, err := os.Stat("../../bink")
	Expect(err).ToNot(HaveOccurred(), "bink binary not found. Run 'make build-bink' first")
}

// RequireImage verifies that a container image exists
func RequireImage(image string) {
	podmanClient, err := podman.NewClient()
	Expect(err).ToNot(HaveOccurred(), "Failed to create podman client")

	exists, err := podmanClient.ImageExists(context.Background(), image)
	Expect(err).ToNot(HaveOccurred(), "Failed to check image existence")
	Expect(exists).To(BeTrue(), "Image %s not found. Run 'make build-cluster-image' and 'make build-images-container'", image)
}

// CleanupAllTestClusters removes all test clusters (containers with label bink.cluster-name=test-bink-*)
func CleanupAllTestClusters() {
	ctx := context.Background()
	podmanClient, err := podman.NewClient()
	if err != nil {
		return
	}

	containerNames, err := podmanClient.ContainerList(ctx, "label=bink.cluster-name")
	if err != nil {
		return
	}

	for _, name := range containerNames {
		labelValue, err := podmanClient.ContainerInspect(ctx, name, "{{index .Config.Labels \"bink.cluster-name\"}}")
		if err != nil {
			continue
		}

		if strings.HasPrefix(labelValue, "test-bink-") {
			_ = podmanClient.ContainerRemove(ctx, name, true)
		}
	}

	volumes, err := podmanClient.VolumeList(ctx, "name=test-")
	if err == nil {
		for _, vol := range volumes {
			if strings.HasPrefix(vol, "test-") {
				_ = podmanClient.VolumeRemove(ctx, vol)
			}
		}
	}
}
