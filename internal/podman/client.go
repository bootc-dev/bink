package podman

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containers/podman/v6/pkg/api/handlers"
	"github.com/containers/podman/v6/pkg/bindings"
	"github.com/containers/podman/v6/pkg/bindings/containers"
	"github.com/containers/podman/v6/pkg/bindings/images"
	"github.com/containers/podman/v6/pkg/bindings/network"
	"github.com/containers/podman/v6/pkg/bindings/volumes"
	"github.com/containers/podman/v6/pkg/domain/entities"
	"github.com/containers/podman/v6/pkg/specgen"
	"github.com/sirupsen/logrus"
	nettypes "go.podman.io/common/libnetwork/types"
)

type Client struct {
	conn       context.Context
	socketPath string
	connected  bool
}

type ClientOption func(*Client)

func WithSocketPath(path string) ClientOption {
	return func(c *Client) {
		c.socketPath = path
	}
}

func NewClient(opts ...ClientOption) (*Client, error) {
	// Check for CONTAINER_HOST environment variable first (like podman CLI does)
	defaultSocketPath := os.Getenv("CONTAINER_HOST")
	if defaultSocketPath == "" {
		uid := os.Getuid()
		defaultSocketPath = fmt.Sprintf("unix:///run/user/%d/podman/podman.sock", uid)
	}

	c := &Client{
		socketPath: defaultSocketPath,
		connected:  false,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

func (c *Client) ensureConnection() error {
	if c.connected {
		return nil
	}

	conn, err := bindings.NewConnection(context.Background(), c.socketPath)
	if err != nil {
		return fmt.Errorf("connecting to Podman service at %s: %w", c.socketPath, err)
	}

	c.conn = conn
	c.connected = true
	logrus.Debugf("Connected to Podman service at %s", c.socketPath)
	return nil
}

func (c *Client) NetworkExists(ctx context.Context, name string) (bool, error) {
	if err := c.ensureConnection(); err != nil {
		return false, err
	}

	_, err := network.Inspect(c.conn, name, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no such network") || strings.Contains(err.Error(), "network not found") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *Client) NetworkCreate(ctx context.Context, name, subnet string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	logrus.Infof("Creating podman network '%s' with subnet %s", name, subnet)

	net := &nettypes.Network{
		Name: name,
	}

	if subnet != "" {
		ipnet, err := nettypes.ParseCIDR(subnet)
		if err != nil {
			return fmt.Errorf("parsing subnet %s: %w", subnet, err)
		}
		net.Subnets = []nettypes.Subnet{{Subnet: ipnet}}
	}

	_, err := network.Create(c.conn, net)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			logrus.Infof("Network '%s' already exists", name)
			return nil
		}
		if strings.Contains(err.Error(), "subnet") && strings.Contains(err.Error(), "use") {
			logrus.Warnf("Subnet %s already in use, creating network with auto-assigned subnet", subnet)
			net.Subnets = nil
			_, err = network.Create(c.conn, net)
			if err != nil {
				return fmt.Errorf("creating network with auto-assigned subnet: %w", err)
			}
		} else {
			return fmt.Errorf("creating network: %w", err)
		}
	}

	logrus.Infof("Network '%s' created successfully", name)
	return nil
}

func (c *Client) NetworkInspect(ctx context.Context, name, format string) (string, error) {
	if err := c.ensureConnection(); err != nil {
		return "", err
	}

	report, err := network.Inspect(c.conn, name, nil)
	if err != nil {
		return "", err
	}

	// Handle format strings
	switch format {
	case "{{range .Subnets}}{{.Subnet}}{{end}}":
		if len(report.Subnets) > 0 {
			return report.Subnets[0].Subnet.String(), nil
		}
		return "", nil
	default:
		return "", fmt.Errorf("unsupported format string: %s", format)
	}
}

func (c *Client) NetworkRemove(ctx context.Context, name string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	logrus.Debugf("Removing network '%s'", name)

	_, err := network.Remove(c.conn, name, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no such network") || strings.Contains(err.Error(), "network not found") {
			logrus.Debugf("Network '%s' does not exist", name)
			return nil
		}
		return fmt.Errorf("removing network: %w", err)
	}

	logrus.Infof("Network '%s' removed successfully", name)
	return nil
}

