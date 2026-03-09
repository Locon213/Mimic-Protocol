# Mimic Protocol

<div align="center">

**[🇷🇺 Русский](README.md) | [🇺🇸 English](README.en.md)**

**Mimic** is an open-source censorship circumvention protocol that constantly changes its "digital face" by mimicking the traffic of various legitimate services (VK, Rutube, Telegram, etc.).

[![License](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.25.5-00ADD8.svg)](https://golang.org)

</div>

---

## 🎯 Core Concept
Instead of just encrypting traffic (which is often flagged by DPI systems), Mimic disguises it as normal user activity.
1. **Polymorphism:** The protocol dynamically switches behavior profiles.
2. **Mimicry:** Traffic looks like video streaming, messaging, or social network scrolling.
3. **Elusiveness:** No consistent signature that can be easily blocked.

## 🛠️ Custom-Built Technologies

### MTP — Mimic Transport Protocol

**MTP** is a fully custom transport protocol over UDP, built from scratch as a TCP replacement for DPI evasion.

| Component | Description |
|-----------|-------------|
| **QUIC Masking** | Packets are fully disguised as HTTP/3 (QUIC Short Header). Server protected by Active Probing Defender (drops fake DNS replies to DPI scanners) |
| **Polymorphic Headers** | Smart padding dynamically expands packet size up to MTU to perfectly imitate video streaming. DPI cannot write a regex to intercept |
| **ChaCha20-Poly1305** | Each packet is individually encrypted. Retransmissions get fresh nonces |
| **ARQ Engine** | Reliable delivery: sliding window, Selective ACK, adaptive RTO (Jacobson/Karels) |
| **BBR Congestion Control** | Novel congestion management based on Bottleneck Bandwidth & Min RTT. Replaces legacy AIMD logic for maximum throughput |
| **Forward Error Correction**| Reed-Solomon based FEC transparently recovers lost UDP packets on the fly, eliminating lag spikes on unstable 4G networks |
| **Session Migration** | Seamless rotation: client migrates session to new UDP socket with zero data loss |
| **Keepalive** | Automatic PING/PONG every 5 seconds, dead connection detection |

#### How It Works

```
Client                                    Server
  │                                          │
  │──── SYN (AUTH:uuid, encrypted) ────────>│
  │<─── SYN-ACK (OK, encrypted) ───────────│
  │                                          │
  │──── DATA [junk][nonce][encrypted] ─────>│  (each packet looks different)
  │<─── ACK + SACK ────────────────────────│
  │                                          │
  │   === Rotation (seamless) ===            │
  │──── SYN+MIGRATE (session_id) ─────────>│  (new UDP socket)
  │<─── SYN-ACK ───────────────────────────│  (server swaps address)
  │                                          │  (yamux doesn't notice)
```

### MTP Polymorphic Packet

```
[ QUIC Header: 9 bytes ][ Padding: up to 1350 bytes ][ Nonce: 24 bytes ][ Encrypted(Header+Payload) ]
   ↑ Fake HTTP/3 prefix      ↑ Smart MTU Padding          ↑ unique                ↑ ChaCha20-Poly1305
      for DPI evasion          (size masking)            for packet
```

**No DPI can intercept this traffic** because:
- Every packet has a different size (junk padding)
- No fixed markers or magic bytes
- Even retransmissions of the same packet look completely different (new nonce + new padding)

## 🏗️ Repository Structure

```
Mimic-Protocol/
├── cmd/                # Executables
│   ├── client/         # CLI Client with SOCKS5 proxy
│   └── server/         # Server (MTP)
├── pkg/                # Public libraries
│   ├── mtp/            # ★ MTP — custom transport over UDP
│   ├── protocol/       # Protocol Core (TLS-mimicry, legacy)
│   ├── transport/      # VirtualConn + Manager (seamless rotation)
│   ├── proxy/          # SOCKS5 proxy server
│   ├── mimic/          # Traffic pattern generator
│   ├── presets/        # Behavior presets (social, video, messenger)
│   └── config/         # Configuration with validation
├── internal/           # Internal components
└── docs/               # Documentation
```

## 🔧 How It Works

### Basic Principle
1. The user defines a list of "allowed" domains (e.g., `vk.com`, `rutube.ru`).
2. Mimic establishes an **MTP connection** (UDP) to the server.
3. **yamux** runs on top of MTP for stream multiplexing.
4. The client provides a **SOCKS5 proxy** (`127.0.0.1:1080`) with full **UDP Associate** support (online games, DNS, WebRTC work over the tunnel).
5. A **Built-in Routing Engine** flexibly categorizes traffic (`direct`, `proxy`, `block`) via rules.
6. Every 30-600 seconds, a **seamless transport rotation** occurs.

### Configuration
Example `config.yaml` for client:

```yaml
server: "your-mimic-server.com:443"
uuid: "your-uuid-here"
local_port: 1080  # SOCKS5 proxy port (TCP/UDP)

# Routing Engine (Optional)
routing:
  default_policy: proxy
  rules:
    - type: domain_suffix
      value: ru
      policy: direct
    - type: ip_cidr
      value: 127.0.0.0/8
      policy: block

domains:
  - vk.com          # Preset "social"
  - rutube.ru       # Preset "video"
  - telegram.org    # Preset "messenger"

settings:
  switch_time: "60s-300s"   # Change profile every 1-5 minutes
  randomize: true           # Randomize domain switch order
```

## 📦 Go Dependencies
The project relies on the following powerful open-source libraries:
- **[hashicorp/yamux](https://github.com/hashicorp/yamux)** — Stream multiplexing over MTP.
- **[klauspost/reedsolomon](https://github.com/klauspost/reedsolomon)** — Blazing fast FEC implementation for packet loss recovery.
- **[refraction-networking/utls](https://github.com/refraction-networking/utls)** — TLS Fingerprint spoofing (browser mimicry).
- **[golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto/chacha20poly1305)** — Secure ChaCha20-Poly1305 encryption.
- **[google/uuid](https://github.com/google/uuid)** — UUID generation and parsing for authorization.
- **[go-yaml/yaml](https://github.com/go-yaml/yaml)** — Configuration file parsing.

## 🚀 Usage

### Build from source

```bash
# Clone
git clone https://github.com/Locon213/Mimic-Protocol.git
cd Mimic-Protocol

# Build server and client
go build -o server ./cmd/server
go build -o client ./cmd/client
```

### Generate UUID

```bash
./server generate-uuid
# Output: 550e8400-e29b-41d4-a716-446655440000
```

### Start Server

```bash
./server -config server.yaml
```

### Start Client

```bash
./client -config config.yaml
```

On successful connection, the client displays:
```
✅ Session established!
🌐 SOCKS5 Proxy: 127.0.0.1:1080
  ↑ 125.3 KB/s  ↓ 1.2 MB/s  │  Traffic: 45.6 MB  │  Connected: 00:15:32  │  Active: 3
```

## 🔐 Security
- **Transport:** MTP (custom protocol over UDP) with ChaCha20-Poly1305 encryption
- **Polymorphism:** Every packet is unique — DPI cannot create a signature
- **Key Exchange:** UUID-based authorization
- **Anonymity:** Server does not store logs, no registration required

## 📄 License
This project is distributed under the GPLv3 License. See [LICENSE](LICENSE) for details.

Copyright (c) 2025-present Locon213 & Contributors.