package integration_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/test/integration/helpers"
)

var _ = Describe("Kubernetes Workloads", func() {
	var clusterName string

	BeforeEach(func() {
		clusterName = helpers.GenerateTestClusterName()
	})

	AfterEach(func() {
		helpers.CleanupCluster(clusterName)
	})

	It("should schedule pods across multiple nodes", func() {
		By("Creating a cluster with two worker nodes")
		helpers.CreateCluster(clusterName)
		helpers.AddNode(clusterName, "node2", "--role", "worker")
		helpers.AddNode(clusterName, "node3", "--role", "worker")

		By("Exposing API and creating Kubernetes client")
		kubeClient, kubeconfigPath := helpers.SetupKubeClient(clusterName)
		defer helpers.CleanupKubeconfig(kubeconfigPath)

		By("Removing control-plane taint to allow DaemonSet on all nodes")
		helpers.RemoveControlPlaneTaint(kubeClient, "node1")

		By("Waiting for all nodes to be Ready")
		helpers.WaitForNodeReady(kubeClient, "node1", 3*time.Minute)
		helpers.WaitForNodeReady(kubeClient, "node2", 3*time.Minute)
		helpers.WaitForNodeReady(kubeClient, "node3", 3*time.Minute)

		By("Deploying a DaemonSet across all nodes")
		helpers.ApplyDaemonSet(kubeClient, "default", fmt.Sprintf(`
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: spread-test
spec:
  selector:
    matchLabels:
      app: spread-test
  template:
    metadata:
      labels:
        app: spread-test
    spec:
      containers:
      - name: busybox
        image: %s
        command: ["sleep", "3600"]
`, config.TestBusyboxImage))

		By("Waiting for DaemonSet to schedule on all nodes")
		helpers.WaitForDaemonSetReady(kubeClient, "default", "spread-test", 3, 10*time.Minute)

		By("Verifying pods are running on all three nodes")
		podNodes := helpers.GetPodNodes(kubeClient, "default", "app=spread-test")
		Expect(podNodes).To(HaveLen(3))
		Expect(podNodes).To(ContainElement("node1"))
		Expect(podNodes).To(ContainElement("node2"))
		Expect(podNodes).To(ContainElement("node3"))
	})
})
