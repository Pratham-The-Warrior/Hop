# hop v2.1 — Product Vision & Specification Manual
*Focused Scope Layout — A detailed, plain-English blueprint for direct, peer-to-peer file transfers and rock-solid localhost web tunneling.*

---

## Document Metadata
- **Document Release:** v2.1 (Revised & Hardened Portfolio Blueprint)
- **Core Mechanics:** File Sharing Engine & HTTPS Localhost Relay
- **Development Language:** Go (Golang) — Single Binary Execution
- **Document Type:** Comprehensive Plain-English Product Requirements Document (PRD)
- **Previous Revision:** v2.0 — Scope & vision established
- **This Revision:** v2.1 — Protocol specifics, security hardening, operational clarity, and quality-of-life enhancements

---

## 1. Vision

**hop** is a single-binary command-line application built to restore direct, peer-to-peer (P2P) data movement between machines. It cleanly addresses two universal, everyday problems without demanding any router configuration, premium cloud account management, or third-party storage costs:

1. **Direct File Transmission:** Sending a file (or directory) straight to an intended recipient without an upload-then-download round trip, ensuring no temporary files ever sit on an external third-party disk asset.
2. **Localhost Web Tunneling:** Exposing a local web server running on your computer directly to the open internet, allowing a website, mock layout, or API running on a local port to be tested, demoed, or hit by an automated live webhook from anywhere in the world.

Both problems exist across the internet for the exact same underlying reason: modern home and office internet routers are built to aggressively block incoming connection requests by default. `hop` resolves this architectural barrier using an automated, multi-tier connectivity strategy. This guarantees that the user never has to log into their router dashboard, adjust a firewall setting, or touch port forwarding rules.

This utility is deliberately scoped to focus entirely on these two core features. Every structural design choice, piece of logic, and optimization outlined within this document exists specifically to make these two tools exceptionally reliable and performant. It intentionally strips away generic features to double down on engineering excellence.

---

## 2. Scope Decision

Keeping the feature set tightly constrained is an intentional, strategic engineering decision. A completely polished, fully working, two-feature utility serves as a drastically stronger portfolio piece and interview demonstrator than a sprawling, half-broken utility weighed down by five or six complex sub-systems.

### In Scope
*Core Practical Goals & Capabilities Served:*
- **Direct CLI-to-CLI Transfers:** Securely streaming large files directly between two active terminal screens with end-to-end encryption keys.
- **Directory & Multi-File Support:** Transparently packaging a directory into a compressed tar archive before transfer and unpacking it on the receiver's side.
- **Browser Bridge:** Allowing non-technical users to download shared files using a plain web browser with zero terminal installation required.
- **Localhost Web Tunneling:** Forwarding incoming internet traffic directly to a local computer port, including mandatory HTTPS support.
- **Core Connectivity Foundations:** The shared 3-tier connection engine, memory-efficient safety loops, and tamper-proof security protocols that run both systems.
- **Developer Quality-of-Life:** Shell completions, bandwidth throttling, transfer history, and on-the-fly compression.

### Explicitly Out of Scope
*Engineering and Structural Reasons for Exclusion:*
- **Cooperative Swarm / Multi-Peer Distribution:** Demands complex firewall punching between every single matching peer pair, alongside unresolved, messy encryption key distribution challenges. It adds massive development bloat without serving either primary core objective.
- **Terminal Clipboard / Text Pipe:** While simple to write, it does not cleanly align with the core vision of moving files and tunneling local websites. It acts as a cheap feature add-on rather than a core structural pillar.
- **Custom Subdomain Registration:** Introduces a massive domain name management problem, requiring database persistence, user accounts, and cleanup logic. Both file sharing and localhost tunneling use random, temporary text slugs (e.g., `hop.to/t/bright-moon-7`) that are perfectly sufficient for everyday use.
- **Generic TCP Tunneling:** Unnecessarily broad. Scoping the local network tunnel strictly to standard HTTP/HTTPS web traffic completely covers the true, practical engineering use cases (web development reviews and receiving active webhooks).

---

## 3. Feature 1: Direct File Sharing

