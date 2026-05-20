// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/test/integration/helpers"
)

var _ = Describe("Local Registry", func() {
	var clusterName string

	BeforeEach(func() {
		clusterName = helpers.GenerateTestClusterName()
	})

	AfterEach(func() {
		helpers.CollectFailureLogs(clusterName)
		helpers.CleanupCluster(clusterName)
	})

	It("should push an image to the local registry and run it in the cluster", func() {
		By("Creating a single-node cluster")
		helpers.CreateCluster(clusterName)

		By("Pulling busybox image locally")
		helpers.ImagePull(config.TestBusyboxImage)

		registryTag := fmt.Sprintf("localhost:%d/busybox:registry-test", config.RegistryPort)

		By("Tagging busybox for the local registry")
		helpers.ImageTag(config.TestBusyboxImage, "registry-test", fmt.Sprintf("localhost:%d/busybox", config.RegistryPort))

		By("Pushing busybox to the local registry")
		helpers.ImagePush(registryTag, registryTag)

		By("Exposing API and creating Kubernetes client")
		kubeClient, kubeconfigPath := helpers.SetupKubeClient(clusterName)
		defer helpers.CleanupKubeconfig(kubeconfigPath)

		By("Removing control-plane taint to allow scheduling on single-node cluster")
		helpers.RemoveControlPlaneTaint(kubeClient, "node1")

		By("Deploying a pod using the image from the local registry")
		registryImage := fmt.Sprintf("%s.%s:%d/busybox:registry-test",
			config.RegistryHostname, config.ClusterDomain, config.RegistryPort)
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "registry-test",
				Labels: map[string]string{"run": "registry-test"},
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{
					Name:    "busybox",
					Image:   registryImage,
					Command: []string{"sh", "-c", "echo 'registry-pull-success' && sleep 3600"},
				}},
			},
		}
		helpers.CreatePod(kubeClient, "default", pod, 5*time.Minute)

		By("Verifying the pod is running with the registry image")
		runningPod, err := kubeClient.CoreV1().Pods("default").Get(
			context.Background(), "registry-test", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(runningPod.Status.Phase).To(Equal(corev1.PodRunning))
		Expect(runningPod.Spec.Containers[0].Image).To(Equal(registryImage))

		By("Verifying the container is functional by running a command inside it")
		Eventually(func() string {
			result, _ := helpers.PodExec(kubeconfigPath, "default", "registry-test",
				[]string{"echo", "hello"})
			return result
		}, 1*time.Minute, 5*time.Second).Should(ContainSubstring("hello"))

		By("Cleaning up the pod")
		helpers.DeletePod(kubeClient, "default", "registry-test")

		By("Cleaning up the local registry tag")
		helpers.ImageRemove(registryTag)
	})
})
