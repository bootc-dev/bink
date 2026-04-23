package main

import (
	"fmt"
	"os"

	"github.com/bootc-dev/bink/internal/cli/api"
	"github.com/bootc-dev/bink/internal/cli/cluster"
	"github.com/bootc-dev/bink/internal/cli/node"
	"github.com/bootc-dev/bink/internal/cli/registry"
	"github.com/bootc-dev/bink/internal/config"
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

It replaces the shell scripts in the bootc-operator project with a
well-structured Go application that's easier to maintain and extend.`,
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

	rootCmd.AddCommand(cluster.NewClusterCmd())
	rootCmd.AddCommand(node.NewNodeCmd())
	rootCmd.AddCommand(api.NewAPICmd())
	rootCmd.AddCommand(registry.NewRegistryCmd())
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
