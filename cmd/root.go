package cmd

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/prathmeshsarda/hop/pkg/update"
	"github.com/spf13/cobra"
)

const (
	appVersion      = "1.0.0"
	protocolVersion = "HOP/1.0"

	// GitHub repository for update checks
	githubOwner = "Pratham-The-Warrior"
	githubRepo  = "Hop"
)

var verbose bool
var versionCheckUpdate bool

var rootCmd = &cobra.Command{
	Use:   "hop",
	Short: "Direct peer-to-peer file transfers and localhost web tunneling",
	Long: `hop — Direct, peer-to-peer file transfers and rock-solid localhost web tunneling.

  hop share <file>     Share a file or directory
  hop get <token>      Receive a shared file
  hop http <port>      Tunnel localhost to the internet
  hop replay           Replay captured tunnel requests
  hop history          View transfer history`,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("hop v%s (%s, %s/%s)\n", appVersion, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		fmt.Printf("Protocol: %s\n", protocolVersion)

		if versionCheckUpdate {
			checkForUpdate()
		}
	},
}

// checkForUpdate queries GitHub for the latest release and prints an update
// notice if a newer version is available. Silently skips on any error.
func checkForUpdate() {
	fmt.Println()
	fmt.Print("Checking for updates... ")

	release, err := update.CheckLatestVersion(context.Background(), githubOwner, githubRepo)
	if err != nil {
		// Silently skip — never block the version command
		fmt.Println("(unable to check)")
		return
	}

	cmp := update.CompareVersions(appVersion, release.TagName)
	switch {
	case cmp < 0:
		fmt.Printf("\n⬆ Update available: v%s → %s\n", appVersion, release.TagName)
		fmt.Printf("  Download: %s\n", release.HTMLURL)
	case cmp == 0:
		fmt.Println("✓ You're on the latest version.")
	default:
		fmt.Println("✓ You're ahead of the latest release.")
	}
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	versionCmd.Flags().BoolVar(&versionCheckUpdate, "check-update", false, "Check GitHub for newer versions")
	rootCmd.AddCommand(versionCmd)
}
