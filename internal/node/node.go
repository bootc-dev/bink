package node

import (
	"context"
	"fmt"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/internal/virsh"
	"github.com/bootc-dev/bink/internal/virtiofsd"
)

type Node struct {
	Name            string
	ContainerName   string
	ClusterName     string
	ClusterIP       string
	ClusterMAC      string
	IsControlPlane  bool
	Memory          int
	VCPUs           int
	BaseDisk        string
	ImagesImage     string
	APIPort         int // Configured API port (0 = auto-assign)
	AssignedAPIPort int // Actual assigned port after container creation

	podman       *podman.Client
	virsh        *virsh.Client
	virtiofsdMgr *virtiofsd.Manager
}

type NodeOption func(*Node) error

func WithImagesImage(image string) NodeOption {
	return func(n *Node) error {
		n.ImagesImage = image
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
		n.Memory = memory
		return nil
	}
}

func New(name string, isControlPlane bool, opts ...NodeOption) (*Node, error) {
	n := &Node{
		Name:           name,
		IsControlPlane: isControlPlane,
		ImagesImage:    config.DefaultBootcImagesImage,
		Memory:         config.DefaultMemory,
		VCPUs:          config.DefaultVCPUs,
		BaseDisk:       config.DefaultBaseDisk,
	}

	for _, opt := range opts {
		if err := opt(n); err != nil {
			return nil, err
		}
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
	n.ClusterIP = CalculateClusterIP(name)
	n.ClusterMAC = CalculateClusterMAC(name)

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

	// Setup virtiofsd to share container images
	if err := n.setupVirtiofsd(ctx); err != nil {
		return fmt.Errorf("setting up virtiofsd: %w", err)
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