### 3.1 CLI-to-CLI Transfer
This represents the baseline operational case for `hop`: both the sender and the receiver are running the application inside their respective terminal environments.

**On the Sender's machine:**
```bash
hop share family_vacation.mp4
```

**On the Receiver's machine:**
```bash
hop get summer-surf-14
```

The underlying file data streams directly between the two physical computers using whichever connection tier successfully establishes the path (detailed in Section 5). Because both endpoints are controlled by the native `hop` application binary, the data payload is fully encrypted end-to-end. The sender and receiver execute an X25519 Diffie-Hellman key exchange at connection time; no intermediate party, routing server, or internet network observer can ever derive the master encryption key.

### 3.2 Transfer Acceptance
Before a transfer begins, the receiver is shown a preview of what they are about to download and must explicitly accept:

```text
$ hop get summer-surf-14

Incoming file: family_vacation.mp4 (2.00 GB)
From: 192.168.1.42 via LAN
Accept transfer? [Y/n]
```

This serves two purposes: it prevents unexpected large downloads from consuming disk space, and it provides a layer of social authentication — the receiver can verify the filename and size match what the sender told them to expect. A `--yes` flag is available to bypass the confirmation prompt for scripted or automated workflows:

```bash
hop get summer-surf-14 --yes
```

### 3.3 Browser Bridge
In everyday real-world scenarios, the person receiving a file is often a non-technical client, family member, or coworker who does not have `hop` installed and does not know how to interact with a terminal screen. The **Browser Bridge** addresses this friction effortlessly.

```bash
hop share my_large_file.zip
# Output generated:
# https://hop.to/summer-surf-14
```

The recipient simply clicks the generated web link inside any standard browser (such as Safari, Chrome, or Edge). The file download begins instantly with no browser plugins, terminal installs, or account registrations required.

#### Important Architectural Security Trade-Off:
Because a standard stock web browser cannot run `hop`'s customized low-level key exchange protocols, a Browser Bridge transfer cannot be fully zero-knowledge end-to-end. The traffic is securely encrypted using HTTPS in transit from the sender to the cloud relay, and encrypted from the relay to the browser.

This means the data payload becomes momentarily visible in plaintext inside the cloud relay's volatile memory at the exact physical intersection point where it translates the P2P custom protocol into the browser's standard HTTPS response format. CLI-to-CLI transfers never have this exposure. To maintain absolute transparency, the application terminal must explicitly display this distinction to the user:

> *"Notice: Browser links are securely encrypted in transit; CLI-to-CLI transfers are fully end-to-end encrypted."*

### 3.4 Directory & Multi-File Transfers
Users frequently need to share entire folders rather than individual files. When `hop share` receives a directory path as its argument, it transparently compresses the directory into a `.tar.gz` archive before streaming:

```bash
hop share ./my-project/
# Output:
# Packaging directory 'my-project/' (142 files, 38 MB)...
# Token: green-river-22
# https://hop.to/green-river-22
```

On the receiver's side, `hop get` detects the incoming payload is an archive and automatically unpacks it into a directory with the original folder name. The archive is treated as an ephemeral transport wrapper — neither the sender nor receiver ever sees or interacts with a raw `.tar.gz` file.

### 3.5 Resumable Transfers
In real-world network environments, large file transfers frequently face random disruptions — a laptop lid closes, a home Wi-Fi signal drops momentarily, or a public network router resets. `hop` gracefully survives these failures by implementing a robust chunk-level resume protocol rather than forcing the user to restart a massive download from zero.

This stability mechanism relies on three strict design rules:
- **Chunk-Level Checksums:** Files are sliced into distinct chunks. Each chunk receives a CRC-32 fingerprint. The receiver verifies this fingerprint upon arrival to confirm the chunk is uncorrupted.
- **Persistent Transfer Offset:** The receiver keeps a running tally of exactly how many bytes have been successfully verified and written to disk. This offset is persisted to a small temporary file on disk (`.hop-resume-<sha256-prefix>`) alongside the partial download, ensuring the resume state survives process restarts, laptop sleep cycles, and system reboots.
- **Resume Flow Detection:** When a user runs `hop get summer-surf-14 --resume`, or when the system detects a matching `.hop-resume-*` marker file on disk, it automatically passes the saved byte position back to the sender. The sender instantly skips ahead to that offset and resumes streaming the remaining chunks seamlessly. Once the transfer completes successfully, the marker file is deleted.

