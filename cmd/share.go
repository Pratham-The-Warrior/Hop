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

	"github.com/prathmeshsarda/hop/pkg/archive"
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

	// Handle directory packaging: create a temporary .tar.gz archive
	isDir := info.IsDir()
	var archivePath string
	if isDir {
		fmt.Printf("Packaging directory '%s'...\n", filepath.Base(target))
		packResult, err := archive.PackDirectory(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to package directory: %v\n", err)
			os.Exit(1)
		}
		archivePath = packResult.ArchivePath
		defer archive.CleanupArchive(archivePath)

		fmt.Printf("Packaged %d files (%s) into archive\n",
			packResult.FileCount, formatSize(packResult.TotalSize))

		// Switch target to the archive for transfer
		target = archivePath
		// Re-stat the archive file
		info, err = os.Stat(archivePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot access archive: %v\n", err)
			os.Exit(1)
		}
	}

	tok := token.Generate()
	link := fmt.Sprintf("https://hop.to/%s", tok)
	var name string
	if isDir {
		// Use the original directory name for display, but the archive
		// filename (.tar.gz) is what gets transferred over the wire.
		// The receiver sees the IsDir flag and auto-unpacks.
		name = filepath.Base(args[0])
	} else {
		name = filepath.Base(target)
	}
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
	if isDir {
		waitingLines = append(waitingLines, "Type:  directory (tar.gz archive)")
	}
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
			signal.Stop(sigCh)
			fmt.Println("\n\nTransfer cancelled.")
			cancelMsg := &protocol.Message{Type: protocol.MsgTransferCancel}
			_ = client.Send(context.Background(), cancelMsg)
			client.Close()
			cancel()
		case <-ctx.Done():
		}
	}()

	// Run the send transfer — dual mode: handles both browser and CLI receivers.
	// SendFileBrowserMode listens for messages and routes accordingly:
	// - BROWSER_INFO_REQ → serve browser download page/stream
	// - HOP_HELLO → falls through to CLI-to-CLI SendFile
	browserDownloadCount := 0
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
		OnResumeDetected: func(offset int64, total int64) {
			if total > 0 {
				pct := float64(offset) / float64(total) * 100
				fmt.Printf("Receiver resuming from %s / %s (%.1f%%)\n", formatSize(offset), formatSize(total), pct)
			}
		},
		OnBrowserDownload: func() {
			browserDownloadCount++
			fmt.Printf("\n🌐 Browser download #%d started\n", browserDownloadCount)
			fmt.Println("   (encrypted in transit via HTTPS — not end-to-end)")
		},
	}

	// First, try browser mode — this also handles CLI receivers via ErrCLIReceiverDetected
	err = transfer.SendFileBrowserMode(ctx, transport, target, limiter, callbacks)

	// Check if we need to fall back to CLI-to-CLI mode
	if cliErr, ok := err.(transfer.ErrCLIReceiverDetected); ok {
		// A CLI receiver connected — switch to the standard encrypted transfer.
		// We need to continue the handshake that was already started (the HOP_HELLO
		// was already received). Create a HandshakeTransport wrapper that replays
		// the first message.
		_ = cliErr // The HOP_HELLO message is handled inside SendFile's receive loop
		fmt.Println("\n🔒 CLI receiver connected — using end-to-end encryption")

		err = transfer.SendFile(ctx, transport, target, isDir, shareCompress, limiter, callbacks)
	}

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
	tierName := actualTier.String()
	if browserDownloadCount > 0 {
		tierName = fmt.Sprintf("%s (browser×%d)", actualTier.String(), browserDownloadCount)
	}
	historyName := name
	if isDir {
		historyName = fmt.Sprintf("./%s/ (tar)", name)
	}
	history.Log(history.Entry{
		Timestamp: time.Now(),
		Direction: history.Sent,
		FileName:  historyName,
		FileSize:  formatSize(info.Size()),
		Token:     tok,
		Tier:      tierName,
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
