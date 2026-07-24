package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/prathmeshsarda/hop/pkg/history"
	"github.com/spf13/cobra"
)

var (
	historyClear bool
	historyLast  int
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "View transfer history",
	Long:  "Display a log of all completed file transfers, including direction, filename, size, token, tier, and verification status.",
	Run:   runHistory,
}

func runHistory(cmd *cobra.Command, args []string) {
	if historyClear {
		if err := history.Clear(); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No history to clear.")
				return
			}
			fmt.Fprintf(os.Stderr, "Error clearing history: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Transfer history cleared.")
		return
	}

	entries, err := history.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading history: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Println("hop")
		fmt.Println(strings.Repeat("─", 50))
		fmt.Println("No transfer history yet.")
		fmt.Println()
		fmt.Println("Transfer history is recorded automatically when")
		fmt.Println("you send or receive files with 'hop share' or 'hop get'.")
		fmt.Println(strings.Repeat("─", 50))
		return
	}

	// Apply --last N filter
	display := entries
	if historyLast > 0 && historyLast < len(entries) {
		display = entries[len(entries)-historyLast:]
	}

	fmt.Println("hop — Transfer History")
	fmt.Println(strings.Repeat("─", 100))
	fmt.Printf("  %-19s  %-4s  %-30s  %-10s  %-20s  %-6s  %-8s  %s\n",
		"Timestamp", "Dir", "Filename", "Size", "Token", "Tier", "Time", "OK")
	fmt.Printf("  %-19s  %-4s  %-30s  %-10s  %-20s  %-6s  %-8s  %s\n",
		strings.Repeat("─", 19), "────", strings.Repeat("─", 30), strings.Repeat("─", 10),
		strings.Repeat("─", 20), strings.Repeat("─", 6), strings.Repeat("─", 8), "──")

	for _, e := range display {
		verified := "✓"
		if !e.Verified {
			verified = "✗"
		}
		dirIcon := "↑"
		if e.Direction == history.Received {
			dirIcon = "↓"
		}

		fmt.Printf("  %-19s  %s%-3s  %-30s  %-10s  %-20s  %-6s  %-8s  %s\n",
			e.Timestamp.Format("2006-01-02 15:04"),
			dirIcon,
			string(e.Direction),
			truncateHistoryStr(e.FileName, 30),
			e.FileSize,
			e.Token,
			e.Tier,
			e.Duration,
			verified,
		)
	}

	fmt.Println(strings.Repeat("─", 100))
	if historyLast > 0 && historyLast < len(entries) {
		fmt.Printf("Showing last %d of %d transfers\n", historyLast, len(entries))
	} else {
		fmt.Printf("%d transfers total\n", len(entries))
	}
}

// truncateHistoryStr shortens a string to maxLen, adding "..." if truncated.
func truncateHistoryStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func init() {
	historyCmd.Flags().BoolVar(&historyClear, "clear", false, "Clear all transfer history")
	historyCmd.Flags().IntVar(&historyLast, "last", 0, "Show only the N most recent entries")
	rootCmd.AddCommand(historyCmd)
}