### 3.6 Full-File Integrity Verification
After all chunks have been received and reassembled, `hop` performs a final SHA-256 hash comparison of the complete file. The sender computes and transmits the full-file hash at the start of the session; the receiver independently computes the hash of the assembled output and compares:

```text
✓ Transfer complete. SHA-256 verified: a3f2b8...c91d
```

This catches any edge-case corruption that per-chunk CRC-32 might miss (e.g., chunk reordering bugs, off-by-one writes) and gives the user a cryptographically strong guarantee that the received file is bit-for-bit identical to the original.

### 3.7 QR Code Display
When a user is sharing a file, link, or layout intended for a mobile smartphone or tablet, manually typing a randomized text URL onto a touch screen is annoying and slow. To optimize the practical workflow, `hop` automatically renders a clean, scannable QR code directly inside the terminal window using basic text blocks, alongside the standard text link. This adds massive interactive value during live demonstrations with minimal underlying code complexity.

### 3.8 On-the-Fly Compression
For compressible file types (source code, text logs, CSV datasets), `hop` supports optional streaming compression to dramatically improve throughput on bandwidth-constrained connections:

```bash
hop share large_dataset.csv --compress
```

When enabled, data chunks are compressed using **zstd** (Zstandard) before encryption and transmission. The receiver transparently decompresses each chunk after decryption. For already-compressed formats (`.zip`, `.mp4`, `.jpg`), the flag has minimal effect and can be safely omitted. Compression is opt-in rather than automatic to avoid wasting CPU cycles on incompressible binary payloads.

### 3.9 Bandwidth Throttling
To prevent `hop` from saturating a user's internet connection (particularly important during video calls or when sharing a connection), an optional speed limit flag is available:

```bash
hop share big_file.iso --limit 5MB/s
```

This is implemented as a simple token-bucket rate limiter on the chunk write loop. Both `hop share` and `hop get` support the `--limit` flag independently.

---

## 4. Feature 2: Localhost Web Tunnel

### 4.1 Core Tunnel Mechanics
The localhost tunnel instantly projects a website project running on your private laptop out onto the open internet through a secure public endpoint address.

```bash
hop http 3000
# Output generated:
# https://hop.to/t/bright-moon-7
```

The tunnel URL uses the same random slug system as file sharing — no subdomain registration, no database, no cleanup logic. Any incoming internet visitor hitting that public URL has their request captured by the public cloud relay server. The relay packs the request up, slides it down the active tunnel line to your laptop, collects your website's local response data, and streams it back to the visitor's screen in real time.

### 4.2 Mandatory HTTPS Support
HTTPS encryption is treated as a core requirement, not an optional bonus feature. The dominant real-world use case for a local web tunnel is testing webhooks from modern platform systems (like Stripe payment alerts, Twilio SMS trackers, or GitHub repository triggers).

For strict security reasons, almost all modern web APIs completely refuse to send webhook notifications to an unencrypted `http://` address. An HTTP-only tunneling utility fails its primary practical job out of the box. Therefore, the public cloud relay component of `hop` must handle active SSL/TLS termination, providing every tunnel with a valid, secure HTTPS address instantly.

### 4.3 Request Replay (The Testing Inspector)
When debugging an incoming webhook connection (for example, analyzing a payment notification payload sent from Stripe), making a minor code fix on your laptop normally requires you to log into the Stripe dashboard and manually trigger the event all over again.

`hop` avoids this tedious step by implementing a lightweight local **Request Replay** engine. The application continuously holds the last **50** web requests directly inside your laptop's memory, subject to a **1 MB maximum body size** per captured request (headers are always retained; bodies exceeding the cap are truncated with a warning). Both the buffer depth and the body size cap are configurable:

```bash
hop http 3000 --replay-buffer 100 --replay-max-body 5MB
```

By running a simple command in a secondary terminal window:

