# Hop — Project Status

> **Last updated:** 2026-07-14  
> **Current milestone:** 11 of 12 complete  
> **Branch:** `master`

---

## What is Hop?

A single-binary Go CLI for **direct peer-to-peer file transfers** and **localhost web tunneling**. Full spec in [`hop_product_specification_v2.1.md`](./hop_product_specification_v2.1.md).

---

## ✅ Completed Milestones

### Milestone 1 — CLI Skeleton
All Cobra commands are wired and functional:
- `hop share <file|dir>` — validates target, generates token, renders QR + TUI
- `hop get <token>` — validates token format, shows acceptance prompt
- `hop http <port>` — validates port, renders tunnel monitor with QR
- `hop replay` — `--list` and `--last N` flags
- `hop completion <shell>` — bash/zsh/fish/powershell
- `hop version` — prints version, Go version, OS/arch, protocol

### Milestone 2 — TUI Progress Engine
- `pkg/tui/renderer.go` — ANSI in-place rendering (no terminal scrolling)
- `pkg/tui/progress.go` — progress bar with sliding-window speed, ETA, tier icons
- `pkg/tui/tunnel.go` — tunnel monitor (pipes, request count, replay buffer, log)
- `pkg/tui/qr.go` — Unicode half-block QR codes in terminal

### Milestone 3 — Crypto & Core Packages
- `pkg/crypto/keys.go` — X25519 key pair generation + HKDF-SHA256 key derivation
- `pkg/crypto/cipher.go` — ChaCha20-Poly1305 AEAD with sequential nonces
- `pkg/crypto/integrity.go` — CRC-32 per-chunk + SHA-256 streaming file hash
- `pkg/token/token.go` — `word-word-NN` token generator (~36 bits entropy)
- `pkg/transfer/chunker.go` — 1 MB fixed-buffer file reader with seek (resume)
- `pkg/transfer/ratelimit.go` — token bucket bandwidth limiter
- `pkg/protocol/message.go` — length-prefixed binary wire format (18 message types)
- `pkg/protocol/version.go` — `HOP/1.0` versioning + feature flag negotiation
- `pkg/history/history.go` — append-only transfer log at `~/.hop/history.log`

### Milestone 4 — Minimal Relay Service
- `relay/auth.go` — Ed25519 + JWT session authentication (24h ephemeral sessions)
- `relay/registry.go` — In-memory token → session registry with 24h auto-expiry
- `relay/bridge.go` — WebSocket bidirectional data bridge (sender ↔ relay ↔ receiver)
- `relay/ratelimit.go` — Per-IP rate limiting (5 conns/IP, 10 lookups/min, 5-min ban)
- `relay/server.go` — HTTP server with logging, recovery & rate-limit middleware
- `relay/main.go` — Standalone relay binary (`--addr`, `--tls`, `--cert`, `--key`)
- `pkg/relay/client.go` — Relay client library (auth, register/join, message send/receive)

### Milestone 5 — Tier 3 Relay Transfers
- `pkg/transfer/engine.go` — Full sender/receiver orchestration (handshake → offer → encrypted chunks → verify)
- `pkg/protocol/handshake.go` — HOP_HELLO key exchange, TRANSFER_ACCEPT, TRANSFER_COMPLETE payloads
- `pkg/config/config.go` — Relay URL configuration via `HOP_RELAY` env var
- `cmd/share.go` — Wired to relay: auth → register → encrypted chunk streaming → SHA-256 verify → history log
- `cmd/get.go` — Wired to relay: auth → join → acceptance prompt → decryption → CRC-32/SHA-256 verify → history log
- Both commands: Ctrl+C graceful shutdown with `TRANSFER_CANCEL` signaling
- E2E encrypted: X25519 ECDH → HKDF-SHA256 → ChaCha20-Poly1305 per-chunk encryption
- Integrity: CRC-32 per chunk + SHA-256 full-file verification
- Protocol handshake with version compatibility check and feature flag negotiation

### Milestone 6 — NAT Hole Punching (Tier 2)
- `relay/signal.go` — WebSocket signaling endpoint for peer address exchange
- `pkg/protocol/signaling.go` — `PEER_INFO` and `PUNCH_SIGNAL` message encoding
- `pkg/network/punch.go` — UDP NAT hole punch coordination (3 probes, 5s timeout)
- `pkg/network/transport.go` — `UDPTransport` implementing fragmentation and stop-and-wait reliability
- `pkg/network/connector.go` — Tier waterfall connection negotiation (P2P fallback to Relay)
- `cmd/share.go` & `cmd/get.go` — Integrated tier negotiation and transport abstraction

