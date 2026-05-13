package node

import (
	"strings"
	"testing"

	"github.com/bootc-dev/bink/internal/config"
	"gopkg.in/yaml.v3"
)

func testCloudInitData() CloudInitData {
	return CloudInitData{
		NodeName:         "node1",
		ClusterIP:        "10.0.0.83",
		DNSIP:            "10.88.0.3",
		SSHUser:          config.DefaultSSHUser,
		SSHPubKey:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest test@host",
		ClusterDomain:    config.ClusterDomain,
		UpstreamDNS1:     config.UpstreamDNS1,
		UpstreamDNS2:     config.UpstreamDNS2,
		RegistryStaticIP: config.RegistryStaticIP,
		RegistryPort:     config.RegistryPort,
		RegistryHostname: config.RegistryHostname,
		ServiceCIDR:      config.ServiceCIDR,
	}
}

func TestRenderTemplate_MetaData(t *testing.T) {
	data := testCloudInitData()
	content, err := renderTemplate("meta-data.yaml.tmpl", data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	var doc map[string]interface{}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("rendered meta-data is not valid YAML: %v", err)
	}

	if doc["instance-id"] != "node1" {
		t.Errorf("instance-id: got %v, want node1", doc["instance-id"])
	}
	if doc["local-hostname"] != "node1" {
		t.Errorf("local-hostname: got %v, want node1", doc["local-hostname"])
	}
}

func TestRenderTemplate_NetworkConfig(t *testing.T) {
	data := testCloudInitData()
	content, err := renderTemplate("network-config.yaml.tmpl", data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	var doc map[string]interface{}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("rendered network-config is not valid YAML: %v", err)
	}

	if doc["version"] != 2 {
		t.Errorf("version: got %v, want 2", doc["version"])
	}
	if _, ok := doc["ethernets"]; !ok {
		t.Error("missing ethernets key")
	}

	s := string(content)
	if !strings.Contains(s, "10.0.0.83/24") {
		t.Error("missing cluster IP in addresses")
	}
	if !strings.Contains(s, config.ServiceCIDR) {
		t.Error("missing service CIDR in routes")
	}
	if !strings.Contains(s, "10.88.0.3") {
		t.Error("missing DNS IP in nameservers")
	}
}

func TestRenderTemplate_UserData(t *testing.T) {
	data := testCloudInitData()
	content, err := renderTemplate("user-data.yaml.tmpl", data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if !strings.HasPrefix(string(content), "#cloud-config\n") {
		t.Error("user-data must start with #cloud-config")
	}

	raw := strings.TrimPrefix(string(content), "#cloud-config\n")
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("rendered user-data is not valid YAML: %v", err)
	}

	if doc["hostname"] != "node1" {
		t.Errorf("hostname: got %v, want node1", doc["hostname"])
	}
	for _, key := range []string{"users", "write_files", "runcmd"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("missing required key %q", key)
		}
	}

	s := string(content)
	if !strings.Contains(s, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest test@host") {
		t.Error("missing SSH public key")
	}
	if !strings.Contains(s, "--node-ip=10.0.0.83") {
		t.Error("missing node IP in kubelet args")
	}

	registryURL := config.RegistryStaticIP + ":5000"
	if !strings.Contains(s, registryURL) {
		t.Errorf("missing registry URL %s", registryURL)
	}
}

func TestValidateYAML(t *testing.T) {
	data := testCloudInitData()

	templates := []string{
		"meta-data.yaml.tmpl",
		"network-config.yaml.tmpl",
		"user-data.yaml.tmpl",
	}

	for _, tmpl := range templates {
		t.Run(tmpl, func(t *testing.T) {
			content, err := renderTemplate(tmpl, data)
			if err != nil {
				t.Fatalf("render failed: %v", err)
			}
			if err := validateYAML(content, tmpl); err != nil {
				t.Errorf("validation failed: %v", err)
			}
		})
	}
}

func TestValidateYAML_InvalidYAML(t *testing.T) {
	err := validateYAML([]byte(":\n  :\n[broken"), "meta-data.yaml.tmpl")
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestValidateYAML_MissingRequiredKeys(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    string
		content string
		wantErr string
	}{
		{
			"user-data missing hostname",
			"user-data.yaml.tmpl",
			"#cloud-config\nusers: []\nwrite_files: []\nruncmd: []",
			"hostname",
		},
		{
			"user-data missing runcmd",
			"user-data.yaml.tmpl",
			"#cloud-config\nhostname: x\nusers: []\nwrite_files: []",
			"runcmd",
		},
		{
			"network-config missing version",
			"network-config.yaml.tmpl",
			"ethernets: {}",
			"version",
		},
		{
			"network-config missing ethernets",
			"network-config.yaml.tmpl",
			"version: 2",
			"ethernets",
		},
		{
			"meta-data missing instance-id",
			"meta-data.yaml.tmpl",
			"local-hostname: x",
			"instance-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateYAML([]byte(tt.content), tt.tmpl)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got %q, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateYAML_NotAMapping(t *testing.T) {
	err := validateYAML([]byte("- item1\n- item2"), "meta-data.yaml.tmpl")
	if err == nil {
		t.Error("expected error for non-mapping YAML")
	}
	if !strings.Contains(err.Error(), "expected a mapping") {
		t.Errorf("got %q, want error about mapping", err)
	}
}