```bash
hop replay          # Replays the most recent request
hop replay --last 3 # Replays the 3rd most recent request
hop replay --list   # Lists all buffered requests with timestamps
```

The application takes the exact captured network header and data payload, re-packages it locally, and fires it directly back at your `localhost:3000` port. This allows you to rapidly test, adjust, and debug your backend code loops instantly without needing the original outside provider to fire another live event. This delivers immense developer utility without the bloat of building an entire complex web dashboard UI.

### 4.4 Access Password Protection
When demoing a work-in-progress web application to a client, you often want the link to be public enough for them to access, but protected enough to prevent random web crawlers, bots, or strangers from stumbling upon your unfinished layout. `hop` handles this by providing an optional shared-password flag:

```bash
hop http 3000 --password "secret-demo-2026"
```

When active, the cloud relay server intercepts any visitor hitting your tunnel link and forces them to enter the matching password before allowing any traffic to pass through down to your local machine. The password is transmitted to the relay as a bcrypt hash — the relay never stores or sees the plaintext password.

---

## 5. Connectivity Strategy (The 3-Tier Layer)

Both features within `hop` use the exact same underlying network connection logic. When a request is initialized, the binary attempts three distinct connection tiers in sequential order, optimizing for speed first and falling back to reliability. The active tier is displayed prominently in the terminal interface so the user always knows how their data is moving.

> **The Honest Engineering Reality:**
> *Tiers 1 and 2 are built strictly as speed optimizations to keep data transfers fast, free, and private. Tier 3 is the ultimate fallback mechanism that guarantees the connection successfully completes 100% of the time, regardless of network restrictions.*

### Tier 1: Local Network Check (LAN Fast-Path)
**Timeout: 500ms**

The application first issues a rapid, silent UDP broadcast probe to see if both devices are sharing the exact same local Wi-Fi router or office area network. If a matching `hop` instance responds within 500ms, the application immediately routes all traffic directly across local airwaves at maximum hardware speeds, bypassing the external internet entirely.

*The Architectural Caveat:* This local path will fail on public coffee shop Wi-Fi networks or strict corporate offices that have **Client/AP Isolation** enabled. This isolation setting intentionally stops local wireless devices from seeing or talking to each other for safety. Therefore, Tier 1 must always be treated as a fast-path optimization attempt, never as a guaranteed connection.

### Tier 2: NAT Hole Punching (Coordinated Direct Knock)
**Timeout: 5 seconds (3 punch attempts, ~1.5s apart)**

If the devices are in different locations, Tier 1 fails. `hop` immediately transitions to Tier 2. Both machines establish a brief connection to the public cloud signaling server simultaneously to swap their external public ports and internet addresses. On an exact synchronized signal, both machines fire a UDP packet at each other's router at the same moment, tricking the firewalls into opening up direct point-to-point pathways. The system attempts this coordinated knock up to 3 times before giving up.

*The Architectural Caveat:* While this works flawlessly across standard home routers, it will reliably fail when dealing with **Symmetric NAT** configurations (which are highly common on enterprise corporate firewalls and university networks). Symmetric NAT randomizes out-of-bound ports for every new connection, rendering the swapped connection details obsolete before the punch can land.

### Tier 3: The Cloud Relay Fallback (The Universal Guarantee)
**Timeout: None — always succeeds**

If direct hole-punching fails to link the devices within 5 seconds, `hop` stops trying to optimize and routes all traffic through its final safety net. Both computers drop direct connection attempts and establish stable outbound TLS connections to the public cloud relay node.

The relay node acts as a secure, live conveyor belt pipeline, receiving data blocks from the sender and passing them down to the receiver instantly. Because both firewalls happily allow outbound traffic to a public server, this tier bypasses all router restrictions globally, ensuring the application always works.

### Tier Visibility
The active connection tier is always displayed in the terminal interface:

```text
Connection: ⚡ Direct (LAN)           # Tier 1 — fastest
Connection: 🔗 Direct (P2P)           # Tier 2 — fast, no relay cost
Connection: ☁️  Relayed                 # Tier 3 — guaranteed
```

This transforms an invisible internal optimization into a demonstrable, visible feature.

