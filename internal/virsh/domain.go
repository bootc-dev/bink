package virsh

import (
	"fmt"

	"libvirt.org/go/libvirtxml"
)

type DomainOption func(d *libvirtxml.Domain)

func NewDomain(opts ...DomainOption) *libvirtxml.Domain {
	domain := &libvirtxml.Domain{}
	for _, f := range opts {
		f(domain)
	}
	return domain
}

func allocateDevices(d *libvirtxml.Domain) {
	if d.Devices == nil {
		d.Devices = &libvirtxml.DomainDeviceList{}
	}
}

func WithKVM() DomainOption {
	return func(d *libvirtxml.Domain) {
		d.Type = "kvm"
	}
}

func WithName(name string) DomainOption {
	return func(d *libvirtxml.Domain) {
		d.Name = name
	}
}

func WithMemory(memory uint) DomainOption {
	return func(d *libvirtxml.Domain) {
		d.Memory = &libvirtxml.DomainMemory{
			Value: memory,
			Unit:  "MiB",
		}
	}
}

func WithVCPUs(cpus uint) DomainOption {
	return func(d *libvirtxml.Domain) {
		d.VCPU = &libvirtxml.DomainVCPU{Value: cpus}
	}
}

func WithQ35OS() DomainOption {
	return func(d *libvirtxml.Domain) {
		d.OS = &libvirtxml.DomainOS{
			Type: &libvirtxml.DomainOSType{
				Arch:    "x86_64",
				Machine: "q35",
				Type:    "hvm",
			},
			BootDevices: []libvirtxml.DomainBootDevice{
				{Dev: "hd"},
			},
		}
	}
}

func WithFeatures() DomainOption {
	return func(d *libvirtxml.Domain) {
		d.Features = &libvirtxml.DomainFeatureList{
			ACPI: &libvirtxml.DomainFeature{},
			APIC: &libvirtxml.DomainFeatureAPIC{},
		}
	}
}

func WithCPUHostPassthrough() DomainOption {
	return func(d *libvirtxml.Domain) {
		d.CPU = &libvirtxml.DomainCPU{
			Mode: "host-passthrough",
		}
	}
}

func WithMemoryBackingForVirtiofs() DomainOption {
	return func(d *libvirtxml.Domain) {
		d.MemoryBacking = &libvirtxml.DomainMemoryBacking{
			MemorySource: &libvirtxml.DomainMemorySource{Type: "memfd"},
			MemoryAccess: &libvirtxml.DomainMemoryAccess{Mode: "shared"},
		}
	}
}

func WithDisk(path, format, dev, bus string) DomainOption {
	return func(d *libvirtxml.Domain) {
		allocateDevices(d)
		d.Devices.Disks = append(d.Devices.Disks, libvirtxml.DomainDisk{
			Device: "disk",
			Driver: &libvirtxml.DomainDiskDriver{
				Name: "qemu",
				Type: format,
			},
			Source: &libvirtxml.DomainDiskSource{
				File: &libvirtxml.DomainDiskSourceFile{File: path},
			},
			Target: &libvirtxml.DomainDiskTarget{
				Dev: dev,
				Bus: bus,
			},
		})
	}
}

func WithCDROM(path string) DomainOption {
	return func(d *libvirtxml.Domain) {
		allocateDevices(d)
		d.Devices.Disks = append(d.Devices.Disks, libvirtxml.DomainDisk{
			Device: "cdrom",
			Driver: &libvirtxml.DomainDiskDriver{
				Name: "qemu",
				Type: "raw",
			},
			Source: &libvirtxml.DomainDiskSource{
				File: &libvirtxml.DomainDiskSourceFile{File: path},
			},
			Target: &libvirtxml.DomainDiskTarget{
				Dev: "sda",
				Bus: "sata",
			},
			ReadOnly: &libvirtxml.DomainDiskReadOnly{},
		})
	}
}

type PortForward struct {
	Start int
	To    int
}

