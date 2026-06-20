package tui

import (
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// RenderQR generates a QR code as terminal-printable lines using Unicode
// block characters. The QR code is rendered using half-block characters
// (▀, ▄, █, and space) to achieve 2 rows per character height.
func RenderQR(url string) []string {
	qr, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return []string{"[QR code generation failed]"}
	}

	qr.DisableBorder = false
	bitmap := qr.Bitmap()

	rows := len(bitmap)
	cols := 0
	if rows > 0 {
		cols = len(bitmap[0])
	}

	var lines []string

	// Process two rows of the bitmap at a time using half-block characters.
	// Top half = upper row, bottom half = lower row.
	// ▀ = top black, bottom white
	// ▄ = top white, bottom black
	// █ = both black
	// " " = both white
	for y := 0; y < rows; y += 2 {
		var sb strings.Builder
		sb.WriteString("  ") // Left padding for centering

		for x := 0; x < cols; x++ {
			topBlack := bitmap[y][x]
			bottomBlack := false
			if y+1 < rows {
				bottomBlack = bitmap[y+1][x]
			}

			switch {
			case topBlack && bottomBlack:
				sb.WriteRune('█')
			case topBlack && !bottomBlack:
				sb.WriteRune('▀')
			case !topBlack && bottomBlack:
				sb.WriteRune('▄')
			default:
				sb.WriteRune(' ')
			}
		}

		lines = append(lines, sb.String())
	}

	return lines
}