---

## 6. Security Model Overview

### 6.1 Cryptographic Choices
`hop` uses well-established, modern cryptographic primitives throughout:

| Component | Algorithm | Purpose |
|---|---|---|
| Key Exchange | X25519 (Curve25519 Diffie-Hellman) | Derive shared secret between sender and receiver |
| Symmetric Encryption | ChaCha20-Poly1305 (AEAD) | Encrypt and authenticate each data chunk |
| Chunk Integrity | CRC-32 | Fast per-chunk corruption detection during transit |
| File Integrity | SHA-256 | Full-file verification after transfer completion |
| Password Hashing | bcrypt | Hash tunnel access passwords before relay transmission |

These choices prioritize both security strength and performance on the kinds of devices `hop` targets. ChaCha20-Poly1305 in particular is significantly faster than AES-GCM on hardware without AES-NI instructions (common on ARM devices like Raspberry Pi).

### 6.2 Per-Mode Security Guarantees
`hop` implements distinct security protocols mapped to the specific user workflow chosen, ensuring complete clarity regarding data exposure limits:

- **CLI-to-CLI Transfers:** Fully zero-knowledge and end-to-end encrypted. The X25519 key exchange occurs directly between the two edge terminal nodes inside the established pipeline. The cloud relay server sees nothing but scrambled digital noise and cannot read a single byte. Every data chunk carries a Poly1305 authentication tag; any manipulation causes an instant connection teardown.
- **Browser Bridge Transfers:** Utilizes standard HTTPS encryption between the browser and the relay, and an encrypted protocol link between the relay and the sender's terminal. The relay acts as the physical translator between these two connections, meaning file data enters the relay's volatile RAM in plaintext during transmission. To protect user privacy, the relay service is forbidden from ever writing file blocks to a persistent hard disk under any circumstances.
- **Localhost Tunnel Requests:** All web requests and responses are passed through the cloud relay over secure TLS paths. The cloud relay is strictly forbidden from logging or storing request bodies, response payloads, or header data. The Request Replay feature is handled and stored entirely inside the user's local machine memory, keeping the cloud node completely clean.

### 6.3 Token Security
Transfer tokens (e.g., `summer-surf-14`) are the primary addressing mechanism for connecting senders and receivers. To prevent enumeration and brute-force attacks:

- **Minimum Entropy:** Tokens are generated from a dictionary of 4,096 common English words combined with a 2-digit numeric suffix, producing a minimum of **~36 bits of entropy** per token (e.g., `word-word-NN`). This makes blind guessing impractical within the token's lifetime.
- **Rate Limiting:** The relay server enforces a strict rate limit of **10 token lookup attempts per minute per IP address**. Exceeding this limit results in a temporary 5-minute IP ban from the lookup endpoint.
- **Automatic Expiry:** Tokens expire automatically **24 hours** after creation, or immediately when the sender disconnects — whichever comes first. Expired tokens return a generic "not found" response indistinguishable from a non-existent token.
- **One-Time Consumption:** For CLI-to-CLI transfers, a token is consumed (invalidated) after the first successful receiver connects. Browser Bridge tokens remain active until the sender disconnects, allowing multiple downloads.

---

## 7. Relay Service Architecture

### 7.1 Client-Relay Authentication
Every `hop` CLI instance authenticates with the relay using a lightweight session handshake:

1. The CLI generates an ephemeral Ed25519 key pair at startup.
2. It sends the public key to the relay alongside a protocol version identifier.
3. The relay issues a short-lived **session token** (JWT, 24-hour expiry) bound to that public key.
4. All subsequent messages from the CLI to the relay are signed with the ephemeral private key and carry the session token. The relay verifies both before processing.

This prevents unauthenticated clients from registering tokens, impersonating senders, or flooding the relay with garbage traffic. No user accounts or registration flows are required — authentication is automatic and invisible.

### 7.2 Abuse Prevention
The relay is a publicly accessible service that streams arbitrary data. Without protections, it could be trivially abused. The following safeguards are enforced:

