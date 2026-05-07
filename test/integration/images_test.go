package integration_test

import (
	"context"
	"time"

	"github.com/containers/podman/v5/pkg/specgen"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bootc-dev/bink/internal/cluster"
	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/test/integration/helpers"
)

var _ = Describe("Cluster Images Volume", Serial, func() {
	var (
		podmanClient *podman.Client
		volumeName   string
	)

	BeforeEach(func() {
		helpers.RequireImage(config.DefaultNodeImage)

		ctx := context.Background()
		var err error
		podmanClient, err = podman.NewClient()
		Expect(err).ToNot(HaveOccurred())

		_ = podmanClient.ContainerRemove(ctx, cluster.PopulatorContainerName, true)

		// Remove containers using volumes before removing them
		containers, err := podmanClient.ContainerList(ctx, "")
		Expect(err).ToNot(HaveOccurred())
		for _, name := range containers {
			_ = podmanClient.ContainerStop(ctx, name)
			_ = podmanClient.ContainerRemove(ctx, name, true)
		}

		// Determine the expected volume name from the node image label
		kubeadmVersion, err := cluster.GetKubeadmVersionFromImage(ctx, podmanClient, config.DefaultNodeImage)
		Expect(err).ToNot(HaveOccurred())
		volumeName = cluster.ClusterImagesVolumeName(kubeadmVersion)

		Eventually(func() error {
			exists, err := podmanClient.VolumeExists(ctx, volumeName)
			if err != nil {
				return err
			}
			if !exists {
				return nil
			}
			return podmanClient.VolumeRemove(ctx, volumeName)
		}, 30*time.Second, 2*time.Second).Should(Succeed())
	})

	AfterEach(func() {
		ctx := context.Background()

		_ = podmanClient.ContainerRemove(ctx, "test-images-verify", true)
		_ = podmanClient.ContainerRemove(ctx, cluster.PopulatorContainerName, true)
	})

	It("should populate a versioned cluster-images volume with Kubernetes and Calico images", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		By("Verifying volume does not exist")
		exists, err := podmanClient.VolumeExists(ctx, volumeName)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())

		By("Populating the cluster-images volume via EnsureImagesVolume")
		c := cluster.New(cluster.Config{Name: "test-images"})
		returnedVolumeName, err := c.EnsureImagesVolume(ctx, config.DefaultNodeImage)
		Expect(err).ToNot(HaveOccurred())
		Expect(returnedVolumeName).To(Equal(volumeName))

		By("Verifying volume was created with correct name")
		exists, err = podmanClient.VolumeExists(ctx, volumeName)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		By("Verifying volume has version labels")
		labels, err := podmanClient.VolumeInspectLabels(ctx, volumeName)
		Expect(err).ToNot(HaveOccurred())
		Expect(labels).To(HaveKey("bink.kubeadm-version"))
		Expect(labels).To(HaveKey("bink.node-image"))
		Expect(labels["bink.node-image"]).To(Equal(config.DefaultNodeImage))

		By("Verifying volume is marked as completed")
		err = podmanClient.ContainerRunQuiet(ctx,
			config.DefaultClusterImage,
			[]string{"test", "-f", "/check/.completed"},
			[]string{volumeName + ":/check:z"},
		)
		Expect(err).ToNot(HaveOccurred(), ".completed marker should exist in the volume")

		By("Verifying Calico images are stored in the volume")
		verifyContainer := "test-images-verify"
		_, err = podmanClient.ContainerCreate(ctx, &podman.ContainerCreateOptions{
			Name:    verifyContainer,
			Image:   config.DefaultClusterImage,
			Command: []string{"sleep", "infinity"},
			Volumes: []*specgen.NamedVolume{{
				Name: volumeName,
				Dest: "/var/lib/containers/storage",
			}},
		})
		Expect(err).ToNot(HaveOccurred())

		err = podmanClient.ContainerExecQuiet(ctx, verifyContainer,
			[]string{"podman", "image", "exists", config.CalicoImageBase + "/node:" + config.CalicoVersion})
		Expect(err).ToNot(HaveOccurred(), "calico/node image should exist in the volume")

		err = podmanClient.ContainerExecQuiet(ctx, verifyContainer,
			[]string{"podman", "image", "exists", config.CalicoImageBase + "/cni:" + config.CalicoVersion})
		Expect(err).ToNot(HaveOccurred(), "calico/cni image should exist in the volume")
	})
})
