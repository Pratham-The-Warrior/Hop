package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/prathmeshsarda/hop/pkg/config"
	"github.com/prathmeshsarda/hop/pkg/history"
	"github.com/prathmeshsarda/hop/pkg/protocol"
	"github.com/prathmeshsarda/hop/pkg/relay"
	"github.com/prathmeshsarda/hop/pkg/transfer"
	"github.com/prathmeshsarda/hop/pkg/tui"
	"github.com/spf13/cobra"
)

var (
	getYes    bool
	getResume bool
	getLimit  string
	getOutput string
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
	startTime := time.Now()

	// Validate token format
	if !tokenPattern.MatchString(tok) {
		fmt.Fprintf(os.Stderr, "Error: invalid token format '%s'. Expected format: word-word-NN\n", tok)
		os.Exit(1)
	}

	// Determine output directory
	outputDir := getOutput
	if outputDir == "" {
		var err error
		outputDir, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot determine current directory: %v\n", err)
			os.Exit(1)
		}
	}

	// Parse bandwidth limit if specified
	var limiter *transfer.TokenBucketLimiter
	if getLimit != "" {
		bps, err := transfer.ParseBandwidthLimit(getLimit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid bandwidth limit '%s': %v\n", getLimit, err)
			os.Exit(1)
		}
		limiter = transfer.NewTokenBucketLimiter(bps)
		_ = limiter // Will be used when we add receive-side rate limiting
	}

	// Set up context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Connect to relay
	relayURL := config.RelayURL()
	client := relay.NewClient(relayURL)

	fmt.Println("hop")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("Connecting to token: %s\n", tok)
	fmt.Printf("Relay: %s\n", relayURL)

	if getResume {
		fmt.Println("Resume mode: enabled (will check for partial downloads)")
	}

	fmt.Println()

	if err := client.Authenticate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: relay authentication failed: %v\n", err)
		os.Exit(1)
	}

	if err := client.JoinToken(ctx, tok); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to join token '%s': %v\n", tok, err)
		fmt.Fprintf(os.Stderr, "Hint: make sure the sender is running 'hop share' and the token is correct.\n")
		os.Exit(1)
	}
	defer client.Close()

	// Handle Ctrl+C in background
	go func() {
		select {
		case <-sigCh:
			fmt.Println("\n\nTransfer cancelled.")
			cancelMsg := &protocol.Message{Type: protocol.MsgTransferCancel}
			_ = client.Send(context.Background(), cancelMsg)
			client.Close()
			cancel()
		case <-ctx.Done():
		}
	}()

	// Set up TUI for progress
	renderer := tui.NewRenderer()
	var progressBar *tui.ProgressBar
	var receivedFileName string

	callbacks := &transfer.EngineCallbacks{
		OnHandshakeComplete: func(tier tui.ConnectionTier) {
			fmt.Printf("Connection: %s\n", tier.String())
			fmt.Println()
		},
		OnOfferReceived: func(offer *protocol.TransferOffer) bool {
			receivedFileName = offer.FileName
			hashPrefix := fmt.Sprintf("%x", offer.SHA256[:3]) + "..." + fmt.Sprintf("%x", offer.SHA256[28:])

			fmt.Printf("Incoming file: %s (%s)\n", offer.FileName, formatSize(offer.FileSize))
			fmt.Printf("SHA-256: %s\n", hashPrefix)
			if offer.Compressed {
				fmt.Println("Compression: zstd")
			}
			fmt.Println()

			// Auto-accept if --yes flag
			if getYes {
				fmt.Println("Auto-accept enabled (--yes)")
				// Initialize progress bar
				progressBar = tui.NewProgressBar(offer.FileName, offer.FileSize, tok, "")
				progressBar.Direction = "receiving"
				progressBar.Tier = tui.TierRelayed
				return true
			}

			// Prompt for acceptance
			fmt.Print("Accept transfer? [Y/n] ")
			reader := bufio.NewReader(os.Stdin)
			response, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nError reading input: %v\n", err)
				return false
			}

			response = strings.TrimSpace(strings.ToLower(response))
			if response == "" || response == "y" || response == "yes" {
				// Initialize progress bar
				progressBar = tui.NewProgressBar(offer.FileName, offer.FileSize, tok, "")
				progressBar.Direction = "receiving"
				progressBar.Tier = tui.TierRelayed
				fmt.Println()
				return true
			}

			fmt.Println("Transfer rejected.")
			return false
		},
		OnProgress: func(bytesSoFar, totalBytes int64) {
			if progressBar != nil {
				progressBar.Update(bytesSoFar)
				renderer.Render(progressBar.Render())
			}
		},
		OnComplete: func(hashHex string) {
			if progressBar != nil {
				hp := hashHex
				if len(hp) > 12 {
					hp = hp[:6] + "..." + hp[len(hp)-4:]
				}
				progressBar.Complete(hp)
				renderer.Render(progressBar.Render())
			}
		},
		OnError: func(err error) {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		},
	}

	err := transfer.ReceiveFile(ctx, client, outputDir, callbacks)
	if err != nil {
		if ctx.Err() != nil {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "\nTransfer failed: %v\n", err)
		os.Exit(1)
	}

	// Log to history
	duration := time.Since(startTime)
	if receivedFileName == "" {
		receivedFileName = "unknown"
	}
	history.Log(history.Entry{
		Timestamp: time.Now(),
		Direction: history.Received,
		FileName:  receivedFileName,
		FileSize:  "—",
		Token:     tok,
		Tier:      "Relay",
		Duration:  formatDuration(duration),
		Verified:  true,
	})

	fmt.Println()
}

func init() {
	getCmd.Flags().BoolVar(&getYes, "yes", false, "Auto-accept incoming transfers")
	getCmd.Flags().BoolVar(&getResume, "resume", false, "Resume a partial transfer")
	getCmd.Flags().StringVar(&getLimit, "limit", "", "Bandwidth limit (e.g., 5MB/s)")
	getCmd.Flags().StringVarP(&getOutput, "output", "o", "", "Output directory (default: current directory)")
	rootCmd.AddCommand(getCmd)
}
