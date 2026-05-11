package node

import (
	"fmt"
	"testing"

	"github.com/bootc-dev/bink/internal/config"
)

func TestCalculateClusterIP(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		nodeName    string
		expectedIP  string
	}{
		{"default cluster node1", "podman", "node1", "10.0.0.83"},
		{"default cluster node2", "podman", "node2", "10.0.0.166"},
		{"custom cluster", "mycluster", "node1", "10.0.0.242"},
		{"empty cluster name", "", "node1", "10.0.0.68"},
		{"empty node name", "podman", "", "10.0.0.98"},
		{"both empty", "", "", "10.0.0.112"},
		{"long names", "my-very-long-cluster-name", "my-very-long-node-name", "10.0.0.13"},
		{"special characters", "cluster-1", "node_2", "10.0.0.73"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := CalculateClusterIP(tt.clusterName, tt.nodeName)
			if ip != tt.expectedIP {
				t.Errorf("got %s, want %s", ip, tt.expectedIP)
			}
		})
	}
}


func TestCalculateClusterIP_Deterministic(t *testing.T) {
	for i := 0; i < 10; i++ {
		ip1 := CalculateClusterIP("podman", "node1")
		ip2 := CalculateClusterIP("podman", "node1")
		if ip1 != ip2 {
			t.Fatalf("non-deterministic: got %s and %s", ip1, ip2)
		}
	}
}

func TestCalculateClusterIP_DifferentInputsDifferentIPs(t *testing.T) {
	ip1 := CalculateClusterIP("podman", "node1")
	ip2 := CalculateClusterIP("podman", "node2")
	ip3 := CalculateClusterIP("other", "node1")

	if ip1 == ip2 {
		t.Errorf("same cluster different nodes should likely differ: both %s", ip1)
	}
	if ip1 == ip3 {
		t.Errorf("different clusters same node should likely differ: both %s", ip1)
	}
}

func TestCalculateClusterIPExcluding(t *testing.T) {
	// podman/node1 hashes to suffix 83 -> 10.0.0.83
	const baseIP = "10.0.0.83"

	t.Run("nil exclusion returns same as CalculateClusterIP", func(t *testing.T) {
		ip := CalculateClusterIPExcluding("podman", "node1", nil)
		if ip != baseIP {
			t.Errorf("got %s, want %s", ip, baseIP)
		}
	})

	t.Run("empty exclusion returns same as CalculateClusterIP", func(t *testing.T) {
		ip := CalculateClusterIPExcluding("podman", "node1", []string{})
		if ip != baseIP {
			t.Errorf("got %s, want %s", ip, baseIP)
		}
	})

	t.Run("unrelated exclusion returns same IP", func(t *testing.T) {
		ip := CalculateClusterIPExcluding("podman", "node1", []string{"10.0.0.99"})
		if ip != baseIP {
			t.Errorf("got %s, want %s", ip, baseIP)
		}
	})

	t.Run("excluding base IP returns next sequential", func(t *testing.T) {
		// suffix 83 excluded -> next is 84
		ip := CalculateClusterIPExcluding("podman", "node1", []string{baseIP})
		if ip != "10.0.0.84" {
			t.Errorf("got %s, want 10.0.0.84", ip)
		}
	})

	t.Run("excluding multiple consecutive IPs skips all", func(t *testing.T) {
		excluded := []string{"10.0.0.83", "10.0.0.84", "10.0.0.85"}
		ip := CalculateClusterIPExcluding("podman", "node1", excluded)
		if ip != "10.0.0.86" {
			t.Errorf("got %s, want 10.0.0.86", ip)
		}
	})

	t.Run("all IPs excluded returns last attempted", func(t *testing.T) {
		var allIPs []string
		for i := 0; i < config.ClusterIPRangeSize; i++ {
			allIPs = append(allIPs, fmt.Sprintf("%s.%d", config.ClusterIPPrefix, config.ClusterIPMinSuffix+i))
		}

		ip := CalculateClusterIPExcluding("podman", "node1", allIPs)
		if ip == "" {
			t.Error("should return an IP even when all are excluded")
		}
	})
}

func TestCalculateClusterMAC(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		nodeName    string
		expectedMAC string
	}{
		{"default cluster node1", "podman", "node1", "52:54:01:49:3f:1f"},
		{"default cluster node2", "podman", "node2", "52:54:01:9c:10:3c"},
		{"custom cluster", "mycluster", "node1", "52:54:01:e8:2b:18"},
		{"empty names", "", "", "52:54:01:66:66:cd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mac := CalculateClusterMAC(tt.clusterName, tt.nodeName)
			if mac != tt.expectedMAC {
				t.Errorf("got %s, want %s", mac, tt.expectedMAC)
			}
		})
	}
}

func TestCalculateClusterMAC_Deterministic(t *testing.T) {
	for i := 0; i < 10; i++ {
		mac1 := CalculateClusterMAC("podman", "node1")
		mac2 := CalculateClusterMAC("podman", "node1")
		if mac1 != mac2 {
			t.Fatalf("non-deterministic: got %s and %s", mac1, mac2)
		}
	}
}

func TestCalculateClusterMAC_Format(t *testing.T) {
	mac := CalculateClusterMAC("podman", "node1")

	var a, b, c int
	prefix := config.ClusterMACPrefix
	remaining := mac[len(prefix)+1:]
	n, err := fmt.Sscanf(remaining, "%02x:%02x:%02x", &a, &b, &c)
	if err != nil || n != 3 {
		t.Errorf("MAC %q does not match expected format %s:xx:xx:xx", mac, prefix)
	}
	if a < 0 || a > 255 || b < 0 || b > 255 || c < 0 || c > 255 {
		t.Errorf("MAC octets out of byte range: %02x:%02x:%02x", a, b, c)
	}
}

func TestCalculateClusterMAC_DifferentInputs(t *testing.T) {
	mac1 := CalculateClusterMAC("podman", "node1")
	mac2 := CalculateClusterMAC("podman", "node2")
	mac3 := CalculateClusterMAC("other", "node1")

	if mac1 == mac2 {
		t.Errorf("same cluster different nodes should differ: both %s", mac1)
	}
	if mac1 == mac3 {
		t.Errorf("different clusters same node should differ: both %s", mac1)
	}
}
