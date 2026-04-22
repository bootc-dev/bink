package integration_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bootc-dev/bink/test/integration/helpers"
)

var _ = Describe("Cluster Lifecycle", func() {
	Describe("Single-Node Cluster Creation", func() {
		var clusterName string

		BeforeEach(func() {
			clusterName = helpers.GenerateTestClusterName()
		})

		AfterEach(func() {
			helpers.CleanupCluster(clusterName)
		})

		It("should create and initialize a complete Kubernetes cluster", func() {
			By("Creating cluster with auto-assigned API port and 4GB memory")
			cmd := helpers.BinkCmd("cluster", "start", "--cluster-name", clusterName, "--api-port", "0", "--memory", "4096")
			session := helpers.RunCommand(cmd)

			By("Verifying cluster creation command succeeded")
			Expect(session.ExitCode()).To(Equal(0))

			By("Verifying container exists and is running")
			containerName := fmt.Sprintf("k8s-%s-node1", clusterName)
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

			By("Verifying Kubernetes is initialized and node is Ready")
			output := helpers.SSHExec(clusterName, "node1", "sudo kubectl get nodes --kubeconfig=/etc/kubernetes/admin.conf")
			Expect(output).To(ContainSubstring("node1"), "node1 should appear in node list")
			Expect(output).To(ContainSubstring("Ready"), "node1 should be Ready")
			Expect(output).To(ContainSubstring("control-plane"), "node1 should have control-plane role")

			By("Verifying Calico CNI is running")
			Eventually(func() string {
				output, _ := helpers.SSHExecQuiet(clusterName, "node1", "sudo kubectl get pods -n kube-system --kubeconfig=/etc/kubernetes/admin.conf")
				return output
			}, "3m", "10s").Should(ContainSubstring("calico"), "Calico pods should be running")

			By("Verifying DNS (dnsmasq) is configured")
			dnsOutput := helpers.SSHExec(clusterName, "node1", "sudo systemctl status dnsmasq")
			Expect(dnsOutput).To(ContainSubstring("active (running)"), "dnsmasq should be running")

			By("Verifying cluster-hosts file is configured")
			hostsFile := helpers.SSHExec(clusterName, "node1", "cat /var/lib/dnsmasq/cluster-hosts")
			Expect(hostsFile).To(ContainSubstring("node1"), "cluster-hosts should contain node1")
			Expect(hostsFile).To(ContainSubstring("10.0.0.32"), "cluster-hosts should contain node1 IP")
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
		})
	})
})
