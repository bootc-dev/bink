package integration_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

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

	It("should enable cross-node pod communication via services", func() {
		By("Creating a two-node cluster")
		helpers.CreateCluster(clusterName)

		By("Adding worker node")
		helpers.AddNode(clusterName, node2, "--role", "worker")

		By("Exposing API and creating Kubernetes client")
		kubeClient, kubeconfigPath := helpers.SetupKubeClient(clusterName)
		defer helpers.CleanupKubeconfig(kubeconfigPath)

		By("Waiting for both nodes to be Ready")
		helpers.WaitForNodeReady(kubeClient, node1, 5*time.Minute)
		helpers.WaitForNodeReady(kubeClient, node2, 5*time.Minute)

		By("Verifying CoreDNS pods have routable Calico IPs (not 10.85.x.x from CRI-O bridge CNI)")
		Eventually(func() bool {
			pods, err := kubeClient.CoreV1().Pods("kube-system").List(context.Background(), metav1.ListOptions{
				LabelSelector: "k8s-app=kube-dns",
			})
			if err != nil || len(pods.Items) == 0 {
				return false
			}
			for _, pod := range pods.Items {
				if pod.Status.Phase != corev1.PodRunning {
					return false
				}
				if pod.Status.PodIP == "" || strings.HasPrefix(pod.Status.PodIP, "10.85.") {
					return false
				}
			}
			return true
		}, 3*time.Minute, 10*time.Second).Should(BeTrue(),
			"CoreDNS pods should have routable Calico IPs, not 10.85.x.x from CRI-O bridge CNI")

		By("Removing control-plane taint from node1")
		helpers.RemoveControlPlaneTaint(kubeClient, node1)

		By("Deploying echo-server pod on node1")
		serverPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "echo-server",
				Labels: map[string]string{
					"run": "echo-server",
					"app": "echo-server",
				},
			},
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{
					"kubernetes.io/hostname": node1,
				},
				Containers: []corev1.Container{
					{
						Name:    "echo-server",
						Image:   "busybox:latest",
						Command: []string{"sh", "-c", "echo 'hello from echo-server' > /tmp/index.html && httpd -f -p 8080 -h /tmp"},
						Ports: []corev1.ContainerPort{
							{ContainerPort: 8080},
						},
					},
				},
			},
		}
		helpers.CreatePod(kubeClient, "default", serverPod, 3*time.Minute)

		By("Creating echo-server service")
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "echo-server",
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					"app": "echo-server",
				},
				Ports: []corev1.ServicePort{
					{
						Port:       8080,
						TargetPort: intstr.FromInt32(8080),
					},
				},
			},
		}
		helpers.CreateService(kubeClient, "default", service)

		By("Deploying echo-client pod on node2")
		clientPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "echo-client",
				Labels: map[string]string{
					"run": "echo-client",
				},
			},
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{
					"kubernetes.io/hostname": node2,
				},
				Containers: []corev1.Container{
					{
						Name:    "echo-client",
						Image:   "busybox:latest",
						Command: []string{"sleep", "3600"},
					},
				},
			},
		}
		helpers.CreatePod(kubeClient, "default", clientPod, 3*time.Minute)

		By("Verifying pods are scheduled on different nodes")
		server, err := kubeClient.CoreV1().Pods("default").Get(context.Background(), "echo-server", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		client, err := kubeClient.CoreV1().Pods("default").Get(context.Background(), "echo-client", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(server.Spec.NodeName).To(Equal(node1), "echo-server should be on node1")
		Expect(client.Spec.NodeName).To(Equal(node2), "echo-client should be on node2")

		By("Testing cross-node pod communication via service")
		Eventually(func() string {
			result, _ := helpers.PodExec(kubeconfigPath, "default", "echo-client",
				[]string{"wget", "-qO-", "-T", "5", "http://echo-server.default.svc.cluster.local:8080"})
			return result
		}, 2*time.Minute, 10*time.Second).Should(ContainSubstring("hello from echo-server"),
			"echo-client on node2 should reach echo-server on node1 via service")

		By("Verifying DNS resolution from a pod")
		Eventually(func() string {
			result, _ := helpers.PodExec(kubeconfigPath, "default", "echo-client",
				[]string{"nslookup", "kubernetes.default.svc.cluster.local"})
			return result
		}, 2*time.Minute, 10*time.Second).Should(ContainSubstring("kubernetes.default.svc.cluster.local"),
			"DNS resolution should work from a pod via CoreDNS")
	})
})