func WithPasstInterface(portForwards []PortForward) DomainOption {
	return func(d *libvirtxml.Domain) {
		allocateDevices(d)
		iface := libvirtxml.DomainInterface{
			Source: &libvirtxml.DomainInterfaceSource{
				User: &libvirtxml.DomainInterfaceSourceUser{},
			},
			Model:   &libvirtxml.DomainInterfaceModel{Type: "virtio"},
			Backend: &libvirtxml.DomainInterfaceBackend{Type: "passt"},
		}
		fwd := libvirtxml.DomainInterfaceSourcePortForward{Proto: "tcp"}
		for _, pf := range portForwards {
			fwd.Ranges = append(fwd.Ranges, libvirtxml.DomainInterfaceSourcePortForwardRange{
				Start: uint(pf.Start),
				To:    uint(pf.To),
			})
		}
		if len(fwd.Ranges) > 0 {
			iface.PortForward = []libvirtxml.DomainInterfaceSourcePortForward{fwd}
		}
		d.Devices.Interfaces = append(d.Devices.Interfaces, iface)
	}
}

func WithMcastInterface(mac, addr string, port int) DomainOption {
	return func(d *libvirtxml.Domain) {
		allocateDevices(d)
		d.Devices.Interfaces = append(d.Devices.Interfaces, libvirtxml.DomainInterface{
			MAC:   &libvirtxml.DomainInterfaceMAC{Address: mac},
			Model: &libvirtxml.DomainInterfaceModel{Type: "virtio"},
			Source: &libvirtxml.DomainInterfaceSource{
				MCast: &libvirtxml.DomainInterfaceSourceMCast{
					Address: addr,
					Port:    uint(port),
				},
			},
		})
	}
}

func WithVirtiofsSocket(socketPath, target string) DomainOption {
	return func(d *libvirtxml.Domain) {
		allocateDevices(d)
		d.Devices.Filesystems = append(d.Devices.Filesystems, libvirtxml.DomainFilesystem{
			Driver: &libvirtxml.DomainFilesystemDriver{Type: "virtiofs"},
			Source: &libvirtxml.DomainFilesystemSource{
				Mount: &libvirtxml.DomainFilesystemSourceMount{Socket: socketPath},
			},
			Target: &libvirtxml.DomainFilesystemTarget{Dir: target},
		})
	}
}

func WithSerialConsole() DomainOption {
	return func(d *libvirtxml.Domain) {
		allocateDevices(d)
		port0 := uint(0)
		d.Devices.Serials = append(d.Devices.Serials, libvirtxml.DomainSerial{
			Source: &libvirtxml.DomainChardevSource{
				Pty: &libvirtxml.DomainChardevSourcePty{},
			},
			Target: &libvirtxml.DomainSerialTarget{
				Type: "isa-serial",
				Port: &port0,
			},
		})
		d.Devices.Consoles = append(d.Devices.Consoles, libvirtxml.DomainConsole{
			Source: &libvirtxml.DomainChardevSource{
				Pty: &libvirtxml.DomainChardevSourcePty{},
			},
			Target: &libvirtxml.DomainConsoleTarget{
				Type: "serial",
				Port: &port0,
			},
		})
	}
}

func WithGuestAgent() DomainOption {
	return func(d *libvirtxml.Domain) {
		allocateDevices(d)
		d.Devices.Channels = append(d.Devices.Channels, libvirtxml.DomainChannel{
			Source: &libvirtxml.DomainChardevSource{
				UNIX: &libvirtxml.DomainChardevSourceUNIX{},
			},
			Target: &libvirtxml.DomainChannelTarget{
				VirtIO: &libvirtxml.DomainChannelTargetVirtIO{
					Name: "org.qemu.guest_agent.0",
				},
			},
		})
	}
}

func MarshalDomainXML(opts ...DomainOption) (string, error) {
	domain := NewDomain(opts...)
	xml, err := domain.Marshal()
	if err != nil {
		return "", fmt.Errorf("marshaling domain XML: %w", err)
	}
	return xml, nil
}
