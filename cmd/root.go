package cmd

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

const (
	appVersion     = "1.0.0"
	protocolVersion = "HOP/1.0"
)

var verbose bool

var rootCmd = &cobra.Command{
	Use:   "hop",
	Short: "Direct peer-to-peer file transfers and localhost web tunneling",
	Long: `hop — Direct, peer-to-peer file transfers and rock-solid localhost web tunneling.

  hop share <file>     Share a file or directory
  hop get <token>      Receive a shared file
  hop http <port>      Tunnel localhost to the internet
  hop replay           Replay captured tunnel requests`,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("hop v%s (%s, %s/%s)\n", appVersion, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		fmt.Printf("Protocol: %s\n", protocolVersion)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.AddCommand(versionCmd)
}
