package tui

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh/terminal"
)

// Renderer handles in-place terminal rendering using ANSI escape codes.
// It tracks how many lines were last rendered so it can clear them before
// drawing the next frame, producing a flicker-free, non-scrolling update.
type Renderer struct {
	lastLineCount int
	termWidth     int
}

// NewRenderer creates a new terminal renderer.
func NewRenderer() *Renderer {
	width := 80 // sensible default
	if w, _, err := terminal.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}
	return &Renderer{
		termWidth: width,
	}
}

// Clear erases the previously rendered block by moving the cursor up
// and clearing each line.
func (r *Renderer) Clear() {
	if r.lastLineCount > 0 {
		// Move cursor up N lines and clear each one
		fmt.Printf("\033[%dA", r.lastLineCount)
		for i := 0; i < r.lastLineCount; i++ {
			fmt.Print("\033[2K") // Clear entire line
			if i < r.lastLineCount-1 {
				fmt.Print("\033[1B") // Move down one line
			}
		}
		// Move back to the top of the cleared block
		if r.lastLineCount > 1 {
			fmt.Printf("\033[%dA", r.lastLineCount-1)
		}
	}
}

// Render draws lines to the terminal and tracks line count for next Clear().
// Each string in lines becomes one terminal line. Lines longer than the terminal
// width are wrapped and counted as multiple lines.
func (r *Renderer) Render(lines []string) {
	r.Clear()

	totalLines := 0
	for _, line := range lines {
		fmt.Println(line)
		// Account for line wrapping
		visibleLen := visibleLength(line)
		if visibleLen == 0 {
			totalLines++
		} else {
			totalLines += (visibleLen-1)/r.termWidth + 1
		}
	}
	r.lastLineCount = totalLines
}

// RenderLine writes a single line with in-place carriage-return update.
// Useful for a single status line that updates repeatedly.
func (r *Renderer) RenderLine(line string) {
	fmt.Printf("\r\033[K%s", line)
}

// visibleLength returns the length of a string excluding ANSI escape sequences.
func visibleLength(s string) int {
	inEscape := false
	length := 0
	for _, ch := range s {
		if ch == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
				inEscape = false
			}
			continue
		}
		length++
	}
	return length
}

// FormatSeparator returns a horizontal separator line.
func FormatSeparator(width int) string {
	return strings.Repeat("─", width)
}