### Milestone 7 — LAN Fast-Path (Tier 1)
- `pkg/protocol/lan.go` — `LAN_PROBE` and `LAN_RESPONSE` wire format (UDP broadcast discovery packets)
- `pkg/network/lan.go` — UDP broadcast LAN discovery engine (500ms timeout, 100ms probe interval)
- `pkg/network/lantransport.go` — `TCPTransport` implementing `transfer.Transport` over raw TCP
- `pkg/network/connector.go` — Full 3-tier waterfall: Tier 1 (LAN) → Tier 2 (P2P) → Tier 3 (Relay)
- `cmd/share.go` & `cmd/get.go` — Integrated LAN discovery with role-based coordination

### Milestone 8 — Chunk-Level Resume
- `pkg/protocol/resume.go` — `ResumeRequest` and `ResumeAccept` wire format (SHA-256 prefix, offset, chunk index, partial hash)
- `pkg/transfer/resume.go` — `.hop-resume-<sha256-prefix>` JSON marker files with atomic writes, detection, and cleanup
- `pkg/transfer/engine.go` — Sender: parse resume offset from TRANSFER_ACCEPT, seek chunker, skip encryptor nonces
- `pkg/transfer/engine.go` — Receiver: detect partial files via markers, open in append mode, re-hash for streaming SHA-256, periodic marker updates, cleanup on completion
- `pkg/crypto/cipher.go` — `SkipNonces(n)` for deterministic nonce advancement on resume
- `cmd/get.go` — `--resume` flag wired into engine with resume progress display
- `cmd/share.go` — `OnResumeDetected` callback for sender-side resume notification

### Milestone 9 — Browser Bridge HTTP Server
- `relay/browser.go` — BrowserBridge handler (HTML download page + file streaming to browser)
- `relay/routes.go` — Token-aware HTTP routing (distinguishes API routes from transfer tokens)
- `relay/server.go` — Integrated BrowserBridge + TokenRouter into relay server
- `relay/registry.go` — BrowserMode for multi-receiver token support
- `relay/bridge.go` — Auto-enable browser mode on token registration
- `pkg/protocol/browser.go` — `BrowserInfoResponse` payload + encode/decode
- `pkg/protocol/message.go` — 4 new message types: `BROWSER_INFO_REQ/RESP`, `BROWSER_DOWNLOAD_START/CANCEL`
- `pkg/transfer/engine.go` — `SendFileBrowserMode()` for serving browser downloads (plaintext chunks, no E2E encryption)
- `cmd/share.go` — Dual-mode sender: auto-detects CLI vs browser receiver and routes accordingly
- Browser download page: styled dark-mode HTML with file metadata, SHA-256 preview, and download button
- Abuse prevention: rate limiting, bandwidth cap (10 GB), connection limits all enforced for browser downloads
- No disk writes: all data flows through memory buffers only on the relay

**Tests:** 85 passing (crypto: 10, token: 5, protocol: 12, transfer engine: 6, transfer resume: 11, transfer misc: 11, relay server: 21, relay client: 7, build: 2)  
**Static analysis:** `go vet` clean

### Milestone 10 — Full Tunnel Suite
- `pkg/protocol/tunnel.go` — Tunnel wire format: `TunnelRegister`, `TunnelHTTPRequest`, `TunnelHTTPResponse` encode/decode
- `pkg/protocol/message.go` — Added `MsgTunnelRegister` (0x43) and `MsgTunnelClose` (0x44) message types
- `relay/tunnel.go` — Relay-side tunnel server: WebSocket registration, public HTTP proxying, request ID correlation, slug cooldown
- `relay/password.go` — bcrypt password verification helper
- `relay/routes.go` — `/t/<slug>/<path>` routing to TunnelServer, `/tunnel` WebSocket endpoint
- `relay/server.go` — TunnelServer integration, `/tunnel` route, health endpoint with tunnel count
- `pkg/tunnel/tunnel.go` — Client-side tunnel engine: relay auth → register → concurrent HTTP proxy loop
- `pkg/tunnel/replay.go` — In-memory ring buffer for request capture (configurable depth + body size caps)
- `pkg/tunnel/replay_store.go` — IPC via `~/.hop/tunnel.json` for cross-process `hop replay` communication
- `pkg/relay/client.go` — `RegisterTunnel()` method using `/tunnel` WebSocket endpoint
- `cmd/http.go` — Full `hop http <port>` wiring: slug generation, bcrypt password, relay connection, TUI monitor, Ctrl+C shutdown
- `cmd/replay.go` — Full `hop replay` wiring: reads state file, lists/replays requests, status comparison
- Browser-accessible password protection with styled dark-mode HTML prompt
- Concurrent request handling with request ID correlation between relay and client
- 30-second slug cooldown after disconnect (per spec §7.3)
- In-flight requests get 502 Bad Gateway on tunnel disconnect

