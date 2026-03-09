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

cat <<EOF > /etc/mimic/server.yaml
port: 443
uuid: "$UUID"
domains_file: "/etc/mimic/domains.txt"
max_clients: 100
rate_limit: 0
transport: "mtp"
EOF

# Default whitelist
cat <<EOF > /etc/mimic/domains.txt
vk.com
rutube.ru
yandex.ru
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

echo "============================================="
echo " Installation Complete!"
echo " Server configuration: /etc/mimic/server.yaml"
echo " Service status: systemctl status mimic-server"
echo " "
echo " You can use the new CLI tool to manage the server:"
echo "   mimic status-server"
echo "   mimic reload-server"
echo "   mimic stop-server"
echo "   mimic config_path"
echo "============================================="
