package integration_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/test/integration/helpers"
)

var _ = Describe("HAProxy Load Balancer", func() {
	const (
		node1 = "node1"
		node2 = "node2"
		node3 = "node3"
	)

	var clusterName string

	BeforeEach(func() {
		clusterName = helpers.GenerateTestClusterName()
	})

	AfterEach(func() {
		helpers.CleanupCluster(clusterName)
	})

	It("should failover API access when a control-plane node goes down", func() {
		By("Creating a cluster with 3 control-plane nodes")
		helpers.CreateCluster(clusterName)
		helpers.AddNode(clusterName, node2, "--role", "control-plane")
		helpers.AddNode(clusterName, node3, "--role", "control-plane")

		By("Exposing API and creating Kubernetes client")
		kubeClient, kubeconfigPath := helpers.SetupKubeClient(clusterName)
		defer helpers.CleanupKubeconfig(kubeconfigPath)

		By("Verifying all 3 nodes are Ready")
		helpers.WaitForNodeReady(kubeClient, node1, 5*time.Minute)
		helpers.WaitForNodeReady(kubeClient, node2, 5*time.Minute)
		helpers.WaitForNodeReady(kubeClient, node3, 5*time.Minute)

		nodes, err := kubeClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(nodes.Items).To(HaveLen(3))

		By("Verifying HAProxy container is running")
		haproxyContainer := helpers.NodeContainerName(clusterName, "haproxy")
		container := helpers.GetContainer(haproxyContainer)
		Expect(container).ToNot(BeNil(), "HAProxy container should exist")
		Expect(container.State).To(Equal("running"), "HAProxy container should be running")

		By("Shutting down node1 VM via virsh")
		node1Container := helpers.NodeContainerName(clusterName, node1)
		podmanClient, err := podman.NewClient()
		Expect(err).ToNot(HaveOccurred())
		err = podmanClient.ContainerExecQuiet(context.Background(), node1Container,
			[]string{"bash", "-c", "virsh -c qemu:///session destroy " + node1})
		Expect(err).ToNot(HaveOccurred(), "Failed to destroy node1 VM")

		By("Waiting for HAProxy to detect the failure")
		time.Sleep(15 * time.Second)

		By("Verifying the API is still accessible with the same kubeconfig")
		_, err = kubeClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred(), "API should still be accessible after node1 failure")

		By("Verifying node1 becomes NotReady")
		Eventually(func() bool {
			node, err := kubeClient.CoreV1().Nodes().Get(context.Background(), node1, metav1.GetOptions{})
			if err != nil {
				return false
			}
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady {
					return cond.Status == corev1.ConditionFalse || cond.Status == corev1.ConditionUnknown
				}
			}
			return false
		}, 5*time.Minute, 10*time.Second).Should(BeTrue(), "node1 should become NotReady")

		By("Verifying node2 and node3 are still Ready")
		for _, nodeName := range []string{node2, node3} {
			node, err := kubeClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			ready := false
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					ready = true
					break
				}
			}
			Expect(ready).To(BeTrue(), "Node %s should still be Ready", nodeName)
		}

		By("Verifying the cluster still reports 3 nodes")
		finalNodes, err := kubeClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(finalNodes.Items).To(HaveLen(3))

		nodeStates := make(map[string]string)
		for _, n := range finalNodes.Items {
			for _, cond := range n.Status.Conditions {
				if cond.Type == corev1.NodeReady {
					nodeStates[n.Name] = string(cond.Status)
				}
			}
		}
		GinkgoWriter.Printf("Final node states: %v\n", nodeStates)
		Expect(nodeStates[node1]).To(Or(Equal("False"), Equal("Unknown")), "node1 should be NotReady")
		Expect(nodeStates[node2]).To(Equal("True"), "node2 should be Ready")
		Expect(nodeStates[node3]).To(Equal("True"), "node3 should be Ready")

		By(fmt.Sprintf("HAProxy failover test passed - API accessible via HAProxy after %s shutdown", node1))
	})
})
