// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
)

var (
	ErrImageInspect          = errors.New("inspecting node image")
	ErrKubeadmVersionUnknown = errors.New("cannot determine kubeadm version")
)

const (
	ClusterImagesVolumePrefix    = "cluster-images"
	ClusterImagesMountPath       = "/var/lib/cluster-images"
	populatorContainerNamePrefix = "cluster-images-populator"

	kubeadmVersionLabelKey = "bink.kubeadm-version"
	nodeImageLabelKey      = "bink.node-image"
)

// PopulatorContainerName returns the populator container name scoped to a kubeadm version.
func PopulatorContainerName(kubeadmVersion string) string {
	return populatorContainerNamePrefix + "-" + kubeadmVersion
}

// ClusterImagesVolumeName returns the versioned volume name for a given kubeadm version.
func ClusterImagesVolumeName(kubeadmVersion string) string {
	return ClusterImagesVolumePrefix + "-" + kubeadmVersion
}

// GetKubeadmVersionFromImage inspects the node image and returns the kubeadm version from its label.
func GetKubeadmVersionFromImage(ctx context.Context, podmanClient PodmanClient, nodeImage string) (string, error) {
	labels, err := podmanClient.ImageInspectLabels(ctx, nodeImage)
	switch {
	case err == nil:
		if v := labels[kubeadmVersionLabelKey]; v != "" {
			return v, nil
		}
	case isImageNotFound(err):
		logrus.Debugf("Image %s not available locally, falling back to tag parsing", nodeImage)
	default:
		return "", fmt.Errorf("%w %s: %w", ErrImageInspect, nodeImage, err)
	}

	if version := extractVersionFromTag(nodeImage); version != "" {
		return version, nil
	}

	return "", fmt.Errorf("%w from node image %s: label %q not found and version cannot be inferred from the tag", ErrKubeadmVersionUnknown, nodeImage, kubeadmVersionLabelKey)
}

func isImageNotFound(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "image not known") || strings.Contains(msg, "no such image")
}

// extractVersionFromTag attempts to parse a kubeadm version from a node image tag.
// Expected format: "registry/repo:v<version>-<rest>" e.g. "ghcr.io/.../node:v1.35-fedora-43" -> "1.35"
func extractVersionFromTag(imageRef string) string {
	ref := imageRef
	if idx := strings.Index(ref, "@"); idx >= 0 {
		ref = ref[:idx]
	}
	lastSlash := strings.LastIndex(ref, "/")
	tagSep := strings.LastIndex(ref, ":")
	if tagSep < 0 || tagSep < lastSlash {
		return ""
	}
	tag := ref[tagSep+1:]
	tag = strings.TrimPrefix(tag, "v")
	if idx := strings.Index(tag, "-"); idx > 0 {
		tag = tag[:idx]
	}
	if tag == "" || tag == "latest" {
		return ""
	}
	return tag
}

// EnsureImagesVolume creates and populates a versioned cluster-images volume.
// It inspects the node image for its kubeadm version and creates a volume named
// cluster-images-<version>. Returns the volume name.
func (c *Cluster) EnsureImagesVolume(ctx context.Context, nodeImage string) (string, error) {
	logrus.Infof("Ensuring cluster images volume for node image: %s", nodeImage)

	if err := c.podmanClient.EnsureImage(ctx, nodeImage); err != nil {
		return "", fmt.Errorf("ensuring node image: %w", err)
	}

	kubeadmVersion, err := GetKubeadmVersionFromImage(ctx, c.podmanClient, nodeImage)
	if err != nil {
		return "", err
	}

	volumeName := ClusterImagesVolumeName(kubeadmVersion)
	logrus.Infof("Kubeadm version: %s, volume: %s", kubeadmVersion, volumeName)

	exists, err := c.volumeExists(ctx, volumeName)
	if err != nil {
		return "", fmt.Errorf("checking volume existence: %w", err)
	}

	populatorName := PopulatorContainerName(kubeadmVersion)

	if exists {
		logrus.Infof("Volume %s already exists, checking if populated...", volumeName)

		if c.isVolumeCompleted(ctx, volumeName) {
			logrus.Infof("Volume %s is already populated with images", volumeName)
			return volumeName, nil
		}

		if c.isPopulationInProgress(ctx, populatorName) {
			logrus.Infof("Another process is populating the volume, waiting...")
			if err := c.waitForPopulationComplete(ctx, populatorName, volumeName); err != nil {
				return "", fmt.Errorf("waiting for volume population: %w", err)
			}
			logrus.Infof("Volume population complete")
			return volumeName, nil
		}

		logrus.Infof("Volume exists but is not populated, will populate...")
	} else {
		logrus.Infof("Creating volume %s...", volumeName)
		if err := c.createVolume(ctx, volumeName, nodeImage, kubeadmVersion); err != nil {
			return "", fmt.Errorf("creating volume: %w", err)
		}
	}

	if err := c.populateImagesVolume(ctx, volumeName, nodeImage, populatorName); err != nil {
		if c.isPopulationInProgress(ctx, populatorName) {
			logrus.Infof("Another process started populating concurrently, waiting...")
			if waitErr := c.waitForPopulationComplete(ctx, populatorName, volumeName); waitErr != nil {
				return "", fmt.Errorf("waiting for concurrent population: %w", waitErr)
			}
			if c.isVolumeCompleted(ctx, volumeName) {
				logrus.Infof("Concurrent population complete")
				return volumeName, nil
			}
		}
		return "", fmt.Errorf("populating volume: %w", err)
	}

	if err := c.markVolumeCompleted(ctx, volumeName); err != nil {
		logrus.Warnf("Failed to mark volume as completed: %v", err)
	}

	logrus.Infof("Cluster images volume ready: %s", volumeName)
	return volumeName, nil
}

