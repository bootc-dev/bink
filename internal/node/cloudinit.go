package node

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var cloudInitTemplates = template.Must(
	template.ParseFS(templateFS, "templates/*.tmpl"),
)

type CloudInitData struct {
	NodeName         string
	ClusterIP        string
	Node1IP          string
	IsNode1          bool
	SSHUser          string
	SSHPubKey        string
	ClusterDomain    string
	UpstreamDNS1     string
	UpstreamDNS2     string
	RegistryStaticIP string
	RegistryPort     int
	RegistryHostname string
}

func (n *Node) newCloudInitData(sshPubKey string) CloudInitData {
	return CloudInitData{
		NodeName:         n.Name,
		ClusterIP:        n.ClusterIP,
		Node1IP:          CalculateClusterIP(n.ClusterName, "node1"),
		IsNode1:          n.Name == "node1",
		SSHUser:          config.DefaultSSHUser,
		SSHPubKey:        strings.TrimSpace(sshPubKey),
		ClusterDomain:    config.ClusterDomain,
		UpstreamDNS1:     config.UpstreamDNS1,
		UpstreamDNS2:     config.UpstreamDNS2,
		RegistryStaticIP: config.RegistryStaticIP,
		RegistryPort:     config.RegistryPort,
		RegistryHostname: config.RegistryHostname,
	}
}

func renderTemplate(name string, data CloudInitData) ([]byte, error) {
	var buf bytes.Buffer
	if err := cloudInitTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, fmt.Errorf("rendering template %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

func validateYAML(content []byte, name string) error {
	raw := content
	if name == "user-data.yaml.tmpl" {
		raw = bytes.TrimPrefix(raw, []byte("#cloud-config\n"))
	}

	var doc interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("invalid YAML in %s: %w", name, err)
	}

	m, ok := doc.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid YAML in %s: expected a mapping at top level", name)
	}

	switch name {
	case "user-data.yaml.tmpl":
		for _, key := range []string{"hostname", "users", "write_files", "runcmd"} {
			if _, exists := m[key]; !exists {
				return fmt.Errorf("invalid cloud-config: missing required key %q", key)
			}
		}
	case "network-config.yaml.tmpl":
		if _, exists := m["version"]; !exists {
			return fmt.Errorf("invalid network-config: missing required key %q", "version")
		}
		if _, exists := m["ethernets"]; !exists {
			return fmt.Errorf("invalid network-config: missing required key %q", "ethernets")
		}
	case "meta-data.yaml.tmpl":
		if _, exists := m["instance-id"]; !exists {
			return fmt.Errorf("invalid meta-data: missing required key %q", "instance-id")
		}
	}

	return nil
}

func (n *Node) generateCloudInit(ctx context.Context) error {
	logrus.Infof("Creating cloud-init ISO for %s", n.Name)

	tmpDir, err := os.MkdirTemp("", "cloud-init-*")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pubKey, err := n.podman.ContainerExec(ctx, n.ContainerName, []string{"cat", config.ClusterKeyPubPath})
	if err != nil {
		return fmt.Errorf("reading public key: %w", err)
	}

	data := n.newCloudInitData(string(pubKey))

	type templateFile struct {
		tmpl     string
		filename string
	}
	files := []templateFile{
		{"meta-data.yaml.tmpl", "meta-data"},
		{"network-config.yaml.tmpl", "network-config"},
		{"user-data.yaml.tmpl", "user-data"},
	}

	for _, f := range files {
		content, err := renderTemplate(f.tmpl, data)
		if err != nil {
			return err
		}
		if err := validateYAML(content, f.tmpl); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmpDir, f.filename), content, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", f.filename, err)
		}
	}

	isoPath := fmt.Sprintf("/workspace/%s-cloud-init.iso", n.Name)
	containerFiles := []string{"/tmp/meta-data", "/tmp/user-data", "/tmp/network-config"}

	for _, f := range files {
		src := filepath.Join(tmpDir, f.filename)
		dst := "/tmp/" + f.filename
		if err := n.podman.ContainerCopy(ctx, src, n.ContainerName, dst); err != nil {
			return fmt.Errorf("copying %s: %w", f.filename, err)
		}
	}

	if err := n.virsh.Genisoimage(ctx, isoPath, config.CloudInitVolID, containerFiles); err != nil {
		return fmt.Errorf("creating ISO: %w", err)
	}

	logrus.Infof("Cloud-init ISO created at %s", isoPath)
	return nil
}
