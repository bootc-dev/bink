package virsh

type VirtInstallOptions struct {
	Name             string
	Memory           int
	MaxMemory        int
	VCPUs            int
	Disks            []string
	Networks         []NetworkConfig
	Filesystems      []FilesystemConfig
	XMLModifications []string
}

type FilesystemConfig struct {
	Source     string // Host path (in container)
	Target     string // Mount tag name for guest
	AccessMode string // mapped, passthrough, squash (default: passthrough)
	ReadOnly   bool
}

type NetworkConfig struct {
	Type        string
	Model       string
	MAC         string
	PortForward string
}

type QemuImgCreateOptions struct {
	Path          string
	Format        string
	BackingFile   string
	BackingFormat string
	Size          string
}
