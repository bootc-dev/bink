package helpers

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

// GenerateTestClusterName creates a unique cluster name for testing
func GenerateTestClusterName() string {
	return fmt.Sprintf("test-bink-%s", uuid.New().String()[:8])
}

// BinkCmd creates a bink command with the given arguments
func BinkCmd(args ...string) *exec.Cmd {
	// Run from project root (two levels up from test/integration)
	return exec.Command("../../bink", args...)
}

// RunCommand executes a command and waits for it to complete
// Returns the gexec session for assertions
func RunCommand(cmd *exec.Cmd, timeout ...time.Duration) *gexec.Session {
	maxTimeout := 5 * time.Minute
	if len(timeout) > 0 {
		maxTimeout = timeout[0]
	}

	session, err := gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
	Expect(err).ToNot(HaveOccurred())

	Eventually(session, maxTimeout).Should(gexec.Exit())
	return session
}

// CreateCluster creates a cluster with the given name
// This is a high-level helper that expects success
// Uses auto-assigned ports (--api-port 0) to avoid port conflicts in tests
// Uses 4GB memory to allow parallel test execution
func CreateCluster(name string) {
	GinkgoWriter.Printf("Creating cluster: %s (with auto-assigned API port and 4GB memory)\n", name)
	cmd := BinkCmd("cluster", "start", "--cluster-name", name, "--api-port", "0", "--memory", "4096")
	session := RunCommand(cmd, 10*time.Minute)
	Expect(session.ExitCode()).To(Equal(0), "Failed to create cluster: %s", string(session.Err.Contents()))
}

// AddNode adds a node to the cluster
// Uses 4GB memory to allow parallel test execution
func AddNode(clusterName, nodeName string, extraArgs ...string) {
	GinkgoWriter.Printf("Adding node %s to cluster %s (with 4GB memory)\n", nodeName, clusterName)
	args := []string{"node", "add", nodeName, "--cluster-name", clusterName, "--memory", "4096"}
	args = append(args, extraArgs...)
	cmd := BinkCmd(args...)
	session := RunCommand(cmd, 10*time.Minute)
	Expect(session.ExitCode()).To(Equal(0), "Failed to add node: %s", string(session.Err.Contents()))
}

// StopCluster stops a cluster
func StopCluster(name string) {
	GinkgoWriter.Printf("Stopping cluster: %s\n", name)
	cmd := BinkCmd("cluster", "stop", "--cluster-name", name)
	session := RunCommand(cmd)
	Expect(session.ExitCode()).To(Equal(0))
}

// CleanupCluster performs full cleanup of a cluster including data
func CleanupCluster(name string) {
	GinkgoWriter.Printf("Cleaning up cluster: %s\n", name)
	cmd := BinkCmd("cluster", "stop", "--remove-data", "--cluster-name", name)
	session := RunCommand(cmd)
	// Don't assert on exit code - cluster may not exist
	_ = session
}

// NodeContainerName returns the container name for a node in a cluster.
func NodeContainerName(clusterName, nodeName string) string {
	return fmt.Sprintf("k8s-%s-%s", clusterName, nodeName)
}

// ExposeAPI exposes the API server and generates kubeconfig
func ExposeAPI(clusterName, kubeconfigPath string) {
	GinkgoWriter.Printf("Exposing API for cluster: %s\n", clusterName)
	cmd := BinkCmd("api", "expose", "--cluster-name", clusterName, "--kubeconfig", kubeconfigPath)
	session := RunCommand(cmd)
	Expect(session.ExitCode()).To(Equal(0))
}
