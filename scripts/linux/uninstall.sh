#!/bin/bash
# Mimic Protocol Server Uninstaller

set -e

echo "============================================="
echo "   Mimic Protocol Server Uninstaller"
echo "============================================="

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root"
  exit 1
fi

# Stop and disable service
echo "=> Stopping and disabling service..."
systemctl stop mimic-server 2>/dev/null || true
systemctl disable mimic-server 2>/dev/null || true
echo "   ✓ Service stopped"

# Remove systemd service
echo "=> Removing systemd service..."
rm -f /etc/systemd/system/mimic-server.service
systemctl daemon-reload
echo "   ✓ Service removed"

# Remove binaries
echo "=> Removing binaries..."
rm -f /usr/local/bin/mimic-server
rm -f /usr/local/bin/mimic
echo "   ✓ Binaries removed"

# Ask about config removal
echo ""
read -p "Do you want to remove configuration files? (y/N): " -n 1 -r
echo ""
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "=> Removing configuration..."
    rm -rf /etc/mimic
    echo "   ✓ Configuration removed"
else
    echo "   ⚠️  Configuration preserved at /etc/mimic/"
fi

# Ask about sysctl optimizations removal
echo ""
read -p "Do you want to remove system optimizations (BBR, buffers)? (y/N): " -n 1 -r
echo ""
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "=> Removing system optimizations..."
    rm -f /etc/sysctl.d/99-mimic-optimizations.conf
    rm -f /etc/security/limits.d/99-mimic.conf
    sysctl -p 2>/dev/null || true
    echo "   ✓ Optimizations removed"
else
    echo "   ⚠️  Optimizations preserved (recommended for server performance)"
fi

echo ""
echo "============================================="
echo " Uninstallation Complete!"
echo "============================================="
echo ""
echo " Mimic Protocol Server has been removed."
echo ""
echo " To reinstall with new version:"
echo "   sudo bash scripts/linux/install.sh"
echo ""
echo "============================================="
