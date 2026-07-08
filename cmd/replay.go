package cmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prathmeshsarda/hop/pkg/tunnel"
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

	// Read the active tunnel state
	state, err := tunnel.ReadStatic()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Println()
		fmt.Println("No active tunnel session found.")
		fmt.Println("Start a tunnel first with 'hop http <port>'")
		fmt.Println(strings.Repeat("─", 50))
		os.Exit(1)
	}

	if replayList {
		// List all buffered requests
		if len(state.Entries) == 0 {
			fmt.Println("Replay buffer is empty — no requests captured yet.")
			fmt.Println(strings.Repeat("─", 50))
			return
		}

		fmt.Printf("Tunnel: localhost:%d → %s\n", state.Port, state.PublicURL)
		fmt.Println()
		fmt.Println("Buffered Requests:")
		fmt.Println()
		fmt.Println("  #   Timestamp            Method  Path                      Status     Latency")
		fmt.Println("  ──  ──────────────────── ──────  ────────────────────────  ──────────  ───────")

		for i, entry := range state.Entries {
			ts, _ := time.Parse(time.RFC3339, entry.Timestamp)
			timestamp := ts.Format("2006-01-02 15:04:05")
			method := padReplayString(entry.Method, 6)
			path := padReplayString(entry.Path, 25)
			status := fmt.Sprintf("%d %s", entry.StatusCode, entry.StatusText)
			status = padReplayString(status, 10)
			latency := fmt.Sprintf("%dms", entry.LatencyMs)

			nth := len(state.Entries) - i // Most recent = 1
			fmt.Printf("  %-3d %s  %s  %s  %s  %s\n",
				nth, timestamp, method, path, status, latency)
		}

		fmt.Println()
		fmt.Printf("%d requests buffered (use 'hop replay --last N' to replay)\n", len(state.Entries))
	} else {
		// Replay a specific request
		if len(state.Entries) == 0 {
			fmt.Println("Replay buffer is empty — no requests to replay.")
			fmt.Println(strings.Repeat("─", 50))
			return
		}

		if replayLast < 1 || replayLast > len(state.Entries) {
			fmt.Fprintf(os.Stderr, "Error: request #%d not found (buffer has %d entries)\n",
				replayLast, len(state.Entries))
			fmt.Println(strings.Repeat("─", 50))
			os.Exit(1)
		}

		// Get the entry to replay (entries are oldest-first, --last N is Nth most recent)
		entryIdx := len(state.Entries) - replayLast
		entry := state.Entries[entryIdx]

		ordinal := "most recent"
		if replayLast > 1 {
			ordinal = fmt.Sprintf("#%d most recent", replayLast)
		}

		fmt.Printf("Replaying %s request to localhost:%d...\n", ordinal, state.Port)
		fmt.Printf("  %s %s\n", entry.Method, entry.Path)
		fmt.Println()

		// Fire the replay request
		targetURL := fmt.Sprintf("http://localhost:%d%s", state.Port, entry.Path)

		var bodyReader io.Reader
		if entry.BodyBase64 != "" {
			bodyReader = bytes.NewReader([]byte(entry.BodyBase64))
		}

		req, err := http.NewRequest(entry.Method, targetURL, bodyReader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error building request: %v\n", err)
			os.Exit(1)
		}

		// Copy original headers
		for k, v := range entry.Headers {
			lower := strings.ToLower(k)
			if lower == "host" || lower == "connection" {
				continue
			}
			req.Header.Set(k, v)
		}
		req.Header.Set("X-Hop-Replay", "true")

		client := &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		start := time.Now()
		resp, err := client.Do(req)
		latency := time.Since(start)

		if err != nil {
			fmt.Fprintf(os.Stderr, "Replay failed: %v\n", err)
			fmt.Println()
			fmt.Println("Make sure your local server is running on the specified port.")
			fmt.Println(strings.Repeat("─", 50))
			os.Exit(1)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)

		statusText := http.StatusText(resp.StatusCode)
		fmt.Printf("✓ Replay complete\n")
		fmt.Printf("  Response: %d %s\n", resp.StatusCode, statusText)
		fmt.Printf("  Latency:  %s\n", formatReplayLatency(latency))

		// Compare with original
		if entry.StatusCode != resp.StatusCode {
			fmt.Printf("  ⚠ Status changed: was %d %s, now %d %s\n",
				entry.StatusCode, entry.StatusText,
				resp.StatusCode, statusText)
		} else {
			fmt.Printf("  ✓ Status matches original (%d %s)\n",
				entry.StatusCode, entry.StatusText)
		}
	}

	fmt.Println(strings.Repeat("─", 50))
}

// padReplayString pads or truncates a string to the given width.
func padReplayString(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

// formatReplayLatency formats a duration for display.
func formatReplayLatency(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func init() {
	replayCmd.Flags().IntVar(&replayLast, "last", 1, "Replay the Nth most recent request")
	replayCmd.Flags().BoolVar(&replayList, "list", false, "List all buffered requests")
	rootCmd.AddCommand(replayCmd)
}
