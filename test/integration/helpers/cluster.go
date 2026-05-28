// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

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
func CreateCluster(name string) {
	GinkgoWriter.Printf("Creating cluster: %s (with auto-assigned API port)\n", name)
	cmd := BinkCmd("cluster", "start", "--cluster-name", name, "--api-port", "0", "--memory", "1900", "--max-memory", "4096")
	session := RunCommand(cmd, 10*time.Minute)
	Expect(session.ExitCode()).To(Equal(0), "Failed to create cluster: %s", string(session.Err.Contents()))
}

// CreateClusterWithNodeName creates a cluster with a custom control-plane node name
func CreateClusterWithNodeName(name, nodeName string) {
	GinkgoWriter.Printf("Creating cluster: %s with node name: %s (with auto-assigned API port)\n", name, nodeName)
	cmd := BinkCmd("cluster", "start", "--cluster-name", name, "--node-name", nodeName, "--api-port", "0", "--memory", "1900", "--max-memory", "4096")
	session := RunCommand(cmd, 10*time.Minute)
	Expect(session.ExitCode()).To(Equal(0), "Failed to create cluster: %s", string(session.Err.Contents()))
}

// AddNode adds a node to the cluster
func AddNode(clusterName, nodeName string, extraArgs ...string) {
	GinkgoWriter.Printf("Adding node %s to cluster %s\n", nodeName, clusterName)
	args := []string{"node", "add", nodeName, "--cluster-name", clusterName}
	args = append(args, extraArgs...)

	// Use lower memory for worker nodes since they don't run control plane components
	isControlPlane := false
	for _, arg := range extraArgs {
		if arg == "control-plane" {
			isControlPlane = true
			break
		}
	}
	if isControlPlane {
		args = append(args, "--memory", "1900", "--max-memory", "4096")
	} else {
		args = append(args, "--memory", "768", "--max-memory", "2048")
	}

	cmd := BinkCmd(args...)
	session := RunCommand(cmd, 10*time.Minute)
	Expect(session.ExitCode()).To(Equal(0), "Failed to add node: %s", string(session.Err.Contents()))
}

// RemoveNode removes a node from the cluster
func RemoveNode(clusterName, nodeName string, extraArgs ...string) {
	GinkgoWriter.Printf("Removing node %s from cluster %s\n", nodeName, clusterName)
	args := []string{"node", "remove", nodeName, "--cluster-name", clusterName}
	args = append(args, extraArgs...)
	cmd := BinkCmd(args...)
	session := RunCommand(cmd, 5*time.Minute)
	Expect(session.ExitCode()).To(Equal(0), "Failed to remove node: %s", string(session.Err.Contents()))
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

// DNSContainerName returns the DNS container name for a cluster.
func DNSContainerName(clusterName string) string {
	return fmt.Sprintf("k8s-%s-dns", clusterName)
}

// ExposeAPI exposes the API server and generates kubeconfig
func ExposeAPI(clusterName, kubeconfigPath string) {
	GinkgoWriter.Printf("Exposing API for cluster: %s\n", clusterName)
	cmd := BinkCmd("api", "expose", "--cluster-name", clusterName, "--kubeconfig", kubeconfigPath)
	session := RunCommand(cmd)
	Expect(session.ExitCode()).To(Equal(0))
}
