package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/prathmeshsarda/hop/pkg/config"
	"github.com/prathmeshsarda/hop/pkg/token"
	"github.com/prathmeshsarda/hop/pkg/tui"
	"github.com/prathmeshsarda/hop/pkg/tunnel"
	"github.com/spf13/cobra"
)

var (
	httpPassword      string
	httpReplayBuffer  int
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

	// Generate tunnel slug using the token generator
	slug := token.Generate()

	// Parse max body size
	maxBody := parseBodySize(httpReplayMaxBody)

	// Hash password with bcrypt if provided
	var passwordHash string
	if httpPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(httpPassword), bcrypt.DefaultCost)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to hash password: %v\n", err)
			os.Exit(1)
		}
		passwordHash = string(hash)
	}

	// Set up context with Ctrl+C handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	// Set up the TUI monitor
	renderer := tui.NewRenderer()
	monitor := tui.NewTunnelMonitor(port, "", httpReplayBuffer)
	if httpPassword != "" {
		monitor.Password = true
	}

	// Relay store for cross-process replay
	replayStore, err := tunnel.NewReplayStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: replay store unavailable: %v\n", err)
	}

	// Render initial connecting state
	monitor.SetConnected(tui.TierNone)
	renderer.Render(monitor.Render())

	// Create tunnel client with callbacks
	relayURL := config.RelayURL()

	var publicURL string
	var tunnelClient *tunnel.TunnelClient

	tunnelClient = tunnel.NewTunnelClient(tunnel.TunnelConfig{
		Port:          port,
		Slug:          slug,
		PasswordHash:  passwordHash,
		RelayURL:      relayURL,
		ReplayBuffer:  httpReplayBuffer,
		ReplayMaxBody: maxBody,
		Callbacks: tunnel.TunnelCallbacks{
			OnConnected: func(url string) {
				publicURL = url
				monitor.PublicURL = url
				monitor.SetConnected(tui.TierRelayed)
				renderer.Render(monitor.Render())

				// Print QR code below the monitor
				qrLines := tui.RenderQR(url)
				for _, line := range qrLines {
					fmt.Println(line)
				}
			},
			OnRequest: func(method, path string, statusCode int, statusText string, latency time.Duration) {
				monitor.LogRequest(method, path, statusCode, statusText, latency)
				renderer.Render(monitor.Render())

				// Update replay store for cross-process access
				if replayStore != nil {
					replayStore.Write(port, slug, publicURL, tunnelClient.ReplayBuf())
				}
			},
			OnPipeChange: func(activePipes int) {
				monitor.SetActivePipes(activePipes)
				renderer.Render(monitor.Render())
			},
			OnDisconnect: func(reason string) {
				fmt.Fprintf(os.Stderr, "\n⚠ Tunnel disconnected: %s\n", reason)
			},
		},
	})

	// Start the tunnel in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- tunnelClient.Start(ctx)
	}()

	// Wait for shutdown signal or error
	select {
	case <-sigCh:
		fmt.Println("\n\n⏹ Shutting down tunnel...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		tunnelClient.Stop(shutdownCtx)
		shutdownCancel()
		cancel()

	case err := <-errCh:
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Clean up replay store
	if replayStore != nil {
		replayStore.Clean()
	}

	fmt.Println("Tunnel closed.")
}

// parseBodySize parses a human-readable size string (e.g., "1MB", "5MB") into bytes.
func parseBodySize(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))

	multiplier := int64(1)
	if strings.HasSuffix(s, "GB") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GB")
	} else if strings.HasSuffix(s, "MB") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MB")
	} else if strings.HasSuffix(s, "KB") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "KB")
	}

	val, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 1 * 1024 * 1024 // Default: 1 MB
	}
	return val * multiplier
}

func init() {
	httpCmd.Flags().StringVar(&httpPassword, "password", "", "Require password for tunnel access")
	httpCmd.Flags().IntVar(&httpReplayBuffer, "replay-buffer", 50, "Number of requests to buffer for replay")
	httpCmd.Flags().StringVar(&httpReplayMaxBody, "replay-max-body", "1MB", "Maximum body size to capture for replay")
	rootCmd.AddCommand(httpCmd)
}
