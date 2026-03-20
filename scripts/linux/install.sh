#!/bin/bash
set -e

echo "============================================="
echo "   Mimic Protocol Server Installer"
echo "============================================="

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root"
  exit 1
fi

# ============================================
# Detect Linux Distribution
# ============================================
detect_distro() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        DISTRO_ID="${ID}"
        DISTRO_NAME="${NAME}"
        DISTRO_VERSION="${VERSION_ID}"
    elif [ -f /etc/redhat-release ]; then
        DISTRO_ID="centos"
        DISTRO_NAME=$(cat /etc/redhat-release)
        DISTRO_VERSION=$(rpm -q --queryformat '%{VERSION}' centos-release 2>/dev/null || echo "7")
    else
        echo "Error: Cannot detect Linux distribution"
        exit 1
    fi
    
    echo "=> Detected: $DISTRO_NAME ($DISTRO_ID $DISTRO_VERSION)"
}

# ============================================
# Install Dependencies based on Distribution
# ============================================
install_dependencies() {
    echo "=> Installing dependencies..."
    
    case "$DISTRO_ID" in
        ubuntu|debian|linuxmint|pop|elementary|zorin)
            echo "   Using apt package manager..."
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -qq
            apt-get install -y -qq wget curl systemd jq
            ;;
        
        centos|rhel|rocky|almalinux|ol)
            echo "   Using yum package manager..."
            if command -v dnf &> /dev/null; then
                dnf install -y wget curl systemd jq
            else
                yum install -y wget curl systemd jq
            fi
            ;;
        
        fedora)
            echo "   Using dnf package manager..."
            dnf install -y wget curl systemd jq
            ;;
        
        arch|manjaro|endeavouros|garuda)
            echo "   Using pacman package manager..."
            pacman -Sy --noconfirm wget curl systemd jq
            ;;
        
        opensuse*|sles)
            echo "   Using zypper package manager..."
            zypper install -y wget curl systemd jq
            ;;
        
        alpine)
            echo "   Using apk package manager..."
            apk add --no-cache wget curl jq
            ;;
        
        *)
            echo "Warning: Unsupported distribution '$DISTRO_ID'"
            echo "Attempting to install dependencies anyway..."
            if command -v apt-get &> /dev/null; then
                apt-get update -qq && apt-get install -y wget curl systemd jq
            elif command -v dnf &> /dev/null; then
                dnf install -y wget curl systemd jq
            elif command -v yum &> /dev/null; then
                yum install -y wget curl systemd jq
            elif command -v pacman &> /dev/null; then
                pacman -Sy --noconfirm wget curl systemd jq
            elif command -v zypper &> /dev/null; then
                zypper install -y wget curl systemd jq
            else
                echo "Error: Cannot determine package manager"
                exit 1
            fi
            ;;
    esac
    
    echo "   ✓ Dependencies installed"
}

# ============================================
# Server Performance Optimizations
# ============================================
optimize_server() {
    echo "=> Applying server performance optimizations..."
    
    # Create sysctl configuration for Mimic Protocol
    cat <<'EOF' > /etc/sysctl.d/99-mimic-optimizations.conf
# ============================================
# Mimic Protocol Server Optimizations
# ============================================

# Enable BBR congestion control (better throughput and latency)
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr

# Increase network buffer sizes
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.ipv4.tcp_rmem=4096 87380 16777216
net.ipv4.tcp_wmem=4096 65536 16777216
net.core.rmem_default=1048576
net.core.wmem_default=1048576

# Increase max connections and backlog
net.core.somaxconn=65535
net.core.netdev_max_backlog=65535
net.ipv4.tcp_max_syn_backlog=65535

# Enable TCP Fast Open
net.ipv4.tcp_fastopen=3

# Optimize TCP keepalive
net.ipv4.tcp_keepalive_time=600
net.ipv4.tcp_keepalive_intvl=60
net.ipv4.tcp_keepalive_probes=5

# Increase max open files limit via sysctl
fs.file-max=2097152
fs.nr_open=2097152

# Optimize for high-throughput UDP
net.core.optmem_max=65536
net.ipv4.udp_rmem_min=8192
net.ipv4.udp_wmem_min=8192

# Disable SYN cookies (not needed with BBR)
net.ipv4.tcp_syncookies=0

# Enable TCP window scaling
net.ipv4.tcp_window_scaling=1

# Increase TCP max orphans
net.ipv4.tcp_max_orphans=262144

# Optimize TIME_WAIT
net.ipv4.tcp_tw_reuse=1
net.ipv4.tcp_fin_timeout=15

# Increase conntrack table size for many connections
net.netfilter.nf_conntrack_max=1048576
net.netfilter.nf_conntrack_tcp_timeout_established=86400
EOF

    # Apply sysctl settings
    if sysctl -p /etc/sysctl.d/99-mimic-optimizations.conf 2>/dev/null; then
        echo "   ✓ Kernel parameters optimized"
    else
        echo "   ⚠️  Some sysctl settings could not be applied (non-critical)"
    fi
    
    # Check if BBR is available
    if modprobe tcp_bbr 2>/dev/null; then
        echo "   ✓ BBR congestion control module loaded"
    fi
    
    # Verify BBR is active
    if sysctl net.ipv4.tcp_congestion_control 2>/dev/null | grep -q bbr; then
        echo "   ✓ BBR congestion control enabled"
    else
        echo "   ⚠️  BBR not available, using default congestion control"
    fi
    
    # Increase system limits
    cat <<'EOF' > /etc/security/limits.d/99-mimic.conf
# Mimic Protocol - Increase file descriptor limits
*    soft    nofile    1048576
*    hard    nofile    1048576
root soft    nofile    1048576
root hard    nofile    1048576
EOF
    
    echo "   ✓ System limits configured"
}

