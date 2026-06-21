# Hop — Project Status

> **Last updated:** 2026-06-21  
> **Current milestone:** 4 of 12 complete  
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

**Tests:** 54 passing (crypto: 10, token: 5, transfer: 8, relay server: 21, relay client: 7, transfer misc: 3)  
**Static analysis:** `go vet` clean

---

## 🔲 Remaining Milestones

| # | Milestone | What it covers |
|---|-----------|---------------|
| 5 | **Tier 3 Fallback** | End-to-end relay transfers between two machines, transfer acceptance, protocol handshake, token security |
| 6 | **NAT Hole Punching (Tier 2)** | Signaling server, UDP punch coordination (3 attempts, 5s timeout) |
| 7 | **LAN Fast-Path (Tier 1)** | UDP broadcast probe (500ms timeout), local network detection |
| 8 | **Chunk-Level Resume** | CRC-32 chunk fingerprints, `.hop-resume-*` marker files, resume negotiation |
| 9 | **Browser Bridge** | HTTPS relay for browser downloads, abuse prevention controls |
| 10 | **Full Tunnel Suite** | HTTPS termination, request replay inspector (buffer + body caps), password protection (bcrypt) |
| 11 | **Directory + Compression** | tar.gz packaging, zstd streaming compression (`--compress`), bandwidth throttling (`--limit`) |
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
│   ├── crypto/       # X25519, ChaCha20, CRC-32, SHA-256
│   ├── token/        # Transfer token generation
│   ├── transfer/     # File chunker, rate limiter
│   ├── protocol/     # Wire format, versioning
│   ├── relay/        # Relay client library
│   ├── tui/          # Terminal UI (progress, tunnel, QR)
│   └── history/      # Transfer history log
└── relay/            # Standalone relay server binary
    ├── main.go       # Entry point (--addr, --tls flags)
    ├── server.go     # HTTP server + middleware
    ├── auth.go       # Ed25519 + JWT session auth
    ├── registry.go   # Token → session mapping
    ├── bridge.go     # WebSocket data bridge
    └── ratelimit.go  # Per-IP abuse prevention
```

---

## 🚀 Where to Start Next

**Begin with Milestone 5: Tier 3 Fallback (Production Relay Transfers).**

1. Wire `cmd/share.go` and `cmd/get.go` to use `pkg/relay/client.go`
2. Implement the full transfer flow: offer → accept → encrypted chunk streaming → completion
3. Add protocol version handshake over the relay connection
4. Implement transfer acceptance prompt with file metadata preview
5. Add token security: entropy validation, expiry, one-time consumption
6. Test end-to-end with two separate machines on different networks

The relay infrastructure is live — Milestone 5 connects the CLI to it for real file transfers.
