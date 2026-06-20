package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/prathmeshsarda/hop/pkg/tui"
	"github.com/spf13/cobra"
)

var (
	httpPassword     string
	httpReplayBuffer int
	httpReplayMaxBody string
)

var httpCmd = &cobra.Command{
	Use:   "http <port>",
	Short: "Tunnel localhost to the internet",
	Long:  "Expose a local web server to the internet through a secure HTTPS tunnel.",
	Args:  cobra.ExactArgs(1),
	Run:   runHTTP,
}

func runHTTP(cmd *cobra.Command, args []string) {
	portStr := args[0]

	// Validate port
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		fmt.Fprintf(os.Stderr, "Error: invalid port '%s'. Must be a number between 1 and 65535.\n", portStr)
		os.Exit(1)
	}

	// Generate tunnel slug
	slug := fmt.Sprintf("t/%s", "bright-moon-7") // Mock for now
	publicURL := fmt.Sprintf("https://hop.to/%s", slug)

	// Render tunnel monitor UI
	renderer := tui.NewRenderer()

	var lines []string
	lines = append(lines, "hop")
	lines = append(lines, strings.Repeat("─", 50))
	lines = append(lines, fmt.Sprintf("tunneling localhost:%d", port))
	lines = append(lines, fmt.Sprintf("Public URL: %s", publicURL))
	lines = append(lines, "Status: ⏳ Connecting...")
	lines = append(lines, "")
	lines = append(lines, "Active Pipes: 0    |    Total Requests: 0")
	lines = append(lines, fmt.Sprintf("Replay Buffer: 0/%d requests captured", httpReplayBuffer))

	if httpPassword != "" {
		lines = append(lines, "Password Protection: ✓ Enabled")
	}

	lines = append(lines, strings.Repeat("─", 50))

	// Print QR code for the tunnel URL
	qrLines := tui.RenderQR(publicURL)
	lines = append(lines, qrLines...)

	renderer.Render(lines)

	// In full implementation, this is where we'd:
	// 1. Connect to the relay
	// 2. Register the tunnel slug
	// 3. Start forwarding requests to localhost:port
}

func init() {
	httpCmd.Flags().StringVar(&httpPassword, "password", "", "Require password for tunnel access")
	httpCmd.Flags().IntVar(&httpReplayBuffer, "replay-buffer", 50, "Number of requests to buffer for replay")
	httpCmd.Flags().StringVar(&httpReplayMaxBody, "replay-max-body", "1MB", "Maximum body size to capture for replay")
	rootCmd.AddCommand(httpCmd)
}
