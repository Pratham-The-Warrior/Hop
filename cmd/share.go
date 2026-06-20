package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prathmeshsarda/hop/pkg/token"
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

	tok := token.Generate()
	link := fmt.Sprintf("https://hop.to/%s", tok)

	name := filepath.Base(target)
	var sizeStr string
	var isDir bool

	if info.IsDir() {
		isDir = true
		// Walk directory to count files and total size
		var totalSize int64
		var fileCount int
		filepath.Walk(target, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !fi.IsDir() {
				totalSize += fi.Size()
				fileCount++
			}
			return nil
		})
		sizeStr = formatSize(totalSize)
		fmt.Printf("Packaging directory '%s/' (%d files, %s)...\n", name, fileCount, sizeStr)
	} else {
		sizeStr = formatSize(info.Size())
	}

	// Determine connection tier display (mock for now — always shows "waiting")
	tierDisplay := "⏳ Waiting for receiver..."

	// Render the sharing UI
	renderer := tui.NewRenderer()

	// Build the display
	var lines []string
	lines = append(lines, "hop")
	lines = append(lines, strings.Repeat("─", 50))
	if isDir {
		lines = append(lines, fmt.Sprintf("sharing '%s/' (tar.gz, %s)", name, sizeStr))
	} else {
		lines = append(lines, fmt.Sprintf("sharing '%s' (%s)", name, sizeStr))
	}
	lines = append(lines, fmt.Sprintf("Token: %s", tok))
	lines = append(lines, fmt.Sprintf("Link:  %s", link))
	lines = append(lines, fmt.Sprintf("Connection: %s", tierDisplay))
	lines = append(lines, "")

	if shareCompress {
		lines = append(lines, "Compression: zstd (enabled)")
	}
	if shareLimit != "" {
		lines = append(lines, fmt.Sprintf("Speed limit: %s", shareLimit))
	}

	lines = append(lines, strings.Repeat("─", 50))

	// Print QR code
	qrLines := tui.RenderQR(link)
	lines = append(lines, qrLines...)

	lines = append(lines, "")
	lines = append(lines, "Notice: Browser links are securely encrypted in transit;")
	lines = append(lines, "        CLI-to-CLI transfers are fully end-to-end encrypted.")

	renderer.Render(lines)

	// In the future, this is where we'd start listening for a receiver connection.
	// For now, the command displays the info and exits.
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

func init() {
	shareCmd.Flags().BoolVar(&shareCompress, "compress", false, "Enable zstd streaming compression")
	shareCmd.Flags().StringVar(&shareLimit, "limit", "", "Bandwidth limit (e.g., 5MB/s)")
	shareCmd.Flags().BoolVar(&shareYes, "yes", false, "Skip confirmation prompts")
	rootCmd.AddCommand(shareCmd)
}
