package integration_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bootc-dev/bink/test/integration/helpers"
)

var _ = Describe("Multi-Cluster Operations", func() {
	var clusterNameA, clusterNameB string

	BeforeEach(func() {
		clusterNameA = helpers.GenerateTestClusterName()
		clusterNameB = helpers.GenerateTestClusterName()
	})

	AfterEach(func() {
		helpers.CleanupCluster(clusterNameA)
		helpers.CleanupCluster(clusterNameB)
	})

	It("should manage multiple independent clusters simultaneously", func() {
		By("Creating first cluster")
		helpers.CreateCluster(clusterNameA)

		By("Creating second cluster")
		helpers.CreateCluster(clusterNameB)

		By("Verifying both clusters have running containers")
		containerA := helpers.GetContainer(fmt.Sprintf("k8s-%s-node1", clusterNameA))
		Expect(containerA).ToNot(BeNil(), "Cluster A container should exist")
		Expect(containerA.State).To(Equal("running"), "Cluster A container should be running")

		containerB := helpers.GetContainer(fmt.Sprintf("k8s-%s-node1", clusterNameB))
		Expect(containerB).ToNot(BeNil(), "Cluster B container should exist")
		Expect(containerB.State).To(Equal("running"), "Cluster B container should be running")

		By("Verifying first cluster has working Kubernetes")
		outputA := helpers.SSHExec(clusterNameA, "node1", "sudo kubectl get nodes --kubeconfig=/etc/kubernetes/admin.conf")
		Expect(outputA).To(ContainSubstring("Ready"), "Cluster A node should be Ready")

		By("Verifying second cluster has working Kubernetes")
		outputB := helpers.SSHExec(clusterNameB, "node1", "sudo kubectl get nodes --kubeconfig=/etc/kubernetes/admin.conf")
		Expect(outputB).To(ContainSubstring("Ready"), "Cluster B node should be Ready")

		By("Verifying clusters have separate cluster-keys volumes")
		Expect(helpers.GetVolume(fmt.Sprintf("%s-cluster-keys", clusterNameA))).To(BeTrue(), "Cluster A should have its own keys volume")
		Expect(helpers.GetVolume(fmt.Sprintf("%s-cluster-keys", clusterNameB))).To(BeTrue(), "Cluster B should have its own keys volume")

		By("Verifying both clusters appear in cluster list")
		listCmd := helpers.BinkCmd("cluster", "list")
		listSession := helpers.RunCommand(listCmd)
		listOutput := string(listSession.Out.Contents())
		Expect(listOutput).To(ContainSubstring(clusterNameA), "Cluster list should contain cluster A")
		Expect(listOutput).To(ContainSubstring(clusterNameB), "Cluster list should contain cluster B")

		By("Stopping first cluster")
		stopCmd := helpers.BinkCmd("cluster", "stop", "--cluster-name", clusterNameA)
		stopSession := helpers.RunCommand(stopCmd)
		Expect(stopSession.ExitCode()).To(Equal(0))

		By("Verifying first cluster container is removed")
		Expect(helpers.ContainerExists(fmt.Sprintf("k8s-%s-node1", clusterNameA))).To(BeFalse(), "Cluster A container should be removed")

		By("Verifying second cluster is still running")
		containerB = helpers.GetContainer(fmt.Sprintf("k8s-%s-node1", clusterNameB))
		Expect(containerB).ToNot(BeNil(), "Cluster B container should still exist")
		Expect(containerB.State).To(Equal("running"), "Cluster B container should still be running")

		By("Verifying second cluster Kubernetes is still functional")
		outputB = helpers.SSHExec(clusterNameB, "node1", "sudo kubectl get nodes --kubeconfig=/etc/kubernetes/admin.conf")
		Expect(outputB).To(ContainSubstring("Ready"), "Cluster B node should still be Ready")
	})
})