func (c *Client) ContainerExists(ctx context.Context, name string) (bool, error) {
	if err := c.ensureConnection(); err != nil {
		return false, err
	}

	exists, err := containers.Exists(c.conn, name, nil)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (c *Client) ContainerCreate(ctx context.Context, opts *ContainerCreateOptions) (string, error) {
	if err := c.ensureConnection(); err != nil {
		return "", err
	}

	logrus.Debugf("Creating container %s from image %s", opts.Name, opts.Image)

	// Create spec generator
	spec := specgen.NewSpecGenerator(opts.Image, false)
	spec.Name = opts.Name
	spec.Command = opts.Command
	spec.Env = opts.Environment
	spec.Labels = opts.Labels
	spec.CapAdd = opts.CapAdd
	spec.SelinuxOpts = opts.SelinuxOpts
	spec.Devices = opts.Devices
	spec.Volumes = opts.Volumes
	spec.Mounts = opts.Mounts
	spec.ImageVolumes = opts.ImageVolumes
	spec.PortMappings = opts.PortMappings

	// Configure network
	if opts.NetworkOptions != nil {
		spec.NetNS = specgen.Namespace{
			NSMode: specgen.Bridge,
		}
		spec.Networks = opts.NetworkOptions
	} else if opts.Network != "" {
		spec.NetNS = specgen.Namespace{
			NSMode: specgen.Bridge,
		}
		spec.Networks = map[string]nettypes.PerNetworkOptions{
			opts.Network: {},
		}
	}

	// Create the container
	createResponse, err := containers.CreateWithSpec(c.conn, spec, nil)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}

	// Start the container
	if err := containers.Start(c.conn, createResponse.ID, nil); err != nil {
		return "", fmt.Errorf("starting container: %w", err)
	}

	logrus.Debugf("Container %s created and started with ID %s", opts.Name, createResponse.ID)
	return createResponse.ID, nil
}

func (c *Client) ContainerStatus(ctx context.Context, name string) (string, error) {
	if err := c.ensureConnection(); err != nil {
		return "", err
	}

	data, err := containers.Inspect(c.conn, name, nil)
	if err != nil {
		return "", fmt.Errorf("inspecting container %s: %w", name, err)
	}

	return data.State.Status, nil
}

func (c *Client) ContainerStart(ctx context.Context, name string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	logrus.Debugf("Starting container %s", name)

	if err := containers.Start(c.conn, name, nil); err != nil {
		return fmt.Errorf("starting container %s: %w", name, err)
	}

	return nil
}

func (c *Client) ContainerExec(ctx context.Context, name string, cmd []string) (string, error) {
	if err := c.ensureConnection(); err != nil {
		return "", err
	}

	execConfig := &handlers.ExecCreateConfig{}
	execConfig.Cmd = cmd
	execConfig.AttachStdout = true
	execConfig.AttachStderr = true

	sessionID, err := containers.ExecCreate(c.conn, name, execConfig)
	if err != nil {
		return "", fmt.Errorf("creating exec session: %w", err)
	}

	var stdout, stderr bytes.Buffer
	startOptions := new(containers.ExecStartAndAttachOptions).
		WithOutputStream(&stdout).
		WithErrorStream(&stderr).
		WithAttachOutput(true).
		WithAttachError(true)

	if err := containers.ExecStartAndAttach(c.conn, sessionID, startOptions); err != nil {
		return "", fmt.Errorf("executing command: %w", err)
	}

	inspectData, err := containers.ExecInspect(c.conn, sessionID, nil)
	if err != nil {
		return "", fmt.Errorf("inspecting exec session: %w", err)
	}

	if inspectData.ExitCode != 0 {
		return "", fmt.Errorf("command exited with code %d: %s", inspectData.ExitCode, stderr.String())
	}

	return stdout.String(), nil
}

