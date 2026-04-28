package node

import (
	"context"
	"fmt"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/bootc-dev/bink/internal/virsh"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	nettypes "github.com/containers/common/libnetwork/types"
)

func (n *Node) createContainer(ctx context.Context) error {
	exists, err := n.Exists(ctx)
	if err != nil {
		return err
	}

	if exists {
		return fmt.Errorf("container %s already exists", n.ContainerName)
	}

	logrus.Infof("Creating container %s", n.ContainerName)
	logrus.Infof("Using node image: %s", n.NodeImage)

	// Cluster images volume is shared across all clusters (same images for all)
	clusterImagesVolume := "cluster-images"

	// Cluster keys volume is namespaced per cluster
	clusterLabel := n.ClusterName
	if clusterLabel == "" {
		clusterLabel = config.DefaultNetworkName
	}
	clusterKeysVolume := fmt.Sprintf("%s-cluster-keys", clusterLabel)

	// Use cluster-specific network for isolation
	networkName := n.ClusterName
	if networkName == "" {
		networkName = config.DefaultNetworkName
	}

	opts := &podman.ContainerCreateOptions{
		Name:    n.ContainerName,
		Image:   config.DefaultClusterImage,
		Network: networkName,
		Devices: []specs.LinuxDevice{
			{Path: "/dev/kvm"},
			{Path: "/dev/fuse"},
		},
		ImageVolumes: []*specgen.ImageVolume{
			{
				Source:      n.NodeImage,
				Destination: "/images",
			},
		},
		Volumes: []*specgen.NamedVolume{
			{
				Name:    clusterKeysVolume,
				Dest:    "/var/run/cluster",
				Options: []string{"z"},
			},
			{
				Name:    clusterImagesVolume,
				Dest:    "/var/lib/cluster-images",
				Options: []string{"z"},
			},
		},
		Labels: map[string]string{
			"bink.cluster-name": clusterLabel,
			"bink.node-name":    n.Name,
		},
		CapAdd:      []string{"SYS_ADMIN"},
		SelinuxOpts: []string{"disable"},
	}

	if n.IsControlPlane {
		opts.PortMappings = []nettypes.PortMapping{
			{
				HostPort:      uint16(n.APIPort),
				ContainerPort: 6443,
				Protocol:      "tcp",
			},
		}
	}

	containerID, err := n.podman.ContainerCreate(ctx, opts)
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}

	logrus.Infof("Container %s created: %s", n.ContainerName, containerID)

	// Assign API port for control plane nodes
	if n.IsControlPlane {
		switch n.APIPort {
		case 0:
			assignedPort, err := n.podman.GetPublishedPort(ctx, n.ContainerName, "6443/tcp")
			if err != nil {
				return fmt.Errorf("getting assigned API port: %w", err)
			}
			n.AssignedAPIPort = assignedPort
			logrus.Infof("API server port auto-assigned: %d", assignedPort)
		default:
			n.AssignedAPIPort = n.APIPort
			logrus.Infof("API server port: %d", n.AssignedAPIPort)
		}
	}

	containerIP, err := n.podman.ContainerInspect(ctx, n.ContainerName, "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}")
	if err != nil {
		return fmt.Errorf("getting container IP: %w", err)
	}

	logrus.Infof("Container IP: %s (VM will inherit this via passt)", containerIP)

	// Create workspace directory for overlay disks and cloud-init ISOs
	if err := n.podman.ContainerExecQuiet(ctx, n.ContainerName, []string{"mkdir", "-p", "/workspace"}); err != nil {
		return fmt.Errorf("creating workspace directory: %w", err)
	}

	return nil
}

