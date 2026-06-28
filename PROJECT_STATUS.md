# Hop — Project Status

> **Last updated:** 2026-06-28  
> **Current milestone:** 5 of 12 complete  
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

**Tests:** 66 passing (crypto: 10, token: 5, protocol: 7, transfer engine: 5, transfer misc: 11, relay server: 21, relay client: 7)  
**Static analysis:** `go vet` clean

---

## 🔲 Remaining Milestones

| # | Milestone | What it covers |
|---|-----------|---------------|
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
│   ├── config/       # Relay URL and env configuration
│   ├── crypto/       # X25519, ChaCha20, CRC-32, SHA-256
│   ├── token/        # Transfer token generation
│   ├── transfer/     # File chunker, rate limiter, transfer engine
│   ├── protocol/     # Wire format, versioning, handshake payloads
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

**Begin with Milestone 6: NAT Hole Punching (Tier 2 — Direct P2P).**

1. Implement a signaling server (or add signaling to the existing relay)
2. UDP hole punch coordination: both peers exchange public IP:port via signaling
3. Synchronized UDP punch attempts (3 tries, ~1.5s apart, 5s total timeout)
4. Fall back to Tier 3 relay if hole punching fails
5. Wire tier selection logic into the transfer engine

Milestone 5 is complete — `hop share` and `hop get` now perform real encrypted transfers via the relay. The next step is to add direct P2P connectivity to bypass the relay when possible.