func (c *Client) ContainerExecQuiet(ctx context.Context, name string, cmd []string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	execConfig := &handlers.ExecCreateConfig{}
	execConfig.Cmd = cmd
	execConfig.AttachStdout = true
	execConfig.AttachStderr = true

	sessionID, err := containers.ExecCreate(c.conn, name, execConfig)
	if err != nil {
		return fmt.Errorf("creating exec session: %w", err)
	}

	var stdout, stderr bytes.Buffer
	startOptions := new(containers.ExecStartAndAttachOptions).
		WithOutputStream(&stdout).
		WithErrorStream(&stderr).
		WithAttachOutput(true).
		WithAttachError(true)

	if err := containers.ExecStartAndAttach(c.conn, sessionID, startOptions); err != nil {
		return fmt.Errorf("executing command: %w", err)
	}

	inspectData, err := containers.ExecInspect(c.conn, sessionID, nil)
	if err != nil {
		return fmt.Errorf("inspecting exec session: %w", err)
	}

	if inspectData.ExitCode != 0 {
		return fmt.Errorf("command exited with code %d: %s", inspectData.ExitCode, stderr.String())
	}

	return nil
}

func (c *Client) ContainerExecInteractive(ctx context.Context, name string, cmd []string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	logrus.Debugf("Executing interactively: %s %s", name, strings.Join(cmd, " "))

	execConfig := &handlers.ExecCreateConfig{}
	execConfig.Cmd = cmd
	execConfig.AttachStdout = true
	execConfig.AttachStderr = true
	execConfig.AttachStdin = true
	execConfig.Tty = true

	sessionID, err := containers.ExecCreate(c.conn, name, execConfig)
	if err != nil {
		return fmt.Errorf("creating exec session: %w", err)
	}

	stdin := bufio.NewReader(os.Stdin)
	startOptions := new(containers.ExecStartAndAttachOptions).
		WithOutputStream(os.Stdout).
		WithErrorStream(os.Stderr).
		WithInputStream(*stdin).
		WithAttachOutput(true).
		WithAttachError(true).
		WithAttachInput(true)

	if err := containers.ExecStartAndAttach(c.conn, sessionID, startOptions); err != nil {
		return fmt.Errorf("executing command: %w", err)
	}

	return nil
}

func (c *Client) ContainerStop(ctx context.Context, name string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	logrus.Debugf("Stopping container %s", name)

	timeout := uint(10)
	opts := new(containers.StopOptions).WithTimeout(timeout)

	if err := containers.Stop(c.conn, name, opts); err != nil {
		return fmt.Errorf("stopping container %s: %w", name, err)
	}

	return nil
}

func (c *Client) ContainerRemove(ctx context.Context, name string, force bool) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	logrus.Debugf("Removing container %s", name)

	opts := new(containers.RemoveOptions).WithForce(force)

	if _, err := containers.Remove(c.conn, name, opts); err != nil {
		return fmt.Errorf("removing container %s: %w", name, err)
	}

	return nil
}

func (c *Client) ContainerList(ctx context.Context, filter string) ([]string, error) {
	if err := c.ensureConnection(); err != nil {
		return nil, err
	}

	opts := new(containers.ListOptions).WithAll(true)
	if filter != "" {
		// Parse filter format "key=value" into map
		parts := strings.SplitN(filter, "=", 2)
		if len(parts) == 2 {
			opts.WithFilters(map[string][]string{
				parts[0]: {parts[1]},
			})
		}
	}

	containerList, err := containers.List(c.conn, opts)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(containerList))
	for _, container := range containerList {
		if len(container.Names) > 0 {
			names = append(names, container.Names[0])
		}
	}

	return names, nil
}