func (n *Node) setupSSHKeys(ctx context.Context) error {
	logrus.Info("Setting up cluster SSH key")

	// Check if key already exists in the volume
	checkCmd := []string{"test", "-f", config.ClusterKeyPath}
	err := n.podman.ContainerExecQuiet(ctx, n.ContainerName, checkCmd)
	if err == nil {
		logrus.Info("Using existing cluster SSH key")
		return nil
	}

	logrus.Info("Generating cluster SSH key in cluster-keys volume")

	// Generate SSH key inside the container
	genCmd := []string{"ssh-keygen", "-t", "rsa", "-b", "4096",
		"-f", config.ClusterKeyPath, "-N", "", "-C", "cluster-key"}
	if err := n.podman.ContainerExecQuiet(ctx, n.ContainerName, genCmd); err != nil {
		return fmt.Errorf("generating SSH key: %w", err)
	}

	// Set correct permissions on private key (SSH requires 600)
	chmodCmd := []string{"chmod", "600", config.ClusterKeyPath}
	if err := n.podman.ContainerExecQuiet(ctx, n.ContainerName, chmodCmd); err != nil {
		return fmt.Errorf("setting key permissions: %w", err)
	}

	logrus.Infof("Cluster SSH key created at %s", config.ClusterKeyPath)
	return nil
}

func (n *Node) createOverlayDisk(ctx context.Context) error {
	overlayPath := fmt.Sprintf("/workspace/%s.qcow2", n.Name)

	logrus.Infof("Creating overlay disk for %s", n.Name)

	opts := &virsh.QemuImgCreateOptions{
		Path:          overlayPath,
		Format:        "qcow2",
		BackingFile:   n.BaseDisk,
		BackingFormat: "qcow2",
	}

	if err := n.virsh.QemuImgCreate(ctx, opts); err != nil {
		return fmt.Errorf("creating overlay disk: %w", err)
	}

	logrus.Infof("Overlay disk created at %s", overlayPath)
	return nil
}

func (n *Node) createVM(ctx context.Context) error {
	logrus.Infof("Creating VM %s", n.Name)

	overlayDisk := fmt.Sprintf("path=/workspace/%s.qcow2,format=qcow2,bus=virtio", n.Name)
	isoPath := fmt.Sprintf("path=/workspace/%s-cloud-init.iso,device=cdrom", n.Name)

	opts := &virsh.VirtInstallOptions{
		Name:   n.Name,
		Memory: n.Memory,
		VCPUs:  n.VCPUs,
		Disks:  []string{overlayDisk, isoPath},
		Networks: []virsh.NetworkConfig{
			{
				Type:        "passt",
				Model:       "virtio",
				PortForward: "2222:22",
			},
			{
				Type:  "mcast",
				Model: "virtio",
				MAC:   n.ClusterMAC,
			},
		},
		Filesystems: []virsh.FilesystemConfig{
			{
				Source:     config.VirtiofsSharedDir,
				Target:     "cluster_images",
				AccessMode: "passthrough",
				ReadOnly:   false,
			},
		},
		XMLModifications: []string{
			"xpath.set=./devices/interface[2]/source/@address=" + config.MulticastAddr,
			fmt.Sprintf("xpath.set=./devices/interface[2]/source/@port=%d", config.MulticastPort),
			"xpath.set=./devices/filesystem/source/@socket=" + config.VirtiofsSocketPath,
		},
	}

	if n.IsControlPlane {
		opts.XMLModifications = append(opts.XMLModifications,
			"xpath.create=./devices/interface[1]/portForward/range",
			"xpath.set=./devices/interface[1]/portForward/range[2]/@start=6443",
			"xpath.set=./devices/interface[1]/portForward/range[2]/@to=6443",
		)
	}

	if err := n.virsh.VirtInstall(ctx, opts); err != nil {
		return fmt.Errorf("creating VM with virt-install: %w", err)
	}

	logrus.Infof("VM %s created with dual-NIC networking", n.Name)
	logrus.Infof("  NIC 1 (enp1s0): passt - internet access")
	logrus.Infof("  NIC 2 (enp2s0): %s - cluster communication", n.ClusterIP)

	return nil
}
