package haproxy

import (
	"context"
	"fmt"
	"strings"
	"text/template"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
	"github.com/containers/podman/v5/libpod/define"
	"github.com/sirupsen/logrus"
	nettypes "github.com/containers/common/libnetwork/types"
)

var configTemplate = template.Must(template.New("haproxy.cfg").Parse(`global
    log stdout format raw local0
    stats socket /tmp/haproxy.sock mode 660 level admin

defaults
    mode tcp
    timeout connect 5s
    timeout client 30s
    timeout server 30s
    option tcplog

frontend k8s-api
    bind *:{{.FrontendPort}}
    default_backend k8s-control-plane

backend k8s-control-plane
    balance roundrobin
    option httpchk
    http-check connect ssl alpn h2,http/1.1
    http-check send meth GET uri /healthz
    http-check expect status 200
    default-server inter 3s fall 3 rise 2
{{- range .Backends}}
    server {{.Name}} {{.Address}}:{{.Port}} check verify none
{{- end}}
`))

type backend struct {
	Name    string
	Address string
	Port    int
}

type configParams struct {
	FrontendPort int
	Backends     []backend
}

type Manager struct {
	podman      *podman.Client
	clusterName string
}

func NewManager(clusterName string) (*Manager, error) {
	client, err := podman.NewClient()
	if err != nil {
		return nil, fmt.Errorf("creating podman client: %w", err)
	}
	return &Manager{
		podman:      client,
		clusterName: clusterName,
	}, nil
}

func (m *Manager) containerName() string {
	return fmt.Sprintf("%s%s-%s", config.ContainerNamePrefix, m.clusterName, config.HAProxyContainerName)
}

func (m *Manager) networkName() string {
	if m.clusterName == "" {
		return config.DefaultNetworkName
	}
	return m.clusterName
}

// EnsureHAProxy creates or updates the HAProxy container for the cluster.
func (m *Manager) EnsureHAProxy(ctx context.Context, apiPort int) error {
	name := m.containerName()

	logrus.Info("Ensuring HAProxy load balancer is running")

	exists, err := m.podman.ContainerExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking HAProxy container: %w", err)
	}

	if exists {
		status, err := m.podman.ContainerStatus(ctx, name)
		if err != nil {
			return fmt.Errorf("checking HAProxy status: %w", err)
		}

		switch status {
		case define.ContainerStateRunning.String():
			logrus.Info("HAProxy already running, updating config")
			return m.updateConfig(ctx)
		default:
			logrus.Infof("HAProxy container exists but is %s, removing and recreating", status)
			if err := m.podman.ContainerRemove(ctx, name, true); err != nil {
				return fmt.Errorf("removing stale HAProxy container: %w", err)
			}
		}
	}

	return m.createContainer(ctx, apiPort)
}

// UpdateConfig regenerates the HAProxy config from current control-plane nodes
// by recreating the container.
func (m *Manager) UpdateConfig(ctx context.Context) error {
	return m.updateConfig(ctx)
}

func (m *Manager) updateConfig(ctx context.Context) error {
	name := m.containerName()

	// Get the current published port before removing
	apiPort := 0
	if port, err := m.GetPublishedPort(ctx); err == nil {
		apiPort = port
	}

	// Remove and recreate with updated config
	if err := m.podman.ContainerStop(ctx, name); err != nil {
		logrus.Warnf("Failed to stop HAProxy: %v", err)
	}
	if err := m.podman.ContainerRemove(ctx, name, true); err != nil {
		return fmt.Errorf("removing HAProxy container: %w", err)
	}

	return m.createContainer(ctx, apiPort)
}

// createContainer discovers backends, renders config, and creates the HAProxy container.
func (m *Manager) createContainer(ctx context.Context, apiPort int) error {
	name := m.containerName()

	backends, err := m.discoverBackends(ctx)
	if err != nil {
		return fmt.Errorf("discovering control-plane backends: %w", err)
	}

	if len(backends) == 0 {
		return fmt.Errorf("no control-plane nodes found for cluster %s", m.clusterName)
	}

	cfg, err := m.renderConfig(backends)
	if err != nil {
		return fmt.Errorf("rendering HAProxy config: %w", err)
	}

	// Write config and exec haproxy in a single command so the container
	// doesn't crash before the config is in place.
	cmd := []string{
		"sh", "-c",
		fmt.Sprintf("cat > %s << 'HAPROXYCFG'\n%sHAPROXYCFG\nexec haproxy -f %s",
			config.HAProxyConfigPath, cfg, config.HAProxyConfigPath),
	}

	opts := &podman.ContainerCreateOptions{
		Name:    name,
		Image:   config.HAProxyImage,
		Command: cmd,
		Network: m.networkName(),
		PortMappings: []nettypes.PortMapping{
			{
				HostPort:      uint16(apiPort),
				ContainerPort: uint16(config.HAProxyPort),
				Protocol:      "tcp",
			},
		},
		Labels: map[string]string{
			"bink.cluster-name": m.clusterName,
			"bink.component":    "haproxy",
		},
	}

	containerID, err := m.podman.ContainerCreate(ctx, opts)
	if err != nil {
		return fmt.Errorf("creating HAProxy container: %w", err)
	}

	logrus.Infof("HAProxy container created: %s", containerID)
	logrus.Infof("HAProxy load balancer ready: %s", name)
	return nil
}

// GetPublishedPort returns the host port mapped to the HAProxy frontend.
func (m *Manager) GetPublishedPort(ctx context.Context) (int, error) {
	return m.podman.GetPublishedPort(ctx, m.containerName(), fmt.Sprintf("%d/tcp", config.HAProxyPort))
}

// discoverBackends finds all control-plane node containers in this cluster
// and returns their bridge IPs as backends.
func (m *Manager) discoverBackends(ctx context.Context) ([]backend, error) {
	filter := fmt.Sprintf("label=bink.cluster-name=%s", m.clusterName)
	containers, err := m.podman.ContainerList(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("listing cluster containers: %w", err)
	}

	var backends []backend
	for _, containerName := range containers {
		// Skip non-node containers (haproxy, etc.)
		component, _ := m.podman.ContainerInspect(ctx, containerName, "{{index .Config.Labels \"bink.component\"}}")
		if component != "" {
			continue
		}

		// Check if this is a control-plane node by looking for published port 6443
		_, err := m.podman.GetPublishedPort(ctx, containerName, "6443/tcp")
		if err != nil {
			continue
		}

		ip, err := m.podman.ContainerInspect(ctx, containerName, "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}")
		if err != nil {
			logrus.Warnf("Failed to get IP for %s, skipping: %v", containerName, err)
			continue
		}

		nodeName, _ := m.podman.ContainerInspect(ctx, containerName, "{{index .Config.Labels \"bink.node-name\"}}")
		if nodeName == "" {
			nodeName = containerName
		}

		backends = append(backends, backend{
			Name:    nodeName,
			Address: ip,
			Port:    config.DefaultAPIServerPort,
		})

		logrus.Infof("Discovered control-plane backend: %s (%s:%d)", nodeName, ip, config.DefaultAPIServerPort)
	}

	return backends, nil
}

func (m *Manager) renderConfig(backends []backend) (string, error) {
	params := configParams{
		FrontendPort: config.HAProxyPort,
		Backends:     backends,
	}

	var buf strings.Builder
	if err := configTemplate.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return buf.String(), nil
}
