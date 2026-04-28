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
	BeforeEach(func() {
		helpers.RequireImage(config.DefaultPopulatorImage)

		ctx := context.Background()
		podmanClient, err := podman.NewClient()
		Expect(err).ToNot(HaveOccurred())

		_ = podmanClient.ContainerRemove(ctx, cluster.PopulatorContainerName, true)

		exists, err := podmanClient.VolumeExists(ctx, cluster.ClusterImagesVolume)
		Expect(err).ToNot(HaveOccurred())
		if exists {
			err = podmanClient.VolumeRemove(ctx, cluster.ClusterImagesVolume)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	AfterEach(func() {
		ctx := context.Background()
		podmanClient, err := podman.NewClient()
		Expect(err).ToNot(HaveOccurred())

		_ = podmanClient.ContainerRemove(ctx, "test-images-verify", true)
		_ = podmanClient.ContainerRemove(ctx, cluster.PopulatorContainerName, true)
	})

	It("should populate the cluster-images volume with Kubernetes and Calico images", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		By("Verifying volume does not exist")
		podmanClient, err := podman.NewClient()
		Expect(err).ToNot(HaveOccurred())

		exists, err := podmanClient.VolumeExists(ctx, cluster.ClusterImagesVolume)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())

		By("Populating the cluster-images volume via EnsureImagesVolume")
		c := cluster.New(cluster.Config{Name: "test-images"})
		err = c.EnsureImagesVolume(ctx)
		Expect(err).ToNot(HaveOccurred())

		By("Verifying volume was created")
		exists, err = podmanClient.VolumeExists(ctx, cluster.ClusterImagesVolume)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		By("Verifying volume is marked as completed")
		err = podmanClient.ContainerRunQuiet(ctx,
			config.DefaultPopulatorImage,
			[]string{"test", "-f", "/check/.completed"},
			[]string{cluster.ClusterImagesVolume + ":/check:z"},
		)
		Expect(err).ToNot(HaveOccurred(), ".completed marker should exist in the volume")

		By("Verifying images are stored in the volume")
		verifyContainer := "test-images-verify"
		_, err = podmanClient.ContainerCreate(ctx, &podman.ContainerCreateOptions{
			Name:    verifyContainer,
			Image:   config.DefaultPopulatorImage,
			Command: []string{"sleep", "infinity"},
			Volumes: []*specgen.NamedVolume{{
				Name: cluster.ClusterImagesVolume,
				Dest: "/var/lib/containers/storage",
			}},
		})
		Expect(err).ToNot(HaveOccurred())

		err = podmanClient.ContainerExecQuiet(ctx, verifyContainer,
			[]string{"skopeo", "inspect", "containers-storage:registry.k8s.io/kube-apiserver:" + config.KubernetesVersion})
		Expect(err).ToNot(HaveOccurred(), "kube-apiserver image should exist in the volume")

		err = podmanClient.ContainerExecQuiet(ctx, verifyContainer,
			[]string{"skopeo", "inspect", "containers-storage:" + config.CalicoImageBase + "/node:" + config.CalicoVersion})
		Expect(err).ToNot(HaveOccurred(), "calico/node image should exist in the volume")

		err = podmanClient.ContainerExecQuiet(ctx, verifyContainer,
			[]string{"skopeo", "inspect", "containers-storage:" + config.CalicoImageBase + "/cni:" + config.CalicoVersion})
		Expect(err).ToNot(HaveOccurred(), "calico/cni image should exist in the volume")
	})
})
