// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/podman"
)

func CompleteClusterNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	client, err := podman.NewClient()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx := cmd.Context()
	containers, err := client.ContainerList(ctx, "name="+config.ContainerNamePrefix)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	seen := make(map[string]bool)
	var names []string
	for _, ctr := range containers {
		clusterName, err := client.ContainerInspect(ctx, ctr, config.LabelInspectFormat(config.LabelClusterName))
		if err != nil || clusterName == "" {
			continue
		}
		if !seen[clusterName] && strings.HasPrefix(clusterName, toComplete) {
			seen[clusterName] = true
			names = append(names, clusterName)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func CompleteNodeNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	client, err := podman.NewClient()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx := cmd.Context()
	clusterName := viper.GetString("cluster.name")
	containers, err := client.ContainerList(ctx, config.LabelFilter(config.LabelClusterName, clusterName))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var names []string
	for _, ctr := range containers {
		component, _ := client.ContainerInspect(ctx, ctr, config.LabelInspectFormat(config.LabelComponent))
		if component != "" {
			continue
		}
		nodeName, err := client.ContainerInspect(ctx, ctr, config.LabelInspectFormat(config.LabelNodeName))
		if err != nil || nodeName == "" {
			continue
		}
		if strings.HasPrefix(nodeName, toComplete) {
			names = append(names, nodeName)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}


