package config

const (
	DefaultNetworkName = "podman"
	DefaultSubnet      = "10.88.0.0/16"

	DefaultClusterImage     = "localhost/cluster:latest"
	DefaultBootcImage       = "localhost/fedora-bootc-k8s:latest"
	DefaultBootcImagesImage = "localhost/fedora-bootc-k8s-image:latest"
	DefaultBaseImage        = "quay.io/fedora/fedora:43"
	DefaultPopulatorImage   = "localhost/cluster-images-populator:latest"

	DefaultBaseDisk = "/images/fedora-bootc-k8s.qcow2"
	DefaultMemory   = 8192
	DefaultVCPUs       = 4
	DefaultDiskSize    = "10G"

	DefaultSSHPort      = 2222
	DefaultSSHUser      = "core"
	ClusterKeyPath      = "/var/run/cluster/cluster.key"
	ClusterKeyPubPath   = "/var/run/cluster/cluster.key.pub"
	ClusterKeysHostPath = "./vm"

	MulticastAddr = "230.0.0.1"
	MulticastPort = 5558

	ClusterIPPrefix    = "10.0.0"
	ClusterIPMinSuffix = 10
	ClusterIPMaxSuffix = 250
	ClusterIPRangeSize = 240
	ClusterSubnet      = "10.0.0.0/24"
	ClusterMACPrefix   = "52:54:01"

	DefaultAPIServerPort = 6443

	KubernetesVersion = "v1.35.0"

	CalicoVersion      = "v3.27.0"
	DefaultCNIManifest = "https://raw.githubusercontent.com/projectcalico/calico/" + CalicoVersion + "/manifests/calico.yaml"
	DefaultKubeconfigDir = "."

	ContainerNamePrefix = "k8s-"

	DefaultDNSServer  = "node1"
	DNSMasqHostsFile  = "/var/lib/dnsmasq/cluster-hosts"
	DNSMasqConfigDir  = "/etc/dnsmasq.d"
	ClusterDomain     = "cluster.local"
	UpstreamDNS1      = "8.8.8.8"
	UpstreamDNS2      = "8.8.4.4"

	RegistryContainerName = "bink-registry"
	RegistryImage         = "docker.io/library/registry:2"
	RegistryPort          = 5000
	RegistryStaticIP      = "10.88.0.2"
	RegistryHostname      = "registry"
	RegistryVolume        = "bink-registry-data"

	CloudInitVolID = "cidata"

	DefaultSSHTimeout       = 60
	DefaultCloudInitTimeout = 300
	DefaultRetryInterval    = 2
)
