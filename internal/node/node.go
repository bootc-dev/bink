package node

import (
	"context"
	"fmt"
	"net"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/internal/virsh"
)

type Node struct {
	Name                string
	ContainerName       string
	ClusterName         string
	ClusterIP           string
	ClusterMAC          string
	DNSIP               string
	IsControlPlane      bool
	Memory              int
	MaxMemory           int
	VCPUs               int
	BaseDisk            string
	NodeImage           string
	ClusterImagesVolume string
	APIPort             int // Configured API port (0 = auto-assign)
	AssignedAPIPort     int // Actual assigned port after container creation

	usedIPs []string
	podman  *podman.Client
	virsh   *virsh.Client
}

type NodeOption func(*Node) error

func WithNodeImage(image string) NodeOption {
	return func(n *Node) error {
		n.NodeImage = image
		return nil
	}
}

func WithClusterName(name string) NodeOption {
	return func(n *Node) error {
		n.ClusterName = name
		return nil
	}
}

func WithAPIPort(port int) NodeOption {
	return func(n *Node) error {
		n.APIPort = port
		return nil
	}
}

func WithMemory(memory int) NodeOption {
	return func(n *Node) error {
		if memory > 0 {
			n.Memory = memory
		}
		return nil
	}
}

func WithMaxMemory(maxMemory int) NodeOption {
	return func(n *Node) error {
		if maxMemory > 0 {
			n.MaxMemory = maxMemory
		}
		return nil
	}
}

func WithUsedIPs(ips []string) NodeOption {
	return func(n *Node) error {
		n.usedIPs = ips
		return nil
	}
}

func WithDNSIP(ip string) NodeOption {
	return func(n *Node) error {
		if net.ParseIP(ip) == nil {
			return fmt.Errorf("invalid DNS IP address: %q", ip)
		}
		n.DNSIP = ip
		return nil
	}
}

func WithClusterImagesVolume(volumeName string) NodeOption {
	return func(n *Node) error {
		n.ClusterImagesVolume = volumeName
		return nil
	}
}

func New(name string, isControlPlane bool, opts ...NodeOption) (*Node, error) {
	var memory, maxMemory int
	switch {
	case isControlPlane:
		memory = config.DefaultControlPlaneMemory
		maxMemory = config.DefaultControlPlaneMaxMemory
	default:
		memory = config.DefaultWorkerMemory
		maxMemory = config.DefaultWorkerMaxMemory
	}

	n := &Node{
		Name:           name,
		IsControlPlane: isControlPlane,
		NodeImage:      config.DefaultNodeImage,
		Memory:         memory,
		MaxMemory:      maxMemory,
		VCPUs:          config.DefaultVCPUs,
		BaseDisk:       config.DefaultBaseDisk,
	}

	for _, opt := range opts {
		if err := opt(n); err != nil {
			return nil, err
		}
	}

	if n.ClusterImagesVolume == "" {
		return nil, fmt.Errorf("cluster images volume name is required: use WithClusterImagesVolume()")
	}

	if n.podman == nil {
		podmanClient, err := podman.NewClient()
		if err != nil {
			return nil, fmt.Errorf("creating podman client: %w", err)
		}
		n.podman = podmanClient
	}

	// Build container name with cluster name for uniqueness
	clusterLabel := n.ClusterName
	if clusterLabel == "" {
		clusterLabel = config.DefaultNetworkName
	}
	n.ContainerName = config.ContainerNamePrefix + clusterLabel + "-" + name

	// Set cluster IP and MAC
	n.ClusterIP = CalculateClusterIPExcluding(clusterLabel, name, n.usedIPs)
	n.ClusterMAC = CalculateClusterMAC(clusterLabel, name)

	// Handle API port logic
	switch n.APIPort {
	case -1:
		n.APIPort = 0
	case 0:
		if isControlPlane {
			n.APIPort = config.DefaultAPIServerPort
		}
	}

	n.virsh = virsh.NewClient(n.ContainerName, n.podman)

	return n, nil
}

func (n *Node) Create(ctx context.Context) error {
	if err := n.createContainer(ctx); err != nil {
		return fmt.Errorf("creating container: %w", err)
	}

	if err := n.setupSSHKeys(ctx); err != nil {
		return fmt.Errorf("setting up SSH keys: %w", err)
	}

	if err := n.createOverlayDisk(ctx); err != nil {
		return fmt.Errorf("creating overlay disk: %w", err)
	}

	if err := n.generateCloudInit(ctx); err != nil {
		return fmt.Errorf("generating cloud-init: %w", err)
	}

	if err := n.createVM(ctx); err != nil {
		return fmt.Errorf("creating VM: %w", err)
	}

	return nil
}

func (n *Node) Exists(ctx context.Context) (bool, error) {
	return n.podman.ContainerExists(ctx, n.ContainerName)
}

func (n *Node) role() string {
	if n.IsControlPlane {
		return "control-plane"
	}
	return "worker"
}
