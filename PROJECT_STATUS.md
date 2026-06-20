# Hop — Project Status

> **Last updated:** 2026-06-21  
> **Current milestone:** 3 of 12 complete  
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

**Tests:** 26 passing (crypto: 10, token: 5, transfer: 8, transfer misc: 3)  
**Static analysis:** `go vet` clean

---

## 🔲 Remaining Milestones

| # | Milestone | What it covers |
|---|-----------|---------------|
| 4 | **Minimal Relay Service** | Deploy `relay/` package — session auth (Ed25519 + JWT), rate limiting, basic data bridge |
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
│   ├── tui/          # Terminal UI (progress, tunnel, QR)
│   └── history/      # Transfer history log
└── relay/            # (not started — Milestone 4)
```

---

## 🚀 Where to Start Next

**Begin with Milestone 4: Minimal Relay Service.**

1. Create the `relay/` package with its own `main.go` entry point
2. Implement session authentication (ephemeral Ed25519 + JWT)
3. Build the basic data bridge (Tier 3 relay streaming)
4. Add rate limiting and abuse prevention
5. Deploy to a cloud server and test with two machines on different networks

The relay is the backbone — once it works, Milestones 5–10 all build on top of it.
