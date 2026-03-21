# Mimic Protocol

<div align="center">
  <img src="assets/logo.png" alt="Mimic Protocol Logo" width="200"/>

**[🇷🇺 Русский](README.md) | [🇺🇸 English](README.en.md)**

**Mimic** is an open-source censorship circumvention protocol that constantly changes its "digital face" by mimicking the traffic of various legitimate services (VK, Rutube, Telegram, etc.).

[![License](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.25.5-00ADD8.svg)](https://golang.org)
[![Go Report Card](https://goreportcard.com/badge/github.com/Locon213/Mimic-Protocol)](https://goreportcard.com/report/github.com/Locon213/Mimic-Protocol)
[![Go Reference](https://pkg.go.dev/badge/github.com/Locon213/Mimic-Protocol.svg)](https://pkg.go.dev/github.com/Locon213/Mimic-Protocol)

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
│   ├── mtp/            # ★ MTP — custom transport over UDP (ARQ, FEC, BBR)
│   ├── protocol/       # Protocol Core (TLS-mimicry, legacy)
│   ├── transport/      # VirtualConn + Manager (seamless rotation)
│   ├── proxy/          # SOCKS5 proxy server
│   ├── client/         # Mimic client with session management
│   ├── mimic/          # Traffic pattern generator
│   ├── presets/        # Behavior presets (social, video, messenger)
│   ├── config/         # Configuration with validation
│   ├── compression/    # Data compression (zstd)
│   ├── network/        # Network utilities (DNS, protected dialer)
│   ├── routing/        # Traffic routing engine
│   ├── tunnel/         # Traffic tunneling
│   └── version/        # Version information
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

## 📋 Configuration

> ⚠️ **Important:** The `goccy/go-yaml` library does not support comments in configuration files. When editing configs, remove comments (lines starting with `#`).

### Server Setup (`config.example.yaml`)

Create a configuration file from the example:

```bash
cp config.example.yaml server.yaml
nano server.yaml  # edit for your needs
```

#### Server Configuration Options

| Parameter | Type | Required | Description | Example |
|-----------|------|----------|-------------|---------|
| `port` | int | ❌ | MTP listening port (UDP). Default: `443` | `443`, `8443`, `8080` |
| `uuid` | string | ✅ | Unique UUID for client authentication | `"550e8400-e29b-41d4-a716-446655440000"` |
| `name` | string | ❌ | Server name (shown in logs and links) | `"My-Mimic-Server"` |
| `transport` | string | ❌ | Transport type: `"mtp"` (UDP, recommended) or `"tcp"` (legacy) | `"mtp"` |
| `domain_list` | []object | ❌ | Domains for traffic mimicry (with optional preset) | `[{"domain": "vk.com", "preset": "social"}]` |
| `max_clients` | int | ❌ | Maximum concurrent clients. `0` = unlimited | `100` |
| `dns` | string | ❌ | DNS server for domain resolution | `"1.1.1.1:53"` |
| `compression.enable` | bool | ❌ | Enable zstd compression. Default: `false` | `true`, `false` |
| `compression.level` | int | ❌ | Compression level (1-3): 1=Fastest, 2=Default, 3=Better | `2` |
| `compression.min_size` | int | ❌ | Minimum size for compression (bytes). Default: `64` | `64`, `128` |

```yaml
# Listening port (443 recommended for HTTPS masking)
port: 443

# Unique UUID for authentication (generate: ./server generate-uuid)
uuid: "550e8400-e29b-41d4-a716-446655440000"

# Server name
name: "My-Mimic-Server"

# Transport: "mtp" (UDP, recommended) or "tcp" (legacy)
transport: "mtp"

# Domains for traffic mimicry (with optional preset)
# Format: domain (auto-detect) or domain:preset (explicit)
domain_list:
  # Auto-detect preset by domain
  - vk.com                    # Auto: social
  - rutube.ru                 # Auto: video
  - telegram.org              # Auto: messenger
  - wikipedia.org             # Auto: web_generic
  
  # Explicit preset for specific domain
  - domain: "some-gaming-site.com"
    preset: "gaming"          # Gaming traffic for this domain
  
  - domain: "my-video-site.com"
    preset: "video"           # Video streaming for this domain

# Max clients (0 = unlimited)
max_clients: 100

# DNS server (optional)
dns: "1.1.1.1:53"

# Data compression (optional, disabled by default)
compression:
  enable: false  # true = enable zstd compression
  level: 2       # 1=Fastest, 2=Default, 3=Better
  min_size: 64   # Don't compress packets < 64 bytes
```

**Generate UUID:**
```bash
./server generate-uuid
```

**Generate client link:**
```bash
./server generate-link config.example.yaml
```

Example output:
```
🚀 Share this link with clients to connect:
================================================================
mimic://550e8400-e29b-41d4-a716-446655440000@your-server.com:443?name=My-Mimic-Server&domains=vk.com,rutube.ru&transport=mtp&dns=1.1.1.1:53
================================================================
```

### Client Setup (`config.yaml`)

#### Client Configuration Options

| Parameter | Type | Required | Description | Example |
|-----------|------|----------|-------------|---------|
| `server` | string | ✅ | Server address (IP:PORT or domain:PORT) | `"192.168.1.100:443"` |
| `uuid` | string | ✅ | UUID for authentication (must match server) | `"550e8400-e29b-41d4-a716-446655440000"` |
| `domains` | []object | ❌ | Domains for mimicry (with optional preset) | `[{"domain": "vk.com", "preset": "social"}]` |
| `transport` | string | ❌ | Transport type: `"mtp"` or `"tcp"` | `"mtp"` |
| `local_port` | int | ❌ | Local SOCKS5 proxy port. Default: `1080` | `1080` |
| `dns` | string | ❌ | DNS server for resolution | `"1.1.1.1:53"` |
| `compression.enable` | bool | ❌ | Enable zstd compression. Default: `false` | `true`, `false` |
| `compression.level` | int | ❌ | Compression level (1-3). Default: `2` | `1`, `2`, `3` |
| `compression.min_size` | int | ❌ | Minimum size for compression. Default: `64` | `64`, `128` |
| `custom_presets` | map | ❌ | Custom presets for domains (see below) | `{...}` |
| `proxies` | []object | ❌ | Local proxy list (see below) | `[{"type": "socks5", "port": 1080}]` |
| `routing.default_policy` | string | ❌ | Default policy: `proxy`, `direct`, `block` | `"proxy"` |
| `routing.rules` | []object | ❌ | Routing rules (see below) | `[...]` |
| `settings.switch_time` | string | ❌ | Profile switch interval (format: `"60s-300s"` or `"1m-5m"`) | `"60s-300s"` |
| `settings.randomize` | bool | ❌ | Random domain switch order | `true` |

#### Proxy Configuration (`proxies`)

The client can run multiple local proxies simultaneously.

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | Proxy type: `"socks5"` (with UDP support) or `"http"` |
| `port` | int | Listening port |

**Example proxies:**

```yaml
proxies:
  - type: "socks5"
    port: 1080
  - type: "http"
    port: 8080
```

#### Custom Presets (`custom_presets`)

The preset system allows you to define specific traffic behavior for different services.

**Built-in presets:**
- `web_generic` — Web browsing (500-1420 bytes, 10-150 PPS)
- `social` — Social networks (VK, Instagram)
- `video` — Video streaming (YouTube, Twitch)
- `messenger` — Messengers (Telegram, WhatsApp)
- `gaming` — Gaming (CS2, Dota 2) — small packets, high PPS
- `voip` — VoIP/video calls (Discord, Zoom) — symmetric traffic

**Example custom_presets:**

```yaml
custom_presets:
  # Gaming preset for Steam
  steampowered.com:
    name: "Gaming - Steam"
    type: "gaming"
    packet_size_min: 64
    packet_size_max: 512
    packets_per_sec_min: 30
    packets_per_sec_max: 120
    upload_download_ratio: 0.8
    session_duration: "600s-3600s"
  
  # VoIP preset for Discord
  discord.com:
    name: "VoIP - Discord"
    type: "voip"
    packet_size_min: 80
    packet_size_max: 300
    packets_per_sec_min: 20
    packets_per_sec_max: 50
    upload_download_ratio: 1.0
    session_duration: "300s-7200s"
  
  # Video preset for YouTube
  youtube.com:
    name: "Video - YouTube"
    type: "video"
    packet_size_min: 1000
    packet_size_max: 1450
    packets_per_sec_min: 50
    packets_per_sec_max: 200
    upload_download_ratio: 0.05
    session_duration: "300s-3600s"
```

**Preset selection priority:**
1. Custom presets (exact domain match)
2. Custom presets (by keyword)
3. Default presets (by domain mapping)
4. `web_generic` (default)

#### Routing Configuration (`routing`)

The built-in Routing Engine directs traffic based on rules.

**Policies:**
- `proxy` — route through Mimic tunnel
- `direct` — connect directly (bypass tunnel)
- `block` — block connection

**Rule types:**
- `domain_suffix` — match by domain suffix (e.g., `ru`, `org`)
- `domain_keyword` — match by keyword in domain
- `ip_cidr` — match by IP range (CIDR notation)

**Example routing:**

```yaml
routing:
  default_policy: proxy
  rules:
    - type: domain_suffix
      value: ru
      policy: direct
    - type: domain_suffix
      value: cn
      policy: block
    - type: ip_cidr
      value: 192.168.0.0/16
      policy: direct
    - type: domain_keyword
      value: google
      policy: proxy
```

#### Full Client Configuration Example

```yaml
server: "your-mimic-server.com:443"
uuid: "550e8400-e29b-41d4-a716-446655440000"
local_port: 1080

# Domains for mimicry (with optional preset)
# Format: domain (auto-detect) or domain:preset (explicit)
domains:
  # Auto-detect preset by domain
  - vk.com                    # Auto: social
  - rutube.ru                 # Auto: video
  - telegram.org              # Auto: messenger
  
  # Explicit preset for specific domain
  - domain: "some-gaming-site.com"
    preset: "gaming"          # Gaming traffic for this domain
  
  - domain: "my-video-site.com"
    preset: "video"           # Video streaming for this domain

transport: "mtp"
dns: "1.1.1.1:53"

# Data compression (optional)
compression:
  enable: false  # true = enable zstd compression
  level: 2       # 1=Fastest, 2=Default, 3=Better
  min_size: 64   # Don't compress packets < 64 bytes

# Custom presets
custom_presets:
  discord.com:
    type: "voip"
    packet_size_min: 80
    packet_size_max: 300
    packets_per_sec_min: 20
    packets_per_sec_max: 50

proxies:
  - type: "socks5"
    port: 1080
  - type: "http"
    port: 8080

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

settings:
  switch_time: "60s-300s"   # Change profile every 1-5 minutes
  randomize: true           # Randomize domain switch order
```

## 📦 Go Dependencies
The project relies on the following powerful open-source libraries:
- **[goccy/go-yaml](https://github.com/goccy/go-yaml)** — Fast YAML parser (10x faster than standard)
- **[hashicorp/yamux](https://github.com/hashicorp/yamux)** — Stream multiplexing over MTP.
- **[klauspost/reedsolomon](https://github.com/klauspost/reedsolomon)** — Blazing fast FEC implementation for packet loss recovery.
- **[refraction-networking/utls](https://github.com/refraction-networking/utls)** — TLS Fingerprint spoofing (browser mimicry).
- **[golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto/chacha20poly1305)** — Secure ChaCha20-Poly1305 encryption.
- **[google/uuid](https://github.com/google/uuid)** — UUID generation and parsing for authorization.
- **[klauspost/compress](https://github.com/klauspost/compress)** — High-performance data compression.

## 🚀 Usage

### ⚡ Quick Install on Linux (Automatic)

**Supported distributions:**
- 🐧 **Ubuntu/Debian** (apt)
- 🔴 **CentOS/RHEL** (yum/dnf)
- 🔵 **AlmaLinux/Rocky Linux** (dnf)
- 🟡 **Fedora** (dnf)
- 🟢 **Arch Linux/Manjaro** (pacman)
- 🔷 **openSUSE** (zypper)
- 🏔️ **Alpine Linux** (apk)

**Requirements:** root access

```bash
# 1. Clone repository
git clone https://github.com/Locon213/Mimic-Protocol.git
cd Mimic-Protocol

# 2. Run installer (as root)
sudo bash scripts/linux/install.sh

# 3. Done! Server is installed and running
```

The installer automatically:
- ✅ Detects your distribution and installs dependencies
- ✅ Downloads pre-built binary for your architecture (amd64/arm64/arm)
- ✅ Generates UUID and creates config at `/etc/mimic/server.yaml`
- ✅ Configures systemd service
- ✅ Enables auto-start on boot
- ✅ **Applies performance optimizations:**
  - BBR congestion control for maximum throughput
  - Increased network buffers
  - Optimized file descriptor limits
  - TCP Fast Open for reduced latency

#### Server Management via CLI

After installation, use the `mimic` command:

```bash
# Server management
mimic status-server      # Server status
mimic restart-server     # Restart
mimic stop-server        # Stop
mimic start-server       # Start

# Configuration
mimic generate-uuid      # Generate UUID
mimic generate-link      # Client connection link
mimic config-path        # Config file path
mimic edit-config        # Open config in editor

# Diagnostics
mimic logs               # Last 50 log lines
mimic logs-follow        # Real-time logs
mimic optimize-status    # System optimization status
mimic check-bbr          # Check BBR congestion control
mimic version            # Server version
```

#### Manual Firewall Configuration

```bash
# UFW (Ubuntu/Debian)
sudo ufw allow 443/udp
sudo ufw reload

# firewalld (CentOS/RHEL/Fedora/AlmaLinux/Rocky)
sudo firewall-cmd --permanent --add-port=443/udp
sudo firewall-cmd --reload

# iptables (universal)
sudo iptables -A INPUT -p udp --dport 443 -j ACCEPT
sudo iptables-save > /etc/iptables/rules.v4  # Debian/Ubuntu
# or
sudo service iptables save  # CentOS/RHEL
```

---

### 🔧 Manual Installation on Linux

If you prefer manual setup or want to build from source:

```bash
# 1. Install Go (choose your distribution)

# Ubuntu/Debian
sudo apt update && sudo apt install -y golang-go

# CentOS/RHEL/AlmaLinux/Rocky
sudo dnf install -y golang
# or for older versions:
sudo yum install -y golang

# Fedora
sudo dnf install -y golang

# Arch Linux/Manjaro
sudo pacman -S go

# openSUSE
sudo zypper install go

# Alpine Linux
apk add go

# 2. Build
git clone https://github.com/Locon213/Mimic-Protocol.git
cd Mimic-Protocol
go build -o server ./cmd/server
chmod +x server

# 3. Configure
cp config.example.yaml server.yaml  # or create your own
./server generate-uuid              # generate UUID and add to config

# 4. Run
./server -config server.yaml
```

---

### 🌐 Cross-Compilation

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o server ./cmd/server

# Windows AMD64
GOOS=windows GOARCH=amd64 go build -o server.exe ./cmd/server

# macOS ARM64
GOOS=darwin GOARCH=arm64 go build -o server ./cmd/client
```

---

### 📱 Running the Client

```bash
./client -config config.yaml
```

On successful connection, the client displays:
```
✅ Session established!
🌐 SOCKS5 Proxy: 127.0.0.1:1080
  ↑ 125.3 KB/s  ↓ 1.2 MB/s  │  Traffic: 45.6 MB  │  Connected: 00:15:32  │  Active: 3
```

---

### 📲 Official Application (Beta)

For convenient use, ready-to-use GUI applications are available:

**[Mimic App](https://github.com/Locon213/Mimic-App)** — official client application (in beta)

Available platforms:
- 🐧 **Linux** 
- 🍎 **macOS** 
- 🪟 **Windows** 
- 🤖 **Android** 

The application includes:
- ✅ Graphical user interface
- ✅ Configuration import via link (`mimic://...`)
- ✅ Real-time traffic statistics

---

### 🔌 Using as a Go SDK

You can seamlessly embed the Mimic Protocol Client into your own Go application (e.g., GUI or mobile wrapper). See the **[SDK Documentation](docs/sdk.md)**.

## 🔐 Security
- **Transport:** MTP (custom protocol over UDP) with ChaCha20-Poly1305 encryption
- **Polymorphism:** Every packet is unique — DPI cannot create a signature
- **Key Exchange:** UUID-based authorization
- **Anonymity:** Server does not store logs, no registration required

## 📄 License
This project is distributed under the GPLv3 License. See [LICENSE](LICENSE) for details.

Copyright (c) 2025-present Locon213 & Contributors.