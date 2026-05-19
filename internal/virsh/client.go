package virsh

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bootc-dev/bink/internal/podman"
	"github.com/sirupsen/logrus"
	libvirt "libvirt.org/go/libvirt"
)

type Client struct {
	containerName string
	podmanClient  *podman.Client
	libvirtURI    string
	conn          *libvirt.Connect
}

func NewClient(containerName string, podmanClient *podman.Client) *Client {
	return &Client{
		containerName: containerName,
		podmanClient:  podmanClient,
	}
}

func (c *Client) SetLibvirtURI(uri string) {
	c.libvirtURI = uri
}

func (c *Client) connect(ctx context.Context) error {
	if c.conn != nil {
		alive, err := c.conn.IsAlive()
		if err == nil && alive {
			return nil
		}
		if _, err := c.conn.Close(); err != nil {
			logrus.Debugf("Closing stale libvirt connection: %v", err)
		}
		c.conn = nil
	}

	if c.libvirtURI == "" {
		return fmt.Errorf("libvirt URI not set")
	}

	var lastErr error
	backoff := 500 * time.Millisecond
	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		conn, err := libvirt.NewConnect(c.libvirtURI)
		if err == nil {
			c.conn = conn
			logrus.Debugf("Connected to libvirt at %s", c.libvirtURI)
			return nil
		}
		lastErr = err
		logrus.Debugf("Retrying libvirt connection to %s: %v", c.libvirtURI, err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}

	return fmt.Errorf("connecting to libvirt at %s after 30s: %w", c.libvirtURI, lastErr)
}

func (c *Client) Close() error {
	if c.conn != nil {
		_, err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

func (c *Client) DefineAndStartDomain(ctx context.Context, opts ...DomainOption) error {
	if err := c.connect(ctx); err != nil {
		return fmt.Errorf("connecting to libvirt: %w", err)
	}

	domain := NewDomain(opts...)
	xmlStr, err := domain.Marshal()
	if err != nil {
		return fmt.Errorf("building domain XML: %w", err)
	}

	logrus.Debugf("Defining domain with XML:\n%s", xmlStr)

	dom, err := c.conn.DomainDefineXML(xmlStr)
	if err != nil {
		return fmt.Errorf("defining domain: %w", err)
	}
	defer dom.Free()

	if err := dom.Create(); err != nil {
		return fmt.Errorf("starting domain: %w", err)
	}

	logrus.Infof("Domain %s defined and started via libvirt", domain.Name)
	return nil
}

func (c *Client) ExecInContainer(ctx context.Context, args ...string) (string, error) {
	return c.podmanClient.ContainerExec(ctx, c.containerName, args)
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
