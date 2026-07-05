package main

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prathmeshsarda/hop/pkg/protocol"
)

const (
	// BrowserRequestTimeout is the max time to wait for the sender to respond
	// to a browser info or download request.
	BrowserRequestTimeout = 30 * time.Second

	// MaxBrowserDownloadSize is the max individual HTTP request/response body
	// for browser downloads (per spec Section 7.2: 100 MB per request body).
	// The 10 GB bandwidth cap is enforced separately via the session total.
	MaxBrowserDownloadSize int64 = 100 * 1024 * 1024 * 1024 // Effectively unlimited per-download; bandwidth cap handles the rest
)

// BrowserBridge handles HTTP requests from web browsers for file downloads.
// It communicates with the sender's CLI via their existing WebSocket connection
// to retrieve file metadata and stream file data.
type BrowserBridge struct {
	registry *Registry
	limiter  *RateLimiter
	logger   *log.Logger
}

// NewBrowserBridge creates a new BrowserBridge.
func NewBrowserBridge(registry *Registry, limiter *RateLimiter, logger *log.Logger) *BrowserBridge {
	return &BrowserBridge{
		registry: registry,
		limiter:  limiter,
		logger:   logger,
	}
}

// HandleInfo serves the HTML download page for a token.
// It requests file metadata from the sender and renders a styled page.
func (bb *BrowserBridge) HandleInfo(w http.ResponseWriter, r *http.Request, token string) {
	ip := extractIP(r)

	// Rate limit: count as a lookup
	if !bb.limiter.AllowLookup(ip) {
		http.Error(w, "Too many requests — please try again later.", http.StatusTooManyRequests)
		return
	}

	// Look up the token
	entry := bb.registry.Lookup(token)
	if entry == nil {
		bb.serveNotFound(w)
		return
	}

	// Get the sender's connection
	sender := bb.registry.GetSender(token)
	if sender == nil {
		bb.serveNotFound(w)
		return
	}

	// Send BrowserInfoReq to the sender
	infoReqMsg := &protocol.Message{
		Type:    protocol.MsgBrowserInfoReq,
		Payload: nil,
	}
	infoReqData := protocol.Encode(infoReqMsg)

	ctx, cancel := context.WithTimeout(r.Context(), BrowserRequestTimeout)
	defer cancel()

	if err := sender.Send(ctx, infoReqData); err != nil {
		bb.logger.Printf("[browser] failed to send info request for token %s: %v", token, err)
		http.Error(w, "Sender is not available.", http.StatusBadGateway)
		return
	}

	// Wait for BrowserInfoResp from the sender
	respData, err := sender.Receive(ctx)
	if err != nil {
		bb.logger.Printf("[browser] failed to receive info response for token %s: %v", token, err)
		http.Error(w, "Sender did not respond.", http.StatusBadGateway)
		return
	}

	// Parse the wire-format response: [4 len][1 type][payload]
	if len(respData) < protocol.HeaderSize {
		bb.logger.Printf("[browser] info response too short for token %s: %d bytes", token, len(respData))
		http.Error(w, "Invalid response from sender.", http.StatusBadGateway)
		return
	}

	msgType := protocol.MessageType(respData[4])
	if msgType != protocol.MsgBrowserInfoResp {
		bb.logger.Printf("[browser] unexpected message type from sender for token %s: %s", token, msgType)
		http.Error(w, "Unexpected response from sender.", http.StatusBadGateway)
		return
	}

	payload := respData[protocol.HeaderSize:]
	info, err := protocol.DecodeBrowserInfoResponse(payload)
	if err != nil {
		bb.logger.Printf("[browser] failed to decode info response for token %s: %v", token, err)
		http.Error(w, "Failed to read file info.", http.StatusBadGateway)
		return
	}

	bb.logger.Printf("[browser] serving info page for token %s: %s (%d bytes)", token, info.FileName, info.FileSize)

	// Render the download page
	bb.serveDownloadPage(w, token, info)
}

