package integration_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/node"
	"github.com/bootc-dev/bink/test/integration/helpers"
)

var _ = Describe("Cluster Lifecycle", func() {
	Describe("Single-Node Cluster Creation", func() {
		var clusterName string

		BeforeEach(func() {
			clusterName = helpers.GenerateTestClusterName()
		})

		AfterEach(func() {
			helpers.CollectFailureLogs(clusterName)
			helpers.CleanupCluster(clusterName)
		})

		It("should create and initialize a complete Kubernetes cluster", func() {
			By("Creating cluster with auto-assigned API port and memory ballooning")
			cmd := helpers.BinkCmd("cluster", "start", "--cluster-name", clusterName, "--api-port", "0", "--memory", "1900", "--max-memory", "4096")
			session := helpers.RunCommand(cmd)

			By("Verifying cluster creation command succeeded")
			Expect(session.ExitCode()).To(Equal(0))

			By("Verifying container exists and is running")
			containerName := helpers.NodeContainerName(clusterName, "node1")
			container := helpers.GetContainer(containerName)
			Expect(container).ToNot(BeNil(), "Container %s should exist", containerName)
			Expect(container.State).To(Equal("running"), "Container should be running")

			By("Verifying an API port is published (auto-assigned)")
			portPublished := false
			for _, port := range container.Ports {
				if strings.Contains(port, "6443/tcp") {
					portPublished = true
					break
				}
			}
			Expect(portPublished).To(BeTrue(), "API server port (6443/tcp) should be published to a random host port")

			By("Exposing API and creating Kubernetes client")
			kubeClient, kubeconfigPath := helpers.SetupKubeClient(clusterName)
			defer helpers.CleanupKubeconfig(kubeconfigPath)

			By("Verifying Kubernetes is initialized and node is Ready")
			helpers.WaitForNodeReady(kubeClient, "node1", 5*time.Minute)

			By("Verifying node1 has control-plane role")
			n1, err := kubeClient.CoreV1().Nodes().Get(context.Background(), "node1", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			_, hasCP := n1.Labels["node-role.kubernetes.io/control-plane"]
			Expect(hasCP).To(BeTrue(), "node1 should have control-plane role")

			By("Verifying Calico CNI is running")
			helpers.WaitForPodReady(kubeClient, "kube-system", "k8s-app=calico-node", 3*time.Minute)

			By("Verifying DNS container is running")
			dnsContainer := helpers.GetContainer(helpers.DNSContainerName(clusterName))
			Expect(dnsContainer).ToNot(BeNil(), "DNS container should exist")
			Expect(dnsContainer.State).To(Equal("running"), "DNS container should be running")

			By("Verifying cluster-hosts file in DNS container")
			hostsFile := helpers.PodmanExec(helpers.DNSContainerName(clusterName), "cat /var/lib/dnsmasq/cluster-hosts")
			Expect(hostsFile).To(ContainSubstring("node1"), "cluster-hosts should contain node1")
			expectedIP := node.CalculateClusterIP(clusterName, "node1")
			Expect(hostsFile).To(ContainSubstring(expectedIP), "cluster-hosts should contain node1 IP")

			By("Removing control-plane taint to allow scheduling on single-node cluster")
			helpers.RemoveControlPlaneTaint(kubeClient, "node1")

			By("Deploying a busybox pod to verify cluster is functional")
			busyboxPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "busybox-test",
					Labels: map[string]string{"run": "busybox-test"},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:    "busybox",
						Image:   config.TestBusyboxImage,
						Command: []string{"sleep", "3600"},
					}},
				},
			}
			helpers.CreatePod(kubeClient, "default", busyboxPod, 5*time.Minute)

			By("Verifying the pod is running")
			pod, err := kubeClient.CoreV1().Pods("default").Get(context.Background(), "busybox-test", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))

			By("Cleaning up the busybox pod")
			helpers.DeletePod(kubeClient, "default", "busybox-test")

			By("Verifying cluster appears in cluster list")
			listCmd := helpers.BinkCmd("cluster", "list")
			listSession := helpers.RunCommand(listCmd)
			listOutput := string(listSession.Out.Contents())
			Expect(listOutput).To(ContainSubstring(clusterName), "cluster list should contain the cluster name")
			Expect(listOutput).To(ContainSubstring("1 node(s)"), "cluster list should show 1 node")

			By("Stopping the cluster")
			stopCmd := helpers.BinkCmd("cluster", "stop", "--cluster-name", clusterName)
			stopSession := helpers.RunCommand(stopCmd)
			Expect(stopSession.ExitCode()).To(Equal(0))

			By("Verifying container is removed after stop")
			Expect(helpers.ContainerExists(containerName)).To(BeFalse(), "Container should be removed after stop")
		})

		It("should handle cluster already exists error", func() {
			By("Creating cluster first time")
			helpers.CreateCluster(clusterName)

			By("Attempting to create cluster again")
			cmd := helpers.BinkCmd("cluster", "start", "--cluster-name", clusterName)
			session := helpers.RunCommand(cmd)

			By("Verifying command fails")
			Expect(session.ExitCode()).ToNot(Equal(0))

			By("Verifying error message mentions already exists")
			errorOutput := string(session.Err.Contents())
			Expect(errorOutput).To(ContainSubstring("already exists"))

			By("Stopping cluster with --remove-data")
			stopCmd := helpers.BinkCmd("cluster", "stop", "--remove-data", "--cluster-name", clusterName)
			stopSession := helpers.RunCommand(stopCmd)
			Expect(stopSession.ExitCode()).To(Equal(0))

			By("Verifying container is removed")
			containerName := helpers.NodeContainerName(clusterName, "node1")
			Expect(helpers.ContainerExists(containerName)).To(BeFalse(), "Container should be removed after stop --remove-data")

			By("Verifying cluster-keys volume is removed")
			volumeName := fmt.Sprintf("%s-cluster-keys", clusterName)
			Expect(helpers.GetVolume(volumeName)).To(BeFalse(), "Cluster keys volume should be removed after stop --remove-data")
		})
	})
})
