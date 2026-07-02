// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package podman

import (
	"go.podman.io/podman/v6/pkg/specgen"
	"github.com/opencontainers/runtime-spec/specs-go"
	nettypes "go.podman.io/common/libnetwork/types"
)

type ContainerCreateOptions struct {
	Name           string
	Image          string
	Entrypoint     []string
	Command        []string
	Network        string
	NetworkOptions map[string]nettypes.PerNetworkOptions
	Devices        []specs.LinuxDevice
	Volumes        []*specgen.NamedVolume
	Mounts         []specs.Mount
	ImageVolumes   []*specgen.ImageVolume
	PortMappings   []nettypes.PortMapping
	Environment    map[string]string
	Labels         map[string]string
	CapAdd         []string
	SelinuxOpts    []string
	Privileged     bool
}