// HandleDownload streams the file data to the browser.
// It tells the sender to start streaming chunks, decrypts them (since browser
// bridge doesn't use E2E encryption), and writes plaintext to the HTTP response.
func (bb *BrowserBridge) HandleDownload(w http.ResponseWriter, r *http.Request, token string) {
	ip := extractIP(r)

	// Rate limit: count as a connection
	if !bb.limiter.AllowConnection(ip) {
		http.Error(w, "Too many concurrent downloads from your IP.", http.StatusTooManyRequests)
		return
	}
	defer bb.limiter.ReleaseConnection(ip)

	// Look up the token
	entry := bb.registry.Lookup(token)
	if entry == nil {
		bb.serveNotFound(w)
		return
	}

	// Get the sender's connection
	sender := bb.registry.GetSender(token)
	if sender == nil {
		bb.serveNotFound(w)
		return
	}

	// First, request file info so we can set Content-Length and Content-Disposition
	infoReqMsg := &protocol.Message{
		Type:    protocol.MsgBrowserInfoReq,
		Payload: nil,
	}
	infoReqData := protocol.Encode(infoReqMsg)

	ctx, cancel := context.WithTimeout(r.Context(), BrowserRequestTimeout)
	defer cancel()

	if err := sender.Send(ctx, infoReqData); err != nil {
		bb.logger.Printf("[browser] download: failed to send info request for token %s: %v", token, err)
		http.Error(w, "Sender is not available.", http.StatusBadGateway)
		return
	}

	respData, err := sender.Receive(ctx)
	if err != nil {
		bb.logger.Printf("[browser] download: failed to receive info response for token %s: %v", token, err)
		http.Error(w, "Sender did not respond.", http.StatusBadGateway)
		return
	}

	if len(respData) < protocol.HeaderSize {
		http.Error(w, "Invalid response from sender.", http.StatusBadGateway)
		return
	}

	msgType := protocol.MessageType(respData[4])
	if msgType != protocol.MsgBrowserInfoResp {
		http.Error(w, "Unexpected response from sender.", http.StatusBadGateway)
		return
	}

	info, err := protocol.DecodeBrowserInfoResponse(respData[protocol.HeaderSize:])
	if err != nil {
		http.Error(w, "Failed to read file info.", http.StatusBadGateway)
		return
	}

	// Now tell the sender to start streaming
	startMsg := &protocol.Message{
		Type:    protocol.MsgBrowserDownloadStart,
		Payload: protocol.EncodeBrowserDownloadStart(),
	}
	startData := protocol.Encode(startMsg)

	// Use a long-running context for the actual download (not the 30s timeout)
	dlCtx, dlCancel := context.WithCancel(r.Context())
	defer dlCancel()

	if err := sender.Send(dlCtx, startData); err != nil {
		bb.logger.Printf("[browser] download: failed to send download start for token %s: %v", token, err)
		http.Error(w, "Failed to start download.", http.StatusBadGateway)
		return
	}

	// Set response headers before streaming
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, info.FileName))
	if info.FileSize > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.FileSize, 10))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Flush headers
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	bb.logger.Printf("[browser] starting download stream for token %s: %s (%d bytes)", token, info.FileName, info.FileSize)

	// Stream chunks from sender to browser
	var totalBytes atomic.Int64
	for {
		// Check if the browser disconnected
		select {
		case <-dlCtx.Done():
			// Browser disconnected — notify sender
			cancelMsg := &protocol.Message{
				Type:    protocol.MsgBrowserDownloadCancel,
				Payload: protocol.EncodeBrowserDownloadCancel(),
			}
			cancelData := protocol.Encode(cancelMsg)
			_ = sender.Send(context.Background(), cancelData)
			bb.logger.Printf("[browser] download cancelled by browser for token %s (sent %d bytes)", token, totalBytes.Load())
			return
		default:
		}

		// Read next message from sender
		chunkData, err := sender.Receive(dlCtx)
		if err != nil {
			if dlCtx.Err() != nil {
				// Browser disconnected
				cancelMsg := &protocol.Message{
					Type:    protocol.MsgBrowserDownloadCancel,
					Payload: protocol.EncodeBrowserDownloadCancel(),
				}
				cancelData := protocol.Encode(cancelMsg)
				_ = sender.Send(context.Background(), cancelData)
			}
			bb.logger.Printf("[browser] download: read error for token %s: %v", token, err)
			return
		}

		if len(chunkData) < protocol.HeaderSize {
			bb.logger.Printf("[browser] download: message too short for token %s: %d bytes", token, len(chunkData))
			return
		}

		chunkMsgType := protocol.MessageType(chunkData[4])
		chunkPayload := chunkData[protocol.HeaderSize:]

		switch chunkMsgType {
		case protocol.MsgTransferComplete:
			// Download finished successfully
			bb.logger.Printf("[browser] download complete for token %s (total: %d bytes)", token, totalBytes.Load())
			return

		case protocol.MsgTransferCancel:
			bb.logger.Printf("[browser] download cancelled by sender for token %s", token)
			return

		case protocol.MsgChunkData:
			// Parse chunk header (16 bytes) + plaintext payload
			// In browser mode, the sender sends UNENCRYPTED chunks (no E2E encryption).
			if len(chunkPayload) < 16 {
				bb.logger.Printf("[browser] download: chunk too short for token %s: %d bytes", token, len(chunkPayload))
				return
			}

			// Skip the chunk header (16 bytes: index + size + CRC32)
			// and write the plaintext data directly to the browser
			plaintext := chunkPayload[16:]

			n, err := w.Write(plaintext)
			if err != nil {
				// Browser disconnected
				cancelMsg := &protocol.Message{
					Type:    protocol.MsgBrowserDownloadCancel,
					Payload: protocol.EncodeBrowserDownloadCancel(),
				}
				cancelData := protocol.Encode(cancelMsg)
				_ = sender.Send(context.Background(), cancelData)
				bb.logger.Printf("[browser] download: write error for token %s: %v", token, err)
				return
			}

			newTotal := totalBytes.Add(int64(n))

			// Enforce bandwidth cap (10 GB per session)
			if newTotal > MaxBandwidthPerSession {
				bb.logger.Printf("[browser] download: bandwidth cap exceeded for token %s (%d bytes)", token, newTotal)
				cancelMsg := &protocol.Message{
					Type:    protocol.MsgBrowserDownloadCancel,
					Payload: protocol.EncodeBrowserDownloadCancel(),
				}
				cancelData := protocol.Encode(cancelMsg)
				_ = sender.Send(context.Background(), cancelData)
				return
			}

			// Flush to browser periodically
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}

			// Send ACK back to sender so it sends the next chunk
			ackMsg := &protocol.Message{
				Type:    protocol.MsgChunkAck,
				Payload: chunkPayload[:16], // Echo back the chunk header as ACK
			}
			ackData := protocol.Encode(ackMsg)
			if err := sender.Send(dlCtx, ackData); err != nil {
				bb.logger.Printf("[browser] download: ACK send error for token %s: %v", token, err)
				return
			}

		default:
			bb.logger.Printf("[browser] download: unexpected message type %s for token %s", chunkMsgType, token)
			return
		}
	}
}

