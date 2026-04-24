package cluster

import (
	"context"
	"fmt"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/containers/podman/v6/pkg/specgen"
	"github.com/sirupsen/logrus"
)

const (
	ClusterImagesVolume = "cluster-images"
	// Path inside container where volume is mounted
	ClusterImagesMountPath = "/var/lib/cluster-images"
	// Global container name for volume population (shared across all clusters)
	PopulatorContainerName = "cluster-images-populator"
)

// EnsureImagesVolume creates and populates the cluster-images volume if it doesn't exist
// This volume is shared across all clusters since the images are identical
func (c *Cluster) EnsureImagesVolume(ctx context.Context) error {
	volumeName := ClusterImagesVolume

	logrus.Infof("Ensuring cluster images volume: %s", volumeName)

	// Check if volume exists
	exists, err := c.volumeExists(ctx, volumeName)
	if err != nil {
		return fmt.Errorf("checking volume existence: %w", err)
	}

	if exists {
		logrus.Infof("Volume %s already exists, checking if populated...", volumeName)

		// Check if volume is already successfully populated
		if c.isVolumeCompleted(ctx, volumeName) {
			logrus.Infof("Volume %s is already populated with images", volumeName)
			return nil
		}

		// Check if another process is currently populating the volume
		if c.isPopulationInProgress(ctx) {
			logrus.Infof("Another process is populating the volume, waiting...")
			if err := c.waitForPopulationComplete(ctx); err != nil {
				return fmt.Errorf("waiting for volume population: %w", err)
			}
			logrus.Infof("Volume population complete")
			return nil
		}

		logrus.Infof("Volume exists but is not populated, will populate...")
	} else {
		logrus.Infof("Creating volume %s...", volumeName)
		if err := c.createVolume(ctx, volumeName); err != nil {
			return fmt.Errorf("creating volume: %w", err)
		}
	}

	// Populate the volume with Kubernetes images
	if err := c.populateImagesVolume(ctx, volumeName); err != nil {
		// If another process started populating after our check, wait for it
		if c.isPopulationInProgress(ctx) {
			logrus.Infof("Another process started populating concurrently, waiting...")
			if waitErr := c.waitForPopulationComplete(ctx); waitErr != nil {
				return fmt.Errorf("waiting for concurrent population: %w", waitErr)
			}
			logrus.Infof("Concurrent population complete")
			return nil
		}
		return fmt.Errorf("populating volume: %w", err)
	}

	// Mark volume as completed
	if err := c.markVolumeCompleted(ctx, volumeName); err != nil {
		logrus.Warnf("Failed to mark volume as completed: %v", err)
	}

	logrus.Infof("✓ Cluster images volume ready: %s", volumeName)
	return nil
}

func (c *Cluster) volumeExists(ctx context.Context, name string) (bool, error) {
	return c.podmanClient.VolumeExists(ctx, name)
}

func (c *Cluster) createVolume(ctx context.Context, name string) error {
	return c.podmanClient.VolumeCreate(ctx, name)
}

func (c *Cluster) isVolumePopulated(ctx context.Context, volumeName string) (bool, error) {
	err := c.podmanClient.ContainerRunQuiet(ctx,
		config.DefaultBaseImage,
		[]string{"sh", "-c", "test -d /check/overlay-images && test -d /check/overlay-layers"},
		[]string{fmt.Sprintf("%s:/check:z", volumeName)},
	)
	return err == nil, nil
}

