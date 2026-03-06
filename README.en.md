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

## 🏗️ Repository Structure

The project is organized as a monorepo:

```
Mimic-Protocol/
├── cmd/                # Executables
│   ├── client/         # CLI Client (Windows/Linux/macOS)
│   └── server/         # Server implementation
├── pkg/                # Public libraries
│   ├── protocol/       # Protocol Core (VLESS-like + Mimicry)
│   └── presets/        # Behavior presets logic
├── internal/           # Internal components
└── docs/               # Documentation
```

## 🔧 How It Works

### Basic Principle
1. The user defines a list of "allowed" domains (e.g., `vk.com`, `rutube.ru`).
2. Mimic establishes a connection to the server, simulating the TLS handshake of the chosen site.
3. During operation, the client switches traffic patterns (packet size, timings) matching that service.
4. Every 30-600 seconds (configurable), the "mask" changes to the next domain in the list.

### Configuration
Example `config.yaml` for client:

```yaml
server: "your-mimic-server.com:443"
uuid: "your-uuid-here"
domains:
  - vk.com          # Preset "social"
  - rutube.ru       # Preset "video"
  - telegram.org    # Preset "messenger"

settings:
  switch_time: 60-300     # Change profile every 1-5 minutes
  randomize: true         # Randomize domain switch order
```

## 🚀 Development Plan

### Stage 1: Core & CLI (Current)
- [ ] Protocol core implementation in Go 1.25+
- [ ] Basic presets (social, video, messenger)
- [ ] CLI client with config support

### Stage 2: Infrastructure
- [ ] Full-featured server with user management
- [ ] Windows GUI client
- [ ] Optimization for VLESS/Reality

### Stage 3: Ecosystem
- [ ] Public server network

## 📦 Installation & Usage

> ⚠️ The project is under active development. API may change.

### Build from source

```bash
# Clone
git clone https://github.com/Locon213/Mimic-Protocol.git
cd Mimic-Protocol

# Build client
go build -o mimic-client ./cmd/client

# Run
./mimic-client -config config.yaml
```

## 🔐 Security
- **Encryption:** Full AES-128-GCM encryption (inside TLS 1.3 Application Data records)
- **Key Exchange:** UUID-based authorization
- **Anonymity:** Server does not store activity logs, clients do not require registration (keys only).

## 📄 License
This project is distributed under the GPLv3 License. See [LICENSE](LICENSE) for details.

Copyright (c) 2025-present Locon213 & Contributors.