// serveDownloadPage renders the HTML download page with file metadata.
func (bb *BrowserBridge) serveDownloadPage(w http.ResponseWriter, token string, info *protocol.BrowserInfoResponse) {
	hashHex := fmt.Sprintf("%x", info.SHA256)
	hashPreview := hashHex[:8] + "…" + hashHex[len(hashHex)-8:]

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>hop — Download %s</title>
    <style>
        :root {
            --bg: #0a0a0f;
            --surface: #14141f;
            --border: #1e1e2e;
            --text: #e4e4ef;
            --text-dim: #7a7a8e;
            --accent: #6c5ce7;
            --accent-glow: rgba(108, 92, 231, 0.3);
            --green: #2ecc71;
            --orange: #f39c12;
        }
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: 'SF Mono', 'Fira Code', 'JetBrains Mono', 'Cascadia Code', monospace;
            background: var(--bg);
            color: var(--text);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            padding: 2rem;
        }
        .card {
            background: var(--surface);
            border: 1px solid var(--border);
            border-radius: 12px;
            padding: 2.5rem;
            max-width: 480px;
            width: 100%%;
            box-shadow: 0 4px 24px rgba(0,0,0,0.4);
        }
        .logo {
            font-size: 1.5rem;
            font-weight: 700;
            color: var(--accent);
            margin-bottom: 0.25rem;
            letter-spacing: -0.03em;
        }
        .tagline {
            font-size: 0.75rem;
            color: var(--text-dim);
            margin-bottom: 2rem;
        }
        .divider {
            border: none;
            border-top: 1px solid var(--border);
            margin: 1.5rem 0;
        }
        .meta { margin-bottom: 1.5rem; }
        .meta-row {
            display: flex;
            justify-content: space-between;
            padding: 0.5rem 0;
            border-bottom: 1px solid var(--border);
        }
        .meta-row:last-child { border-bottom: none; }
        .meta-label { color: var(--text-dim); font-size: 0.8rem; }
        .meta-value { font-size: 0.85rem; text-align: right; word-break: break-all; }
        .meta-value.filename { color: var(--green); font-weight: 600; }
        .download-btn {
            display: block;
            width: 100%%;
            padding: 0.9rem 1.5rem;
            background: var(--accent);
            color: white;
            border: none;
            border-radius: 8px;
            font-family: inherit;
            font-size: 0.95rem;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.2s ease;
            text-decoration: none;
            text-align: center;
            letter-spacing: 0.02em;
        }
        .download-btn:hover {
            background: #7c6cf7;
            box-shadow: 0 0 20px var(--accent-glow);
            transform: translateY(-1px);
        }
        .download-btn:active {
            transform: translateY(0);
        }
        .notice {
            margin-top: 1.25rem;
            padding: 0.75rem 1rem;
            background: rgba(243, 156, 18, 0.08);
            border: 1px solid rgba(243, 156, 18, 0.2);
            border-radius: 8px;
            font-size: 0.7rem;
            color: var(--orange);
            line-height: 1.5;
        }
        .token-badge {
            display: inline-block;
            padding: 0.2rem 0.6rem;
            background: rgba(108, 92, 231, 0.12);
            border: 1px solid rgba(108, 92, 231, 0.25);
            border-radius: 4px;
            font-size: 0.8rem;
            color: var(--accent);
            margin-bottom: 1.5rem;
        }
    </style>