**Tests:** 85 passing (all existing tests pass, no regressions)  
**Static analysis:** `go vet` clean

### Milestone 11 — Directory + Compression
- `pkg/archive/archive.go` — tar.gz packaging: `PackDirectory()` walks directory tree, creates streaming gzip archive; `UnpackArchive()` extracts with zip-slip traversal prevention
- `pkg/archive/archive_test.go` — 7 tests: round-trip, empty dir, non-dir, nonexistent, IsArchive, cleanup, unpack error
- `pkg/compress/zstd.go` — zstd chunk compression/decompression: pooled `Compressor`/`Decompressor` wrappers over `klauspost/compress`
- `pkg/compress/zstd_test.go` — 5 tests + 2 benchmarks: round-trip, incompressible data, empty data, sequential reuse, invalid data
- `pkg/transfer/engine.go` — Compression integrated into chunk pipeline: compress→CRC-32→encrypt→send / receive→decrypt→CRC-32→decompress→write; auto-unpack directory archives on `TRANSFER_COMPLETE` when `IsDir` is true
- `cmd/share.go` — Directory packaging: `hop share ./dir/` creates temp tar.gz, transfers it, cleans up; `IsDir` flag set in transfer offer; history logs with `./dirname/ (tar)` format
- `cmd/get.go` — Directory auto-unpack on receive; displays incoming directory indicator; history logs with `(tar)` suffix
- `go.mod` — Added `github.com/klauspost/compress v1.19.0` for pure-Go zstd
- Symlinks skipped during packaging (security: prevents symlink attacks)
- Path traversal prevention on unpack (zip-slip protection)

**Tests:** 97 passing (85 existing + 12 new archive/compress tests, no regressions)  
**Static analysis:** `go vet` clean

---

## 🔲 Remaining Milestones

| # | Milestone | What it covers |
|---|-----------|---------------|
| 12 | **Polish & DX** | Shell completions wiring, transfer history integration, `hop version` update check, graceful shutdown, final E2E tests |

---

## 📁 Project Structure

```
hop/
├── main.go
├── cmd/
│   ├── root.go, share.go, get.go
│   ├── http.go, replay.go, completion.go
├── pkg/
│   ├── config/       # Relay URL and env configuration
│   ├── crypto/       # X25519, ChaCha20, CRC-32, SHA-256
│   ├── token/        # Transfer token generation
│   ├── transfer/     # File chunker, rate limiter, transfer engine, resume markers, browser mode
│   ├── protocol/     # Wire format, versioning, handshake, resume, LAN discovery, browser bridge, tunnel
│   ├── network/      # LAN discovery, P2P hole punch, TCP/UDP transports
│   ├── relay/        # Relay client library
│   ├── tui/          # Terminal UI (progress, tunnel, QR)
│   ├── tunnel/       # Tunnel engine, replay buffer, IPC state store
│   ├── history/      # Transfer history log
│   ├── archive/      # tar.gz directory packaging and extraction
│   └── compress/     # zstd streaming chunk compression
└── relay/            # Standalone relay server binary
    ├── main.go       # Entry point (--addr, --tls flags)
    ├── server.go     # HTTP server + middleware
    ├── auth.go       # Ed25519 + JWT session auth
    ├── registry.go   # Token → session mapping (with browser mode)
    ├── bridge.go     # WebSocket data bridge
    ├── browser.go    # Browser bridge (download page + file streaming)
    ├── tunnel.go     # Tunnel server (registration, HTTP proxying, password auth)
    ├── password.go   # bcrypt password verification
    ├── routes.go     # Token-aware + tunnel-aware HTTP routing
    ├── signal.go     # WebSocket signaling for P2P hole punch
    └── ratelimit.go  # Per-IP abuse prevention
```

---

## 🚀 Where to Start Next

**Begin with Milestone 12: Polish & Developer Experience.**

1. Wire shell completions for bash/zsh/fish/PowerShell.
2. Integrate transfer history log entries with `hop version`.
3. Add optional GitHub release update check.
4. Add graceful shutdown signal handling across all commands.
5. Final end-to-end testing across all tiers.

Milestone 11 is complete — directory sharing via tar.gz packaging and zstd streaming compression are fully operational.
