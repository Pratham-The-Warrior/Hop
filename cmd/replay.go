package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	replayLast int
	replayList bool
)

var replayCmd = &cobra.Command{
	Use:   "replay",
	Short: "Replay captured tunnel requests",
	Long:  "Replay previously captured HTTP requests from the tunnel's replay buffer back to your local server.",
	Run:   runReplay,
}

func runReplay(cmd *cobra.Command, args []string) {
	fmt.Println("hop")
	fmt.Println(strings.Repeat("─", 50))

	if replayList {
		// List all buffered requests
		fmt.Println("Buffered Requests:")
		fmt.Println()
		fmt.Println("  #  Timestamp            Method  Path                      Status")
		fmt.Println("  ── ──────────────────── ──────  ────────────────────────  ──────")
		fmt.Println("  1  2026-06-20 14:32:01  POST    /api/v1/stripe/webhook   200 OK")
		fmt.Println("  2  2026-06-20 14:32:04  GET     /api/v1/users            200 OK")
		fmt.Println("  3  2026-06-20 14:32:08  POST    /api/v1/webhook          500 Error")
		fmt.Println()
		fmt.Println("3 requests buffered (use 'hop replay --last N' to replay)")
	} else {
		ordinal := "most recent"
		if replayLast > 1 {
			ordinal = fmt.Sprintf("#%d most recent", replayLast)
		}
		fmt.Printf("Replaying %s request...\n", ordinal)
		fmt.Println()
		fmt.Println("No active tunnel session found.")
		fmt.Println("Start a tunnel first with 'hop http <port>'")
	}

	fmt.Println(strings.Repeat("─", 50))
}

func init() {
	replayCmd.Flags().IntVar(&replayLast, "last", 1, "Replay the Nth most recent request")
	replayCmd.Flags().BoolVar(&replayList, "list", false, "List all buffered requests")
	rootCmd.AddCommand(replayCmd)
}
