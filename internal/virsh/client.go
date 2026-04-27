package virsh

import (
	"context"
	"fmt"
	"strings"

	"github.com/bootc-dev/bink/internal/podman"
	"github.com/sirupsen/logrus"
)

type Client struct {
	containerName string
	podmanClient  *podman.Client
}

func NewClient(containerName string, podmanClient *podman.Client) *Client {
	return &Client{
		containerName: containerName,
		podmanClient:  podmanClient,
	}
}

func (c *Client) ExecInContainer(ctx context.Context, args ...string) (string, error) {
	return c.podmanClient.ContainerExec(ctx, c.containerName, args)
}

func (c *Client) VirtInstall(ctx context.Context, opts *VirtInstallOptions) error {
	args := []string{
		"virt-install",
		"--connect", "qemu:///session",
		"--name", opts.Name,
		"--memory", fmt.Sprintf("%d", opts.Memory),
		"--vcpus", fmt.Sprintf("%d", opts.VCPUs),
		"--import",
		"--os-variant", "fedora-unknown",
		"--graphics", "none",
		"--console", "pty,target_type=serial",
		"--noautoconsole",
	}

	// Add shared memory support if filesystems are present (required for virtiofs)
	if len(opts.Filesystems) > 0 {
		args = append(args, "--memorybacking", "source.type=memfd,access.mode=shared")
	}

	for _, disk := range opts.Disks {
		args = append(args, "--disk", disk)
	}

	for _, network := range opts.Networks {
		netArg := network.Type
		if network.Model != "" {
			netArg += fmt.Sprintf(",model=%s", network.Model)
		}
		if network.MAC != "" {
			netArg += fmt.Sprintf(",mac=%s", network.MAC)
		}
		if network.PortForward != "" {
			netArg += fmt.Sprintf(",portForward=%s", network.PortForward)
		}
		args = append(args, "--network", netArg)
	}

	for _, fs := range opts.Filesystems {
		// Build filesystem argument for virt-install
		// Explicitly specify virtiofs driver
		fsArg := fmt.Sprintf("source.dir=%s,target.dir=%s,driver.type=virtiofs",
			fs.Source, fs.Target)

		if fs.ReadOnly {
			fsArg += ",readonly=on"
		}

		args = append(args, "--filesystem", fsArg)
	}

	for _, xml := range opts.XMLModifications {
		args = append(args, "--xml", xml)
	}

	args = append(args, "--channel", "unix,target.type=virtio,target.name=org.qemu.guest_agent.0")

	logrus.Debugf("Creating VM with virt-install: %s", strings.Join(args, " "))

	return c.podmanClient.ContainerExecQuiet(ctx, c.containerName, args)
}

func (c *Client) QemuImgCreate(ctx context.Context, opts *QemuImgCreateOptions) error {
	args := []string{
		"qemu-img", "create",
		"-f", opts.Format,
	}

	if opts.BackingFile != "" {
		args = append(args, "-F", opts.BackingFormat, "-b", opts.BackingFile)
	}

	args = append(args, opts.Path)

	if opts.Size != "" {
		args = append(args, opts.Size)
	}

	logrus.Debugf("Creating disk image: qemu-img %s", strings.Join(args, " "))

	return c.podmanClient.ContainerExecQuiet(ctx, c.containerName, args)
}

func (c *Client) Genisoimage(ctx context.Context, outputPath, volumeID string, files []string) error {
	args := []string{
		"genisoimage",
		"-output", outputPath,
		"-volid", volumeID,
		"-joliet",
		"-rock",
	}

	args = append(args, files...)

	logrus.Debugf("Creating ISO: genisoimage %s", strings.Join(args, " "))

	return c.podmanClient.ContainerExecQuiet(ctx, c.containerName, args)
}