</head>
<body>
    <div class="card">
        <div class="logo">hop</div>
        <div class="tagline">direct file transfer</div>
        <div class="token-badge">%s</div>
        <div class="meta">
            <div class="meta-row">
                <span class="meta-label">File</span>
                <span class="meta-value filename">%s</span>
            </div>
            <div class="meta-row">
                <span class="meta-label">Size</span>
                <span class="meta-value">%s</span>
            </div>
            <div class="meta-row">
                <span class="meta-label">SHA-256</span>
                <span class="meta-value">%s</span>
            </div>
        </div>
        <a href="/%s/download" class="download-btn">⬇ Download File</a>
        <div class="notice">
            ⚠ Browser downloads are encrypted in transit (HTTPS). CLI-to-CLI transfers are fully end-to-end encrypted.
        </div>
    </div>
</body>
</html>`,
		html.EscapeString(info.FileName),
		html.EscapeString(token),
		html.EscapeString(info.FileName),
		formatFileSize(info.FileSize),
		hashPreview,
		html.EscapeString(token),
	))

	w.Write(buf.Bytes())
}

// serveNotFound renders a styled 404 page.
func (bb *BrowserBridge) serveNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)

	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>hop — Not Found</title>
    <style>
        :root { --bg: #0a0a0f; --surface: #14141f; --border: #1e1e2e; --text: #e4e4ef; --text-dim: #7a7a8e; --accent: #6c5ce7; }
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: 'SF Mono', 'Fira Code', monospace;
            background: var(--bg); color: var(--text);
            min-height: 100vh; display: flex; align-items: center; justify-content: center;
        }
        .card {
            background: var(--surface); border: 1px solid var(--border);
            border-radius: 12px; padding: 2.5rem; max-width: 400px; width: 100%;
            text-align: center;
        }
        .logo { font-size: 1.5rem; font-weight: 700; color: var(--accent); margin-bottom: 1rem; }
        .code { font-size: 3rem; font-weight: 700; color: var(--text-dim); margin-bottom: 0.5rem; }
        .msg { color: var(--text-dim); font-size: 0.85rem; line-height: 1.6; }
    </style>
</head>
<body>
    <div class="card">
        <div class="logo">hop</div>
        <div class="code">404</div>
        <p class="msg">This transfer token was not found.<br>It may have expired or the sender may have disconnected.</p>
    </div>
</body>
</html>`))
}

// formatFileSize formats bytes into a human-readable string.
func formatFileSize(bytes int64) string {
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
