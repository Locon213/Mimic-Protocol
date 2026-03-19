#!/bin/bash
set -e

echo "============================================="
echo "   Mimic Protocol Server Installer"
echo "============================================="

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root"
  exit
fi

# Determine architecture
ARCH=$(uname -m)
case $ARCH in
    x86_64) GOARCH="amd64" ;;
    aarch64) GOARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "=> Installing dependencies..."
apt-get update -qq && apt-get install -y wget curl systemd jq

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

# Install mimic CLI wrapper
if [ -f "./scripts/linux/mimic.sh" ]; then
    cp ./scripts/linux/mimic.sh /usr/local/bin/mimic
    chmod +x /usr/local/bin/mimic
    echo "=> Installed 'mimic' CLI."
fi

echo "=> Generating Configuration..."
mkdir -p /etc/mimic/
UUID=$(/usr/local/bin/mimic-server generate-uuid)
echo "Generated UUID: $UUID"

# Try to detect public IP
echo "=> Detecting public IP..."
PUBLIC_IP=$(curl -s --max-time 5 https://api.ipify.org || echo "")
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

echo "=> Creating Systemd Service..."
cat <<EOF > /etc/systemd/system/mimic-server.service
[Unit]
Description=Mimic Protocol Server
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/mimic-server -config /etc/mimic/server.yaml
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable mimic-server
systemctl start mimic-server

# Generate connection link
echo ""
echo "=> Generating connection link..."
LINK=$(/usr/local/bin/mimic-server generate-link /etc/mimic/server.yaml --host "$PUBLIC_IP" 2>/dev/null | grep "mimic://" || echo "")

echo "============================================="
echo " Installation Complete!"
echo "============================================="
echo ""
echo " Server configuration: /etc/mimic/server.yaml"
echo " Service status: systemctl status mimic-server"
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
echo "============================================="