func (c *Cluster) volumeExists(ctx context.Context, name string) (bool, error) {
	return c.podmanClient.VolumeExists(ctx, name)
}

func (c *Cluster) createVolume(ctx context.Context, name, nodeImage, kubeadmVersion string) error {
	labels := map[string]string{
		nodeImageLabelKey:      nodeImage,
		kubeadmVersionLabelKey: kubeadmVersion,
	}
	return c.podmanClient.VolumeCreate(ctx, name, labels)
}

func (c *Cluster) populateImagesVolume(ctx context.Context, volumeName, nodeImage, populatorName string) error {
	logrus.Info("Pre-pulling Kubernetes and Calico images into volume...")

	tmpContainer := populatorName

	logrus.Infof("Creating populator container: %s", tmpContainer)

	opts := &podman.ContainerCreateOptions{
		Name:       tmpContainer,
		Image:      config.DefaultClusterImage,
		Entrypoint: []string{"/bin/sh"},
		Command:    []string{"-c", "sleep infinity"},
		ImageVolumes: []*specgen.ImageVolume{{
			Source:      nodeImage,
			Destination: "/images",
		}},
		Volumes: []*specgen.NamedVolume{{Name: volumeName, Dest: "/var/lib/containers/storage"}},
		CapAdd:      []string{"SYS_ADMIN"},
		Devices:     []specs.LinuxDevice{{Path: "/dev/fuse"}},
		SelinuxOpts: []string{"disable"},
	}

	if c.hostNetworkPopulator {
		opts.Network = "host"
	}

	_, err := c.podmanClient.ContainerCreate(ctx, opts)
	if err != nil {
		return fmt.Errorf("starting populator container (another process may be populating): %w", err)
	}

	defer func() {
		logrus.Debugf("Cleaning up populator container %s", tmpContainer)
		c.podmanClient.ContainerRemove(ctx, tmpContainer, true)
	}()

	logrus.Info("Reading required images from node image...")
	output, err := c.podmanClient.ContainerExec(ctx, tmpContainer,
		[]string{"cat", "/images/images.txt"})
	if err != nil {
		return fmt.Errorf("reading images.txt from node image: %w", err)
	}

	var images []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			images = append(images, line)
		}
	}

	images = append(images,
		config.CalicoImageBase+"/cni:"+config.CalicoVersion,
		config.CalicoImageBase+"/node:"+config.CalicoVersion,
		config.CalicoImageBase+"/kube-controllers:"+config.CalicoVersion,
	)

	logrus.Infof("Found %d images to pull", len(images))

	pullTimeout := time.Duration(config.DefaultImagePullTimeout) * time.Second
	maxRetries := 2
	failCount := 0
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
				[]string{"podman", "pull", "--quiet", image})
			cancel()

			if lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			failCount++
			logrus.Warnf("Failed to pull %s: %v (continuing...)", image, lastErr)
		}
	}

	if failCount > 0 {
		logrus.Warnf("Image pulling complete with %d/%d failures", failCount, len(images))
	} else {
		logrus.Info("All images pulled successfully")
	}

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
		config.DefaultClusterImage,
		[]string{"test", "-f", "/check/.completed"},
		[]string{fmt.Sprintf("%s:/check:z", volumeName)},
	)
	return err == nil
}

// isPopulationInProgress checks if the populator container is currently running
func (c *Cluster) isPopulationInProgress(ctx context.Context, populatorName string) bool {
	exists, err := c.podmanClient.ContainerExists(ctx, populatorName)
	if err != nil {
		return false
	}
	return exists
}

// waitForPopulationComplete waits for the populator container to finish
func (c *Cluster) waitForPopulationComplete(ctx context.Context, populatorName, volumeName string) error {
	logrus.Debugf("Waiting for populator container %s to complete...", populatorName)

	_, err := c.podmanClient.ContainerWait(ctx, populatorName)
	if err != nil {
		logrus.Debugf("Container wait failed (may have already completed): %v", err)
	}

	// The populator runs "sleep infinity" and gets force-removed when done,
	// so the exit code is always non-zero (137 = SIGKILL). Check the
	// completion marker instead. The marker is written after the populator
	// is removed, so retry briefly.
	for i := range 5 {
		if c.isVolumeCompleted(ctx, volumeName) {
			return nil
		}
		if i < 4 {
			time.Sleep(time.Second)
		}
	}

	return fmt.Errorf("population did not complete successfully (no .completed marker)")
}

// markVolumeCompleted creates a marker file indicating successful population
func (c *Cluster) markVolumeCompleted(ctx context.Context, volumeName string) error {
	return c.podmanClient.ContainerRunQuiet(ctx,
		config.DefaultClusterImage,
		[]string{"touch", "/mark/.completed"},
		[]string{fmt.Sprintf("%s:/mark:z", volumeName)},
	)
}
