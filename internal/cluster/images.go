package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/containers/podman/v5/pkg/specgen"
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

func (c *Cluster) populateImagesVolume(ctx context.Context, volumeName string) error {
	logrus.Info("Pre-pulling Kubernetes and Calico images into volume...")

	tmpContainer := PopulatorContainerName

	logrus.Infof("Creating populator container: %s", tmpContainer)

	opts := &podman.ContainerCreateOptions{
		Name:    tmpContainer,
		Image:   config.DefaultPopulatorImage,
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

	// Query kubeadm for the exact images it needs
	logrus.Info("Querying kubeadm for required images...")
	output, err := c.podmanClient.ContainerExec(ctx, tmpContainer,
		[]string{"kubeadm", "config", "images", "list", "--kubernetes-version", config.KubernetesVersion})
	if err != nil {
		return fmt.Errorf("querying kubeadm for images: %w", err)
	}

	var images []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			images = append(images, line)
		}
	}

	// Add Calico CNI images
	images = append(images,
		config.CalicoImageBase+"/cni:"+config.CalicoVersion,
		config.CalicoImageBase+"/node:"+config.CalicoVersion,
		config.CalicoImageBase+"/kube-controllers:"+config.CalicoVersion,
	)

	logrus.Infof("Found %d images to pull", len(images))

	// Pull each image using skopeo with a per-image timeout
	pullTimeout := time.Duration(config.DefaultImagePullTimeout) * time.Second
	maxRetries := 2
	for i, image := range images {
		var lastErr error
		for attempt := range maxRetries {
			if attempt > 0 {
				logrus.Infof("[%d/%d] Retrying %s (attempt %d/%d)", i+1, len(images), image, attempt+1, maxRetries)
			} else {
				logrus.Infof("[%d/%d] Pulling %s", i+1, len(images), image)
			}

			pullCtx, cancel := context.WithTimeout(ctx, pullTimeout)
			lastErr = c.podmanClient.ContainerExecQuiet(pullCtx, tmpContainer,
				[]string{"skopeo", "copy", "docker://" + image, "containers-storage:" + image})
			cancel()

			if lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			logrus.Warnf("Failed to pull %s: %v (continuing...)", image, lastErr)
		}
	}

	logrus.Info("✓ All images pulled successfully")

	// Make volume contents world-readable so virtiofsd (running as qemu user) can serve them
	logrus.Info("Setting volume permissions for virtiofsd access...")
	if err := c.podmanClient.ContainerExecQuiet(ctx, tmpContainer,
		[]string{"chmod", "-R", "a+rX", "/var/lib/containers/storage"}); err != nil {
		return fmt.Errorf("setting volume permissions: %w", err)
	}

	return nil
}

// isVolumeCompleted checks if volume has been successfully populated
func (c *Cluster) isVolumeCompleted(ctx context.Context, volumeName string) bool {
	err := c.podmanClient.ContainerRunQuiet(ctx,
		config.DefaultPopulatorImage,
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
		config.DefaultPopulatorImage,
		[]string{"touch", "/mark/.completed"},
		[]string{fmt.Sprintf("%s:/mark:z", volumeName)},
	)
}