- **Connection Limits:** Maximum **5 concurrent active transfers** per IP address. Excess connections are queued and rejected after a 30-second wait.
- **Bandwidth Cap:** Each individual transfer session is capped at **10 GB** of total relay throughput. Transfers exceeding this limit are terminated with a clear error message. (Direct P2P tiers have no cap since they don't consume relay resources.)
- **Session Timeout:** Any relay session idle for more than **5 minutes** (no data flowing) is automatically torn down, and its token is released.
- **No Persistent Storage:** The relay service never writes transferred data to disk under any circumstances. All data flows through memory buffers only.
- **Request Size Limits:** Browser Bridge and tunnel HTTP requests are capped at **100 MB** per individual request body to prevent memory exhaustion attacks.

### 7.3 Graceful Shutdown & Cleanup
Clear, deterministic behavior is defined for all disconnection scenarios:

| Scenario | Behavior |
|---|---|
| **Sender Ctrl+C mid-transfer** | Relay immediately sends a `TRANSFER_CANCELLED` signal to the receiver. The receiver retains the partial file and its `.hop-resume-*` marker for future resume attempts. |
| **Receiver disconnects mid-transfer** | Sender is notified and enters a "waiting for reconnection" state for 60 seconds. If the receiver reconnects with a resume offset within that window, the transfer continues seamlessly. After 60 seconds, the sender's session is released. |
| **Tunnel disconnects** | Relay immediately stops accepting new requests for that tunnel's slug. Any in-flight HTTP requests receive a `502 Bad Gateway` response. The slug is released for reuse after a 30-second cooldown. |
| **Relay server restarts** | All active sessions are lost. CLI clients detect the broken connection and automatically attempt to re-register their token with the relay within 10 seconds using exponential backoff. |

---

## 8. Protocol Versioning

To ensure forward compatibility as `hop` evolves, every connection begins with a **protocol version handshake**:

1. The initiating client sends a `HOP_HELLO` message containing its protocol version (e.g., `HOP/1.0`) and supported feature flags.
2. The receiving side (peer or relay) responds with its own version and the **negotiated feature set** (intersection of both sides' capabilities).
3. If the versions are incompatible (e.g., major version mismatch), the connection is rejected with a clear error message: `"Error: peer is running hop protocol v2.x; please upgrade with 'hop update'."`

The protocol version is a simple `major.minor` integer pair. Minor version differences are always backward-compatible (new optional features). Major version changes indicate breaking protocol changes and require both sides to upgrade.

---

## 9. Performance & Memory Architecture

To prevent system slow-downs or out-of-memory crashes, `hop` enforces a strict **Bounded Streaming Memory Architecture** under the hood.

When reading or writing files, the application allocates a small, fixed-size buffer pool capped at **1 Megabyte per chunk**. The software reads a single 1 MB chunk from the local disk, optionally compresses it, encrypts it with ChaCha20-Poly1305, passes it to the network socket channel, and immediately recycles that buffer slot before picking up the next chunk.

Because memory is continuously recycled rather than stacked up, the application's RAM footprint stays completely flat. Whether a user is moving a tiny 5 MB image or a massive 50 GB system backup, `hop` continuously consumes less than **50 Megabytes** of total system memory. This allows the application to run comfortably on low-powered, resource-constrained devices like a Raspberry Pi without any risk of system freeze.

> **Why 1 MB chunks instead of smaller?** A 50 GB file at 32 KB chunks would produce 1.6 million chunks, each requiring a separate checksum, encryption pass, authentication tag, and network syscall — creating enormous per-chunk overhead that bottlenecks throughput. At 1 MB, the same file is only ~50,000 chunks, reducing overhead by 32x while still keeping total memory well under the 50 MB ceiling.

---

## 10. Terminal User Interface Layouts

To preserve a highly clean, polished feel, both core features utilize a fixed, in-place-updating layout structure. The application avoids clogging up your terminal screen history by using standard carriage-return line-clear commands (`\r` and `\033[K`) to redraw statistics on the exact same lines.

### 10.1 Live File Sharing — Sender Screen
```text
hop
--------------------------------------------------
sharing 'family_vacation.mp4' (2.00 GB)
Token: summer-surf-14
Link:  https://hop.to/summer-surf-14
Connection: ⚡ Direct (LAN)

[========================>------------] 64%
Speed: 12.5 MB/s  |  Progress: 1.28 GB / 2.00 GB
Time: 57s remaining
--------------------------------------------------
```

### 10.2 Live File Sharing — Receiver Acceptance
```text
hop
--------------------------------------------------
Incoming file: family_vacation.mp4 (2.00 GB)
From: 192.168.1.42 via LAN
SHA-256: a3f2b8...c91d

Accept transfer? [Y/n] _
--------------------------------------------------
```

### 10.3 Live File Sharing — Transfer Complete
```text
hop
--------------------------------------------------
sharing 'family_vacation.mp4' (2.00 GB)
Token: summer-surf-14
Connection: ⚡ Direct (LAN)

[====================================] 100%
Speed: 14.2 MB/s  |  2.00 GB / 2.00 GB
✓ Transfer complete. SHA-256 verified: a3f2b8...c91d
--------------------------------------------------
```

### 10.4 Localhost Tunnel Monitor Terminal Screen
```text
hop
--------------------------------------------------
tunneling localhost:3000
Public URL: https://hop.to/t/bright-moon-7
Status: Connected (☁️ Relayed)

Active Pipes: 2    |    Total Requests: 24
Replay Buffer: 18/50 requests captured
--------------------------------------------------
[Log] GET  /api/v1/users          --> 200 OK    (12ms)
[Log] POST /api/v1/stripe/webhook --> 200 OK    (32ms)
```

---

## 11. Developer Quality-of-Life Features

### 11.1 Shell Completions
`hop` leverages Cobra's built-in completion generation to provide tab-completion support for all major shells:

```bash
hop completion bash    # Output bash completion script
hop completion zsh     # Output zsh completion script
hop completion fish    # Output fish completion script
hop completion powershell  # Output PowerShell completion script
```

Users add the output to their shell profile for persistent completions. This is near-zero implementation effort and adds immediate polish.

### 11.2 Transfer History
`hop` maintains a lightweight, append-only log of all completed transfers at `~/.hop/history.log`:

```text
2026-06-20 14:32  SENT  family_vacation.mp4   2.0 GB  summer-surf-14  LAN    1m42s  ✓
2026-06-20 15:01  RECV  report.pdf            4.2 MB  blue-wave-8     Relay  3s     ✓
2026-06-20 16:45  SENT  ./my-project/ (tar)   38 MB   green-river-22  P2P    8s     ✓
```

Each entry records: timestamp, direction, filename, size, token, connection tier, duration, and verification status. This provides a useful audit trail and adds a polished, professional feel. The log file is plain text and can be inspected with standard unix tools (`cat`, `grep`, `tail`).

### 11.3 Version & Update Check
```bash
hop version
# hop v1.0.0 (go1.23, linux/amd64)
# Protocol: HOP/1.0
```

The `hop version` command displays the application version, Go compiler version, target platform, and protocol version. On release builds, it optionally checks GitHub releases for newer versions and prints a one-line upgrade notice if available.

---

## 12. Technical Architecture & Folder Structure

The project is written in Go (Golang) for clean execution speeds, utilizing the industry-standard **Cobra** library to drive the command-line menus. The folder layout is organized as follows:

```text
hop/
├── go.mod                # Go module settings
├── main.go               # Application main entry point
├── cmd/                  # Command-line input handlers
│   ├── root.go           # Cobra base setup + version command
│   ├── share.go          # Logic for 'hop share' and 'hop get'
│   ├── http.go           # Logic for 'hop http' local tunneling
│   ├── replay.go         # Logic for 'hop replay' request replay
│   └── completion.go     # Shell completion generation
├── pkg/                  # Internal core logic packages
│   ├── crypto/           # X25519 key exchange, ChaCha20 encryption, SHA-256 integrity
│   ├── network/          # Multi-tier connection engine and hole-punching logic
│   ├── protocol/         # Wire format, version negotiation, message framing
│   ├── transfer/         # Chunking, compression, resume state, rate limiting
│   ├── tui/              # In-place terminal interface drawing
│   └── history/          # Transfer history logging
└── relay/                # SEPARATE SERVICE: Deployed cloud signaling + relay
    ├── signal.go         # Handles address swapping for hole punching
    ├── relay.go          # Manages Tier 3 data streaming pathways
    ├── bridge.go         # Translates P2P streams into standard HTTP/HTTPS
    ├── auth.go           # Session token issuance and verification
    └── ratelimit.go      # Token lookup throttling and abuse prevention
```

> **Critical Infrastructure Reminder:**
> *The `relay/` code represents a completely separate deployable service component from the main user CLI tool binary. It requires its own dedicated cloud hosting environment, high-uptime monitoring, and active SSL/TLS certificates to manage secure web browser handshakes. It is a genuine piece of cloud infrastructure, not an afterthought.*

---

## 13. The 12-Step Sequential Roadmap

To ensure a smooth, manageable development cycle, build the application following this strict chronological roadmap. Each step produces an independent, fully demoable checkpoint useful for maintaining motivation and providing concrete milestones for technical interviews:

1. **Milestone 1: The CLI Skeleton.** Initialize your Go modules and build out the basic Cobra command structure. Ensure typing `hop share`, `hop get`, `hop http`, `hop replay`, `hop version`, and `hop completion` correctly processes input flags and prints out clean text stubs.
2. **Milestone 2: The Local Progress Engine.** Build a mock data loop that simulates moving a file on your machine. Perfect the carriage-return interface trick so the block loading bar updates fluidly on a static row without scrolling. Include the connection tier display line.
3. **Milestone 3: The Encryption Core.** Write the core security package. Implement X25519 key exchange, ChaCha20-Poly1305 AEAD chunk encryption, CRC-32 per-chunk checksums, and SHA-256 full-file verification in isolated, testable functions.
4. **Milestone 4: The Minimal Relay Service.** Code and deploy a basic version of the `relay/` package onto a cloud server instance. Implement session authentication (ephemeral Ed25519 + JWT) and basic rate limiting. Enable the service to act as a basic data bridge.
5. **Milestone 5: Production Tier-3 Fallback.** Connect two separate computers on different networks to your cloud relay. Successfully stream a large file between them through the relay, achieving a working baseline system. Implement transfer acceptance, protocol version handshake, and token security (entropy, expiry, rate limiting).
6. **Milestone 6: NAT Hole Punching (Tier 2).** Add signaling logic to your cloud server. Instruct the edge computers to exchange return addresses and coordinate simultaneous UDP knocks (3 attempts, 5-second timeout) to establish a direct P2P link, bypassing the relay.
7. **Milestone 7: Local LAN Fast-Path (Tier 1).** Add a rapid UDP broadcast probe (500ms timeout) to detect if both computers share a local network. If detected, skip external internet routes and move data over local channels.
8. **Milestone 8: Chunk-Level Resuming.** Implement CRC-32 chunk fingerprints, disk-persisted resume offset files (`.hop-resume-*`), and the resume negotiation flow. Start a file transfer, abruptly kill the process mid-way, and demonstrate running with `--resume` to pick up exactly where you left off — including after a full system reboot.
9. **Milestone 9: The Browser Bridge HTTP Server.** Upgrade your cloud relay to listen for standard HTTPS traffic. Connect a browser to the relay link, and let the relay pull the data chunks from your terminal to feed the web download. Implement the relay abuse prevention controls.
10. **Milestone 10: Complete Local Tunnel Suite.** Finalize the tunneling mechanics. Implement mandatory HTTPS termination on the relay, integrate the in-memory Request Replay inspector (with buffer depth and body size caps), and add the access password flag (bcrypt hashed).
11. **Milestone 11: Directory Support & Compression.** Add automatic tar.gz packaging for directory arguments. Implement optional zstd streaming compression with the `--compress` flag. Add bandwidth throttling with the `--limit` flag.
12. **Milestone 12: Polish & Developer Experience.** Wire up shell completions for bash/zsh/fish/PowerShell. Implement the transfer history log. Add `hop version` with optional update check. Add graceful shutdown signal handling with proper cleanup across all commands. Final end-to-end testing across all tiers.
