package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var (
	getYes    bool
	getResume bool
	getLimit  string
)

var tokenPattern = regexp.MustCompile(`^[a-z]+-[a-z]+-\d+$`)

var getCmd = &cobra.Command{
	Use:   "get <token>",
	Short: "Receive a shared file",
	Long:  "Connect to a sender using a transfer token and download the shared file.",
	Args:  cobra.ExactArgs(1),
	Run:   runGet,
}

func runGet(cmd *cobra.Command, args []string) {
	tok := args[0]

	// Validate token format
	if !tokenPattern.MatchString(tok) {
		fmt.Fprintf(os.Stderr, "Error: invalid token format '%s'. Expected format: word-word-NN\n", tok)
		os.Exit(1)
	}

	// Display the receiver acceptance layout
	fmt.Println("hop")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("Connecting to token: %s\n", tok)

	if getResume {
		fmt.Println("Resume mode: enabled (will check for partial downloads)")
	}

	// In full implementation, we'd connect to the relay/peer, receive the
	// TRANSFER_OFFER message, and display the acceptance prompt.
	// For now, show the mock acceptance UI:
	fmt.Println()
	fmt.Println("Incoming file: example.mp4 (2.00 GB)")
	fmt.Printf("From: [awaiting connection]\n")
	fmt.Println("SHA-256: [awaiting handshake]")
	fmt.Println()

	if getYes {
		fmt.Println("Auto-accept enabled (--yes)")
	} else {
		fmt.Print("Accept transfer? [Y/n] ")
		// In full implementation, we'd read user input here
	}

	if getLimit != "" {
		fmt.Printf("Speed limit: %s\n", getLimit)
	}

	fmt.Println(strings.Repeat("─", 50))
}

func init() {
	getCmd.Flags().BoolVar(&getYes, "yes", false, "Auto-accept incoming transfers")
	getCmd.Flags().BoolVar(&getResume, "resume", false, "Resume a partial transfer")
	getCmd.Flags().StringVar(&getLimit, "limit", "", "Bandwidth limit (e.g., 5MB/s)")
	rootCmd.AddCommand(getCmd)
}
