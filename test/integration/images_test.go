package integration_test

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
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

		containers, err := podmanClient.ContainerList(ctx, "")
		Expect(err).ToNot(HaveOccurred())
		for _, name := range containers {
			_ = podmanClient.ContainerStop(ctx, name)
			_ = podmanClient.ContainerRemove(ctx, name, true)
		}

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

	It("should create separate volumes for different kubeadm versions", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		fakeVersion := "99.0"
		fakeImage := fmt.Sprintf("localhost/test-fake-node:v%s", fakeVersion)
		fakeVolumeName := cluster.ClusterImagesVolumeName(fakeVersion)

		By("Building a fake node image with a different kubeadm version label")
		containerfile := fmt.Sprintf(`FROM %s
LABEL bink.kubeadm-version=%s`, config.DefaultNodeImage, fakeVersion)
		cmd := exec.CommandContext(ctx, "podman", "build", "-t", fakeImage, "-f", "-", ".")
		cmd.Stdin = strings.NewReader(containerfile)
		output, err := cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "building fake node image: %s", string(output))

		defer func() {
			_ = podmanClient.ImageRemove(ctx, []string{fakeImage}, true)
			_ = podmanClient.VolumeRemove(ctx, fakeVolumeName)
		}()

		By("Populating the volume for the default node image")
		c := cluster.New(cluster.Config{Name: "test-images-v1"})
		vol1, err := c.EnsureImagesVolume(ctx, config.DefaultNodeImage)
		Expect(err).ToNot(HaveOccurred())
		Expect(vol1).To(Equal(volumeName))

		By("Populating the volume for the fake node image")
		c2 := cluster.New(cluster.Config{Name: "test-images-v2"})
		vol2, err := c2.EnsureImagesVolume(ctx, fakeImage)
		Expect(err).ToNot(HaveOccurred())
		Expect(vol2).To(Equal(fakeVolumeName))

		By("Verifying both volumes exist with different names")
		Expect(vol1).ToNot(Equal(vol2), "different kubeadm versions must produce different volume names")

		exists1, err := podmanClient.VolumeExists(ctx, vol1)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists1).To(BeTrue(), "volume for default image should exist")

		exists2, err := podmanClient.VolumeExists(ctx, vol2)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists2).To(BeTrue(), "volume for fake image should exist")

		By("Verifying each volume has the correct labels")
		labels1, err := podmanClient.VolumeInspectLabels(ctx, vol1)
		Expect(err).ToNot(HaveOccurred())
		Expect(labels1["bink.node-image"]).To(Equal(config.DefaultNodeImage))

		labels2, err := podmanClient.VolumeInspectLabels(ctx, vol2)
		Expect(err).ToNot(HaveOccurred())
		Expect(labels2["bink.kubeadm-version"]).To(Equal(fakeVersion))
		Expect(labels2["bink.node-image"]).To(Equal(fakeImage))
	})
})