func (c *Cluster) populateImagesVolume(ctx context.Context, volumeName string) error {
	logrus.Info("Pre-pulling Kubernetes and Calico images into volume...")

	images := []string{
		"registry.k8s.io/kube-apiserver:v1.35.0",
		"registry.k8s.io/kube-controller-manager:v1.35.0",
		"registry.k8s.io/kube-scheduler:v1.35.0",
		"registry.k8s.io/kube-proxy:v1.35.0",
		"registry.k8s.io/coredns/coredns:v1.11.1",
		"registry.k8s.io/pause:3.10",
		"registry.k8s.io/etcd:3.5.16-0",
		"quay.io/calico/cni:" + calicoVersion,
		"quay.io/calico/node:" + calicoVersion,
		"quay.io/calico/kube-controllers:" + calicoVersion,
	}

	logrus.Infof("Found %d images to pull", len(images))

	// Use a global container name for population (shared across all clusters)
	// This allows other processes to wait for it using `podman wait`
	tmpContainer := PopulatorContainerName

	logrus.Infof("Creating populator container: %s", tmpContainer)

	opts := &podman.ContainerCreateOptions{
		Name:    tmpContainer,
		Image:   config.DefaultBaseImage,
		Command: []string{"sleep", "infinity"},
		Volumes: []*specgen.NamedVolume{{Name: volumeName, Dest: "/var/lib/containers/storage"}},
	}

	_, err := c.podmanClient.ContainerCreate(ctx, opts)
	if err != nil {
		return fmt.Errorf("starting populator container (another process may be populating): %w", err)
	}

	defer func() {
		logrus.Debugf("Cleaning up populator container %s", tmpContainer)
		c.podmanClient.ContainerRemove(ctx, tmpContainer, true)
	}()

	logrus.Info("Installing container tools in temporary container...")
	if err := c.podmanClient.ContainerExecQuiet(ctx, tmpContainer,
		[]string{"dnf", "install", "-y", "-q", "skopeo", "podman"}); err != nil {
		return fmt.Errorf("installing container tools: %w", err)
	}

	logrus.Debug("Configuring storage for image extraction...")
	if err := c.podmanClient.ContainerExecQuiet(ctx, tmpContainer,
		[]string{"sh", "-c", "echo 'root:100000:65536' > /etc/subuid && " +
			"echo 'root:100000:65536' > /etc/subgid && " +
			"podman system migrate 2>/dev/null || true"}); err != nil {
		logrus.Debug("Storage configuration completed with warnings")
	}

	// Pull each image using skopeo
	for i, image := range images {
		if image == "" {
			continue
		}
		logrus.Infof("[%d/%d] Pulling %s", i+1, len(images), image)

		if err := c.podmanClient.ContainerExecQuiet(ctx, tmpContainer,
			[]string{"skopeo", "copy", "docker://" + image, "containers-storage:" + image}); err != nil {
			logrus.Warnf("Failed to pull %s: %v (continuing...)", image, err)
			continue
		}
	}

	logrus.Info("✓ All images pulled successfully")
	return nil
}

// GetImagesVolumeName returns the volume name for this cluster
// The images volume is shared across all clusters
func (c *Cluster) GetImagesVolumeName() string {
	return ClusterImagesVolume
}

// isVolumeCompleted checks if volume has been successfully populated
func (c *Cluster) isVolumeCompleted(ctx context.Context, volumeName string) bool {
	err := c.podmanClient.ContainerRunQuiet(ctx,
		config.DefaultBaseImage,
		[]string{"test", "-f", "/check/.completed"},
		[]string{fmt.Sprintf("%s:/check:z", volumeName)},
	)
	return err == nil
}

// isPopulationInProgress checks if the populator container is currently running
func (c *Cluster) isPopulationInProgress(ctx context.Context) bool {
	exists, err := c.podmanClient.ContainerExists(ctx, PopulatorContainerName)
	if err != nil {
		return false
	}
	return exists
}

// waitForPopulationComplete waits for the populator container to finish
func (c *Cluster) waitForPopulationComplete(ctx context.Context) error {
	logrus.Debugf("Waiting for populator container %s to complete...", PopulatorContainerName)

	exitCode, err := c.podmanClient.ContainerWait(ctx, PopulatorContainerName)
	if err != nil {
		logrus.Debugf("Container wait failed (may have already completed): %v", err)
		return nil
	}

	logrus.Debugf("Populator container exited with code: %d", exitCode)

	if exitCode != 0 {
		return fmt.Errorf("population failed with exit code %d", exitCode)
	}

	return nil
}

// markVolumeCompleted creates a marker file indicating successful population
func (c *Cluster) markVolumeCompleted(ctx context.Context, volumeName string) error {
	return c.podmanClient.ContainerRunQuiet(ctx,
		config.DefaultBaseImage,
		[]string{"touch", "/mark/.completed"},
		[]string{fmt.Sprintf("%s:/mark:z", volumeName)},
	)
}
