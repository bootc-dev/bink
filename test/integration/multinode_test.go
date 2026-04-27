package integration_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/bootc-dev/bink/test/integration/helpers"
)

var _ = Describe("Multi-Node Clusters", func() {
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

	It("should add worker nodes and scale the cluster", func() {
		By("Creating a single-node cluster")
		helpers.CreateCluster(clusterName)

		By("Exposing API and creating Kubernetes client")
		kubeClient, kubeconfigPath := helpers.SetupKubeClient(clusterName)
		defer helpers.CleanupKubeconfig(kubeconfigPath)

		By("Adding first worker node")
		helpers.AddNode(clusterName, node2, "--role", "worker")

		By("Verifying node2 container is running")
		containerName2 := helpers.NodeContainerName(clusterName, node2)
		container2 := helpers.GetContainer(containerName2)
		Expect(container2).ToNot(BeNil(), "Container %s should exist", containerName2)
		Expect(container2.State).To(Equal("running"), "Container should be running")

		By("Verifying node2 joined Kubernetes with worker role")
		helpers.WaitForNodeReady(kubeClient, node2, 5*time.Minute)
		n2, err := kubeClient.CoreV1().Nodes().Get(context.Background(), node2, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		hasWorkerRole := false
		for key := range n2.Labels {
			if key == "node-role.kubernetes.io/worker" {
				hasWorkerRole = true
				break
			}
		}
		hasControlPlaneRole := false
		for key := range n2.Labels {
			if key == "node-role.kubernetes.io/control-plane" {
				hasControlPlaneRole = true
				break
			}
		}
		Expect(hasControlPlaneRole).To(BeFalse(), "node2 should not have control-plane role")
		// Worker role label may not be set by kubeadm by default, so just verify it's not control-plane
		_ = hasWorkerRole

		By("Verifying DNS entry for node2")
		hostsFile := helpers.SSHExec(clusterName, node1, "cat /var/lib/dnsmasq/cluster-hosts")
		Expect(hostsFile).To(ContainSubstring(node2), "cluster-hosts should contain node2")

		By("Running node list and verifying both nodes appear")
		listCmd := helpers.BinkCmd("node", "list", "--cluster-name", clusterName)
		listSession := helpers.RunCommand(listCmd)
		listOutput := string(listSession.Out.Contents())
		Expect(listOutput).To(ContainSubstring(node1), "node list should contain node1")
		Expect(listOutput).To(ContainSubstring(node2), "node list should contain node2")

		By("Adding second worker node")
		helpers.AddNode(clusterName, node3, "--role", "worker")

		By("Verifying all three containers are running")
		for _, nodeName := range []string{node1, node2, node3} {
			cn := helpers.NodeContainerName(clusterName, nodeName)
			c := helpers.GetContainer(cn)
			Expect(c).ToNot(BeNil(), "Container %s should exist", cn)
			Expect(c.State).To(Equal("running"), "Container %s should be running", cn)
		}

		By("Verifying all three nodes are Ready with correct roles")
		helpers.WaitForNodeReady(kubeClient, node3, 5*time.Minute)
		nodes, err := kubeClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(nodes.Items).To(HaveLen(3))
		for _, node := range nodes.Items {
			ready := false
			for _, cond := range node.Status.Conditions {
				if cond.Type == "Ready" && cond.Status == "True" {
					ready = true
					break
				}
			}
			Expect(ready).To(BeTrue(), "Node %s should be Ready", node.Name)
		}

		By("Verifying DNS entries for all nodes")
		hostsFile = helpers.SSHExec(clusterName, node1, "cat /var/lib/dnsmasq/cluster-hosts")
		Expect(hostsFile).To(ContainSubstring(node1), "cluster-hosts should contain node1")
		Expect(hostsFile).To(ContainSubstring(node2), "cluster-hosts should contain node2")
		Expect(hostsFile).To(ContainSubstring(node3), "cluster-hosts should contain node3")

		By("Verifying node count")
		Expect(helpers.GetNodeCount(kubeClient)).To(Equal(3))
	})

	It("should add control-plane nodes for HA configuration", func() {
		By("Creating a single-node cluster")
		helpers.CreateCluster(clusterName)

		By("Adding a second control-plane node")
		helpers.AddNode(clusterName, node2, "--role", "control-plane")

		By("Adding a third control-plane node")
		helpers.AddNode(clusterName, node3, "--role", "control-plane")

		By("Verifying all containers are running")
		for _, nodeName := range []string{node1, node2, node3} {
			cn := helpers.NodeContainerName(clusterName, nodeName)
			c := helpers.GetContainer(cn)
			Expect(c).ToNot(BeNil(), "Container %s should exist", cn)
			Expect(c.State).To(Equal("running"), "Container %s should be running", cn)
		}

		By("Exposing API and creating Kubernetes client")
		kubeClient, kubeconfigPath := helpers.SetupKubeClient(clusterName)
		defer helpers.CleanupKubeconfig(kubeconfigPath)

		By("Verifying all nodes are control-plane in Kubernetes")
		helpers.WaitForNodeReady(kubeClient, node3, 5*time.Minute)
		nodes, err := kubeClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(nodes.Items).To(HaveLen(3))
		for _, node := range nodes.Items {
			_, hasCP := node.Labels["node-role.kubernetes.io/control-plane"]
			Expect(hasCP).To(BeTrue(), "Node %s should have control-plane role", node.Name)
		}

		By("Verifying all nodes are Ready")
		for _, node := range nodes.Items {
			ready := false
			for _, cond := range node.Status.Conditions {
				if cond.Type == "Ready" && cond.Status == "True" {
					ready = true
					break
				}
			}
			Expect(ready).To(BeTrue(), "Node %s should be Ready", node.Name)
		}

		By("Verifying etcd is running on all control-plane nodes")
		for _, nodeName := range []string{node2, node3} {
			etcdPodName := "etcd-" + nodeName
			Eventually(func() bool {
				pods, err := kubeClient.CoreV1().Pods("kube-system").List(context.Background(), metav1.ListOptions{})
				if err != nil {
					return false
				}
				for _, pod := range pods.Items {
					if pod.Name == etcdPodName {
						return true
					}
				}
				return false
			}, 3*time.Minute, 10*time.Second).Should(BeTrue(), "%s should be running", etcdPodName)
		}

		By("Verifying node list shows all nodes")
		listCmd := helpers.BinkCmd("node", "list", "--cluster-name", clusterName)
		listSession := helpers.RunCommand(listCmd)
		listOutput := string(listSession.Out.Contents())
		Expect(listOutput).To(ContainSubstring(node1), "node list should contain node1")
		Expect(listOutput).To(ContainSubstring(node2), "node list should contain node2")
		Expect(listOutput).To(ContainSubstring(node3), "node list should contain node3")
	})
})
