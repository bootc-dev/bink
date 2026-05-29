// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/bootc-dev/bink/internal/cli"
	"github.com/bootc-dev/bink/internal/cli/api"
	"github.com/bootc-dev/bink/internal/cli/cluster"
	"github.com/bootc-dev/bink/internal/cli/node"
	"github.com/bootc-dev/bink/internal/cli/registry"
	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/internal/version"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	verbose bool
	debug   bool
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "bink",
	Short: "Manage containerized Kubernetes clusters with VMs",
	Long: `bink is a CLI tool for managing containerized Kubernetes clusters
where each node is a Podman container running a VM inside.

Each cluster node is a Podman container running libvirt/QEMU with a
Fedora bootc VM and kubeadm-managed Kubernetes.

Common workflows:

  # Create a cluster and get a kubeconfig in one step
  bink cluster start --expose ./kubeconfig
  export KUBECONFIG=./kubeconfig
  kubectl get nodes

  # Create a cluster, then expose separately
  bink cluster start --cluster-name dev
  bink api expose --cluster-name dev
  export KUBECONFIG=./kubeconfig-dev

  # Add worker nodes
  bink node add node2
  bink node add node3 --memory 4096

  # SSH into a node
  bink node ssh node1

  # Tear down
  bink cluster stop --remove-data`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
			logrus.Debug("Debug logging enabled")
		} else if verbose {
			logrus.SetLevel(logrus.InfoLevel)
		} else {
			logrus.SetLevel(logrus.WarnLevel)
		}

		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: false,
			DisableTimestamp: true,
		})
	},
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.bink/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "debug output")
	rootCmd.PersistentFlags().String("cluster-name", config.DefaultNetworkName, "cluster name")

	viper.BindPFlag("cluster.name", rootCmd.PersistentFlags().Lookup("cluster-name"))
	viper.BindPFlag("logging.verbose", rootCmd.PersistentFlags().Lookup("verbose"))
	viper.BindPFlag("logging.debug", rootCmd.PersistentFlags().Lookup("debug"))

	rootCmd.RegisterFlagCompletionFunc("cluster-name", cli.CompleteClusterNames)

	rootCmd.AddCommand(cluster.NewClusterCmd())
	rootCmd.AddCommand(node.NewNodeCmd())
	rootCmd.AddCommand(api.NewAPICmd())
	rootCmd.AddCommand(registry.NewRegistryCmd())
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.Print())
		},
	})
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath("$HOME/.bink")
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix("BINK")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		logrus.Debugf("Using config file: %s", viper.ConfigFileUsed())
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