# ============================================
# Main Installation
# ============================================

# Detect distribution
detect_distro

# Determine architecture
ARCH=$(uname -m)
case $ARCH in
    x86_64|amd64) GOARCH="amd64" ;;
    aarch64|arm64) GOARCH="arm64" ;;
    armv7l|armhf) GOARCH="arm" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "=> Architecture: $ARCH ($GOARCH)"

# Install dependencies
install_dependencies

# Apply server optimizations
optimize_server

# ============================================
# Download and Install Mimic Server
# ============================================
VERSION=${1:-latest}
if [ "$VERSION" = "latest" ]; then
    BINARY_URL="https://github.com/Locon213/Mimic-Protocol/releases/latest/download/mimic-server-linux-$GOARCH"
else
    BINARY_URL="https://github.com/Locon213/Mimic-Protocol/releases/download/${VERSION}/mimic-server-linux-$GOARCH"
fi

if [ -f "./server" ]; then
    echo "=> Found local 'server' binary, using it..."
    cp ./server /usr/local/bin/mimic-server
else
    echo "=> Downloading Mimic Protocol ($VERSION) for $GOARCH..."
    if ! wget -q -O /usr/local/bin/mimic-server "$BINARY_URL"; then
        echo "Error: Failed to download binary from $BINARY_URL."
        echo "Make sure the version is correct or a release exists."
        exit 1
    fi
fi
chmod +x /usr/local/bin/mimic-server
echo "   ✓ Binary installed to /usr/local/bin/mimic-server"

# Install mimic CLI wrapper
if [ -f "./scripts/linux/mimic.sh" ]; then
    cp ./scripts/linux/mimic.sh /usr/local/bin/mimic
    chmod +x /usr/local/bin/mimic
    echo "=> Installed 'mimic' CLI."
fi

# ============================================
# Generate Configuration
# ============================================
echo "=> Generating Configuration..."
mkdir -p /etc/mimic/
UUID=$(/usr/local/bin/mimic-server generate-uuid)
echo "   Generated UUID: $UUID"

# Try to detect public IP
echo "=> Detecting public IP..."
PUBLIC_IP=$(curl -s --max-time 5 https://api.ipify.org || curl -s --max-time 5 https://ifconfig.me || echo "")
if [ -z "$PUBLIC_IP" ]; then
    PUBLIC_IP="YOUR_SERVER_IP"
    echo "   ⚠️  Could not auto-detect public IP. Please update server.yaml manually."
else
    echo "   ✓ Detected public IP: $PUBLIC_IP"
fi

cat <<EOF > /etc/mimic/server.yaml
# Mimic Protocol Server Configuration
# Documentation: https://github.com/Locon213/Mimic-Protocol

port: 443
uuid: "$UUID"
name: "Mimic-Server"
transport: "mtp"

# Domains for traffic mimicry
domain_list:
  - vk.com
  - rutube.ru
  - telegram.org
  - wikipedia.org

# Max clients (0 = unlimited)
max_clients: 100

# DNS server (optional)
dns: "1.1.1.1:53"

# Data compression (optional, disabled by default for performance)
# compression:
#   enable: false
#   level: 2
#   min_size: 64
EOF

echo "   ✓ Configuration saved to /etc/mimic/server.yaml"

# ============================================
# Create Systemd Service
# ============================================
echo "=> Creating Systemd Service..."
cat <<EOF > /etc/systemd/system/mimic-server.service
[Unit]
Description=Mimic Protocol Server
After=network.target
Documentation=https://github.com/Locon213/Mimic-Protocol

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/mimic-server -config /etc/mimic/server.yaml
Restart=on-failure
RestartSec=3

# Performance optimizations
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/etc/mimic
PrivateTmp=true

# OOM adjustment
OOMScoreAdjust=-500

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable mimic-server
systemctl start mimic-server

echo "   ✓ Service created and started"

# ============================================
# Generate Connection Link
# ============================================
echo ""
echo "=> Generating connection link..."
LINK=$(/usr/local/bin/mimic-server generate-link /etc/mimic/server.yaml --host "$PUBLIC_IP" 2>/dev/null | grep "mimic://" || echo "")

echo "============================================="
echo " Installation Complete!"
echo "============================================="
echo ""
echo " Distribution: $DISTRO_NAME"
echo " Server configuration: /etc/mimic/server.yaml"
echo " Service status: systemctl status mimic-server"
echo ""
echo " Applied optimizations:"
echo "   ✓ BBR congestion control enabled"
echo "   ✓ Network buffers optimized"
echo "   ✓ File descriptor limits increased"
echo "   ✓ TCP Fast Open enabled"
echo ""
if [ -n "$LINK" ] && [ "$PUBLIC_IP" != "YOUR_SERVER_IP" ]; then
    echo "🚀 Client connection link:"
    echo "$LINK"
else
    echo "⚠️  To generate client link:"
    echo "   mimic generate-link"
    echo "   # or specify IP manually:"
    echo "   mimic-server generate-link /etc/mimic/server.yaml --host YOUR_IP"
fi
echo ""
echo " CLI commands:"
echo "   mimic status-server"
echo "   mimic restart-server"
echo "   mimic stop-server"
echo "   mimic generate-uuid"
echo "   mimic generate-link"
echo "   mimic optimize-status"
echo "============================================="
