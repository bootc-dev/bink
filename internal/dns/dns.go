package dns

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/bootc-dev/bink/internal/node"
	"github.com/bootc-dev/bink/internal/ssh"
)

// Manager handles DNS entry management for the cluster
type Manager struct {
	clusterName string
	dnsServer   string
	logger      *logrus.Logger
}

// Config holds DNS manager configuration
type Config struct {
	ClusterName string // Cluster name
	DNSServer   string // Node name running dnsmasq (usually node1)
	Logger      *logrus.Logger
}

// NewManager creates a new DNS manager
func NewManager(cfg Config) *Manager {
	if cfg.Logger == nil {
		cfg.Logger = logrus.New()
	}
	if cfg.DNSServer == "" {
		cfg.DNSServer = "node1"
	}

	return &Manager{
		clusterName: cfg.ClusterName,
		dnsServer:   cfg.DNSServer,
		logger:      cfg.Logger,
	}
}

// AddEntry adds a DNS entry for a node to the cluster DNS server
func (m *Manager) AddEntry(ctx context.Context, nodeName string) error {
	nodeIP := node.CalculateClusterIP(m.clusterName, nodeName)

	m.logger.Infof("=== Adding DNS entry for %s ===", nodeName)
	m.logger.Infof("Node IP: %s", nodeIP)
	m.logger.Infof("DNS Server: %s", m.dnsServer)

	// Create SSH client for DNS server
	sshClient := ssh.NewClientForNode(m.clusterName, m.dnsServer, m.logger)

	// Check if dnsmasq is installed
	output, err := sshClient.Exec(ctx, "rpm -q dnsmasq >/dev/null 2>&1 && echo 'yes' || echo 'no'")
	if err != nil {
		m.logger.Warnf("Could not check dnsmasq installation: %v", err)
	} else {
		dnsmasqInstalled := output
		if len(dnsmasqInstalled) > 0 && dnsmasqInstalled[len(dnsmasqInstalled)-1] == '\n' {
			dnsmasqInstalled = dnsmasqInstalled[:len(dnsmasqInstalled)-1]
		}

		if dnsmasqInstalled != "yes" {
			m.logger.Warn("⚠️  dnsmasq is not installed on " + m.dnsServer)
			m.logger.Warn("This should not happen - dnsmasq is installed via cloud-init on node1")
			m.logger.Warn("You may need to recreate the node with the current scripts")
			m.logger.Warn("")
			m.logger.Warn("Continuing anyway - adding to hosts file...")
		}
	}

	// Ensure dnsmasq directory and file exist
	if _, err := sshClient.Exec(ctx, "sudo bash -c 'mkdir -p /var/lib/dnsmasq && touch /var/lib/dnsmasq/cluster-hosts'"); err != nil {
		return fmt.Errorf("failed to ensure dnsmasq directory: %w", err)
	}

	// Add entry to dnsmasq hosts file (avoiding duplicates)
	// Remove any existing entry for this node, then add new entry
	cmd := fmt.Sprintf(
		`sudo bash -c "grep -v '^[^#]*[[:space:]]%s[[:space:]]' /var/lib/dnsmasq/cluster-hosts > /tmp/cluster-hosts.tmp || true && echo '%s %s %s.cluster.local' >> /tmp/cluster-hosts.tmp && mv /tmp/cluster-hosts.tmp /var/lib/dnsmasq/cluster-hosts && chown dnsmasq:dnsmasq /var/lib/dnsmasq/cluster-hosts && chmod 644 /var/lib/dnsmasq/cluster-hosts && restorecon /var/lib/dnsmasq/cluster-hosts && if systemctl is-active dnsmasq >/dev/null 2>&1; then systemctl restart dnsmasq; fi"`,
		nodeName, nodeIP, nodeName, nodeName,
	)

	if _, err := sshClient.Exec(ctx, cmd); err != nil {
		return fmt.Errorf("failed to add DNS entry: %w", err)
	}

	m.logger.Infof("✅ DNS entry added: %s -> %s", nodeName, nodeIP)

	// Flush systemd-resolved cache on DNS server
	m.logger.Infof("Flushing DNS cache on %s...", m.dnsServer)
	if _, err := sshClient.Exec(ctx, "sudo resolvectl flush-caches"); err != nil {
		m.logger.Warnf("Failed to flush DNS cache: %v", err)
	}

	// Show current entries
	m.logger.Info("")
	m.logger.Info("Current DNS entries:")
	entries, err := sshClient.Exec(ctx, "sudo cat /var/lib/dnsmasq/cluster-hosts")
	if err != nil {
		m.logger.Warnf("Failed to read DNS entries: %v", err)
	} else {
		fmt.Print(entries)
	}

	return nil
}
