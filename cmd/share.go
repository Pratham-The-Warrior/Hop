package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/prathmeshsarda/hop/pkg/config"
	"github.com/prathmeshsarda/hop/pkg/crypto"
	"github.com/prathmeshsarda/hop/pkg/history"
	"github.com/prathmeshsarda/hop/pkg/network"
	"github.com/prathmeshsarda/hop/pkg/protocol"
	"github.com/prathmeshsarda/hop/pkg/relay"
	"github.com/prathmeshsarda/hop/pkg/token"
	"github.com/prathmeshsarda/hop/pkg/transfer"
	"github.com/prathmeshsarda/hop/pkg/tui"
	"github.com/spf13/cobra"
)

var (
	shareCompress bool
	shareLimit    string
	shareYes      bool
)

var shareCmd = &cobra.Command{
	Use:   "share <file|directory>",
	Short: "Share a file or directory",
	Long:  "Share a file or directory directly with another person. Generates a token and link for the receiver.",
	Args:  cobra.ExactArgs(1),
	Run:   runShare,
}

func runShare(cmd *cobra.Command, args []string) {
	target := args[0]

	// Validate file/directory exists
	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot access '%s': %v\n", target, err)
		os.Exit(1)
	}

	// Directories will be handled in Milestone 11 (tar.gz packaging)
	if info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: directory sharing requires packaging (coming in a future release).\n")
		fmt.Fprintf(os.Stderr, "Hint: share individual files for now, e.g., 'hop share myfile.txt'\n")
		os.Exit(1)
	}

	tok := token.Generate()
	link := fmt.Sprintf("https://hop.to/%s", tok)
	name := filepath.Base(target)
	startTime := time.Now()

	// Pre-compute SHA-256 with a spinner
	fmt.Printf("Computing SHA-256 of '%s' (%s)...\n", name, formatSize(info.Size()))
	fileHash, _, err := computeFileHashForDisplay(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error computing file hash: %v\n", err)
		os.Exit(1)
	}
	hashPrefix := fmt.Sprintf("%x", fileHash[:6]) + "..." + fmt.Sprintf("%x", fileHash[28:])
	fmt.Printf("SHA-256: %s\n\n", hashPrefix)

	// Set up context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Parse bandwidth limit if specified
	var limiter *transfer.TokenBucketLimiter
	if shareLimit != "" {
		bps, err := transfer.ParseBandwidthLimit(shareLimit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid bandwidth limit '%s': %v\n", shareLimit, err)
			os.Exit(1)
		}
		limiter = transfer.NewTokenBucketLimiter(bps)
	}

	// Connect to relay
	relayURL := config.RelayURL()
	client := relay.NewClient(relayURL)

	fmt.Printf("Connecting to relay (%s)...\n", relayURL)
	if err := client.Authenticate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: relay authentication failed: %v\n", err)
		os.Exit(1)
	}

	if err := client.RegisterToken(ctx, tok); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to register token with relay: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	// Attempt P2P connection, fall back to relay
	fmt.Println("Negotiating connection tier...")
	connResult, err := network.Connect(ctx, network.ConnectConfig{
		RelayURL:     relayURL,
		Token:        tok,
		SessionToken: client.SessionToken(),
		EnableLAN:    true,
		EnableP2P:    true,
		Role:         "sender",
	}, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: connection negotiation failed: %v\n", err)
		os.Exit(1)
	}
	transport := connResult.Transport
	actualTier := connResult.Tier

	// Set up TUI
	renderer := tui.NewRenderer()
	progressBar := tui.NewProgressBar(name, info.Size(), tok, link)
	progressBar.Tier = actualTier

	if shareCompress {
		progressBar.Compress = true
	}
	if shareLimit != "" {
		progressBar.Limit = shareLimit
	}

	// Render initial "waiting" state
	var waitingLines []string
	waitingLines = append(waitingLines, "hop")
	waitingLines = append(waitingLines, strings.Repeat("─", 50))
	waitingLines = append(waitingLines, fmt.Sprintf("sharing '%s' (%s)", name, formatSize(info.Size())))
	waitingLines = append(waitingLines, fmt.Sprintf("Token: %s", tok))
	waitingLines = append(waitingLines, fmt.Sprintf("Link:  %s", link))
	waitingLines = append(waitingLines, fmt.Sprintf("Connection: %s", tui.TierNone.String()))
	waitingLines = append(waitingLines, "")

	if shareCompress {
		waitingLines = append(waitingLines, "Compression: zstd (enabled)")
	}
	if shareLimit != "" {
		waitingLines = append(waitingLines, fmt.Sprintf("Speed limit: %s", shareLimit))
	}

	waitingLines = append(waitingLines, strings.Repeat("─", 50))

	// QR code
	qrLines := tui.RenderQR(link)
	waitingLines = append(waitingLines, qrLines...)

	waitingLines = append(waitingLines, "")
	waitingLines = append(waitingLines, "Notice: Browser links are securely encrypted in transit;")
	waitingLines = append(waitingLines, "        CLI-to-CLI transfers are fully end-to-end encrypted.")
	waitingLines = append(waitingLines, "")
	waitingLines = append(waitingLines, "⏳ Waiting for receiver...")

	renderer.Render(waitingLines)

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

	// Run the send transfer
	callbacks := &transfer.EngineCallbacks{
		OnHandshakeComplete: func(tier tui.ConnectionTier) {
			progressBar.Tier = tier
		},
		OnProgress: func(bytesSoFar, totalBytes int64) {
			progressBar.Update(bytesSoFar)
			renderer.Render(progressBar.Render())
		},
		OnComplete: func(hashHex string) {
			hp := hashHex
			if len(hp) > 12 {
				hp = hp[:6] + "..." + hp[len(hp)-4:]
			}
			progressBar.Complete(hp)
			renderer.Render(progressBar.Render())
		},
		OnError: func(err error) {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		},
	}

	err = transfer.SendFile(ctx, transport, target, shareCompress, limiter, callbacks)
	if err != nil {
		if ctx.Err() != nil {
			// Already handled by signal handler
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "\nTransfer failed: %v\n", err)
		os.Exit(1)
	}

	// Log to history
	duration := time.Since(startTime)
	history.Log(history.Entry{
		Timestamp: time.Now(),
		Direction: history.Sent,
		FileName:  name,
		FileSize:  formatSize(info.Size()),
		Token:     tok,
		Tier:      actualTier.String(),
		Duration:  formatDuration(duration),
		Verified:  true,
	})

	fmt.Println()
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", float64(bytes)/float64(TB))
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// computeFileHashForDisplay computes the SHA-256 of a file for pre-display.
func computeFileHashForDisplay(path string) ([32]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, 0, err
	}
	defer f.Close()

	hasher := crypto.NewFileHasher()
	buf := make([]byte, 1<<20) // 1 MB
	for {
		n, err := f.Read(buf)
		if n > 0 {
			hasher.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	return hasher.Sum(), hasher.BytesHashed(), nil
}

func init() {
	shareCmd.Flags().BoolVar(&shareCompress, "compress", false, "Enable zstd streaming compression")
	shareCmd.Flags().StringVar(&shareLimit, "limit", "", "Bandwidth limit (e.g., 5MB/s)")
	shareCmd.Flags().BoolVar(&shareYes, "yes", false, "Skip confirmation prompts")
	rootCmd.AddCommand(shareCmd)
}