func (c *Client) ContainerInspect(ctx context.Context, name, format string) (string, error) {
	if err := c.ensureConnection(); err != nil {
		return "", err
	}

	data, err := containers.Inspect(c.conn, name, nil)
	if err != nil {
		return "", err
	}

	// Handle common format strings
	switch format {
	case "{{.ID}}":
		return data.ID, nil
	case "{{.State.Status}}":
		return data.State.Status, nil
	case "{{.Created}}":
		return data.Created.String(), nil
	case "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}":
		for _, network := range data.NetworkSettings.Networks {
			if network.IPAddress != "" {
				return network.IPAddress, nil
			}
		}
		return "", nil
	case "{{json .NetworkSettings.Ports}}":
		// Return port mappings as comma-separated strings like "6443/tcp->0.0.0.0:12345"
		var ports []string
		for containerPort, bindings := range data.NetworkSettings.Ports {
			for _, binding := range bindings {
				ports = append(ports, fmt.Sprintf("%s->%s:%s", containerPort, binding.HostIP, binding.HostPort))
			}
		}
		return strings.Join(ports, ","), nil
	case "{{index .Config.Labels \"bink.cluster-name\"}}":
		if name, ok := data.Config.Labels["bink.cluster-name"]; ok {
			return name, nil
		}
		return "", nil
	case "{{index .Config.Labels \"bink.node-name\"}}":
		if name, ok := data.Config.Labels["bink.node-name"]; ok {
			return name, nil
		}
		return "", nil
	default:
		return "", fmt.Errorf("unsupported format string: %s", format)
	}
}

