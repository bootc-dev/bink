package helpers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootc-dev/bink/internal/podman"
	. "github.com/onsi/ginkgo/v2"
)

const logDir = "/tmp/bink-logs"

func CollectFailureLogs(clusterNames ...string) {
	report := CurrentSpecReport()
	if !report.Failed() {
		return
	}

	ctx := context.Background()
	podmanClient, err := podman.NewClient()
	if err != nil {
		GinkgoWriter.Printf("Log collection: failed to create podman client: %v\n", err)
		return
	}

	testName := report.FullText()
	testDir := sanitizeTestName(testName)

	GinkgoWriter.Printf("=== Collecting failure logs for test: %s ===\n", testName)

	for _, cluster := range clusterNames {
		if cluster == "" {
			continue
		}
		containers, err := podmanClient.ContainerList(ctx, fmt.Sprintf("label=bink.cluster-name=%s", cluster))
		if err != nil {
			GinkgoWriter.Printf("Log collection: failed to list containers for cluster %s: %v\n", cluster, err)
			continue
		}
		for _, ctr := range containers {
			collectContainerLogs(ctx, podmanClient, testDir, ctr)
		}
	}
}

func collectContainerLogs(ctx context.Context, client *podman.Client, testDir, containerName string) {
	dir := filepath.Join(logDir, testDir, containerName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		GinkgoWriter.Printf("Log collection: failed to create dir %s: %v\n", dir, err)
		return
	}

	GinkgoWriter.Printf("  %s -> %s\n", containerName, dir)

	sshCmds := map[string]string{
		"journal.log": "sudo journalctl -n 500 --no-pager",
		"kubelet.log": "sudo journalctl -u kubelet -n 200 --no-pager",
		"crio.log":    "sudo journalctl -u crio -n 200 --no-pager",
		"dmesg.log":   "sudo dmesg",
	}

	for filename, cmd := range sshCmds {
		sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -i /var/run/cluster/cluster.key -p 2222 core@localhost '%s'", cmd)
		output, err := client.ContainerExec(ctx, containerName, []string{"sh", "-c", sshCmd})
		if err != nil {
			output = fmt.Sprintf("(failed to collect: %v)\n%s", err, output)
		}
		_ = os.WriteFile(filepath.Join(dir, filename), []byte(output), 0644)
	}
}

func sanitizeTestName(name string) string {
	r := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_")
	return r.Replace(name)
}