func (c *Client) ContainerCopy(ctx context.Context, srcPath, containerName, destPath string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	// Read source file
	fileData, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("reading source file: %w", err)
	}

	// Get file info for tar header
	fileInfo, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stating source file: %w", err)
	}

	// Create tar archive in memory
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	header := &tar.Header{
		Name: filepath.Base(destPath),
		Mode: int64(fileInfo.Mode()),
		Size: int64(len(fileData)),
	}

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}

	if _, err := tw.Write(fileData); err != nil {
		return fmt.Errorf("writing tar data: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}

	// Copy to container using bindings
	copyFunc, err := containers.CopyFromArchive(c.conn, containerName, filepath.Dir(destPath), &buf)
	if err != nil {
		return fmt.Errorf("preparing copy: %w", err)
	}

	if err := copyFunc(); err != nil {
		return fmt.Errorf("copying to container: %w", err)
	}

	return nil
}

func (c *Client) VolumeRemove(ctx context.Context, name string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	logrus.Debugf("Removing volume %s", name)

	if err := volumes.Remove(c.conn, name, nil); err != nil {
		return fmt.Errorf("removing volume %s: %w", name, err)
	}

	return nil
}

func (c *Client) VolumeExists(ctx context.Context, name string) (bool, error) {
	if err := c.ensureConnection(); err != nil {
		return false, err
	}

	_, err := volumes.Inspect(c.conn, name, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no such volume") || strings.Contains(err.Error(), "volume not found") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *Client) VolumeCreate(ctx context.Context, name string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	logrus.Infof("Creating volume '%s'", name)

	opts := entities.VolumeCreateOptions{
		Name: name,
	}
	_, err := volumes.Create(c.conn, opts, nil)
	if err != nil {
		if strings.Contains(err.Error(), "volume already exists") {
			logrus.Debugf("Volume %s already exists (created by parallel process)", name)
			return nil
		}
		return fmt.Errorf("creating volume: %w", err)
	}

	logrus.Infof("Volume '%s' created successfully", name)
	return nil
}

func (c *Client) VolumeList(ctx context.Context, filter string) ([]string, error) {
	if err := c.ensureConnection(); err != nil {
		return nil, err
	}

	opts := new(volumes.ListOptions)
	if filter != "" {
		parts := strings.SplitN(filter, "=", 2)
		if len(parts) == 2 {
			opts.WithFilters(map[string][]string{
				parts[0]: {parts[1]},
			})
		}
	}

	volumeList, err := volumes.List(c.conn, opts)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(volumeList))
	for _, vol := range volumeList {
		names = append(names, vol.Name)
	}

	return names, nil
}

func (c *Client) ImageExists(ctx context.Context, name string) (bool, error) {
	if err := c.ensureConnection(); err != nil {
		return false, err
	}

	exists, err := images.Exists(c.conn, name, nil)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (c *Client) ContainerWait(ctx context.Context, name string) (int64, error) {
	if err := c.ensureConnection(); err != nil {
		return 0, err
	}

	logrus.Debugf("Waiting for container %s to exit...", name)

	exitCode, err := containers.Wait(c.conn, name, nil)
	if err != nil {
		return 0, fmt.Errorf("waiting for container: %w", err)
	}

	return int64(exitCode), nil
}

func (c *Client) ContainerRunQuiet(ctx context.Context, image string, cmd []string, volumeMounts []string) error {
	if err := c.ensureConnection(); err != nil {
		return err
	}

	spec := specgen.NewSpecGenerator(image, false)
	spec.Command = cmd
	remove := true
	spec.Remove = &remove

	for _, mount := range volumeMounts {
		parts := strings.SplitN(mount, ":", 3)
		if len(parts) >= 2 {
			spec.Volumes = append(spec.Volumes, &specgen.NamedVolume{
				Name: parts[0],
				Dest: parts[1],
			})
		}
	}

	createResponse, err := containers.CreateWithSpec(c.conn, spec, nil)
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}

	if err := containers.Start(c.conn, createResponse.ID, nil); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	exitCode, err := containers.Wait(c.conn, createResponse.ID, nil)
	if err != nil {
		return fmt.Errorf("waiting for container: %w", err)
	}

	if exitCode != 0 {
		return fmt.Errorf("container exited with code %d", exitCode)
	}

	return nil
}

func (c *Client) ContainerRunOutput(ctx context.Context, image string, cmd []string, volumeMounts []string) (string, error) {
	if err := c.ensureConnection(); err != nil {
		return "", err
	}

	spec := specgen.NewSpecGenerator(image, false)
	spec.Command = cmd
	remove := true
	spec.Remove = &remove

	for _, mount := range volumeMounts {
		parts := strings.SplitN(mount, ":", 3)
		if len(parts) >= 2 {
			spec.Volumes = append(spec.Volumes, &specgen.NamedVolume{
				Name: parts[0],
				Dest: parts[1],
			})
		}
	}

	createResponse, err := containers.CreateWithSpec(c.conn, spec, nil)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}

	if err := containers.Start(c.conn, createResponse.ID, nil); err != nil {
		return "", fmt.Errorf("starting container: %w", err)
	}

	exitCode, err := containers.Wait(c.conn, createResponse.ID, nil)
	if err != nil {
		return "", fmt.Errorf("waiting for container: %w", err)
	}

	stdoutChan := make(chan string, 1)
	stderrChan := make(chan string, 1)
	opts := new(containers.LogOptions)
	stdout := true
	opts.Stdout = &stdout

	go func() {
		containers.Logs(c.conn, createResponse.ID, opts, stdoutChan, stderrChan)
	}()

	var output strings.Builder
	for msg := range stdoutChan {
		output.WriteString(msg)
	}

	if exitCode != 0 {
		return "", fmt.Errorf("container exited with code %d", exitCode)
	}

	return output.String(), nil
}

func (c *Client) GetPublishedPort(ctx context.Context, containerName, containerPort string) (int, error) {
	if err := c.ensureConnection(); err != nil {
		return 0, err
	}

	data, err := containers.Inspect(c.conn, containerName, nil)
	if err != nil {
		return 0, fmt.Errorf("inspecting container: %w", err)
	}

	// Look up the published port for the given container port (already includes protocol like "6443/tcp")
	portMappings, ok := data.NetworkSettings.Ports[containerPort]
	if !ok || len(portMappings) == 0 {
		return 0, fmt.Errorf("no published port found for %s", containerPort)
	}

	port := 0
	_, err = fmt.Sscanf(portMappings[0].HostPort, "%d", &port)
	if err != nil {
		return 0, fmt.Errorf("parsing port number %q: %w", portMappings[0].HostPort, err)
	}

	return port, nil
}
