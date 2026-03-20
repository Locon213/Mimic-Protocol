#!/bin/bash
# Mimic Protocol Server Management Script

CONFIG_FILE="/etc/mimic/server.yaml"
SERVICE_NAME="mimic-server.service"

show_help() {
    echo "Mimic Protocol Server Management CLI"
    echo ""
    echo "Usage: mimic <command>"
    echo ""
    echo "Server Management:"
    echo "  start-server      - Starts the Mimic server service"
    echo "  stop-server       - Stops the Mimic server service"
    echo "  restart-server    - Restarts the Mimic server service"
    echo "  reload-server     - Reloads Systemd daemon and restarts server"
    echo "  status-server     - Prints the current status of the server"
    echo ""
    echo "Configuration:"
    echo "  generate-uuid     - Generates a new MTP compatible UUID"
    echo "  generate-link     - Generates a mimic:// connection URI for clients"
    echo "  config-path       - Prints the location of the configuration file"
    echo "  edit-config       - Opens configuration file in default editor"
    echo ""
    echo "Diagnostics:"
    echo "  logs              - Shows server logs (last 50 lines)"
    echo "  logs-follow       - Follows server logs in real-time"
    echo "  optimize-status   - Shows current system optimization status"
    echo "  check-bbr         - Checks if BBR congestion control is enabled"
    echo "  version           - Shows installed Mimic server version"
    echo ""
    echo "Examples:"
    echo "  mimic status-server"
    echo "  mimic generate-link"
    echo "  mimic logs-follow"
}

case "$1" in
    start-server)
        echo "Starting $SERVICE_NAME..."
        sudo systemctl start $SERVICE_NAME
        echo "Done."
        ;;
    stop-server)
        echo "Stopping $SERVICE_NAME..."
        sudo systemctl stop $SERVICE_NAME
        echo "Done."
        ;;
    restart-server)
        echo "Restarting $SERVICE_NAME..."
        sudo systemctl restart $SERVICE_NAME
        echo "Done."
        ;;
    reload-server)
        echo "Reloading daemon and restarting $SERVICE_NAME..."
        sudo systemctl daemon-reload
        sudo systemctl restart $SERVICE_NAME
        echo "Done."
        ;;
    status-server)
        sudo systemctl status $SERVICE_NAME
        ;;
    generate-uuid)
        if command -v mimic-server >/dev/null 2>&1; then
            NEW_UUID=$(mimic-server generate-uuid)
            echo "New UUID generated: $NEW_UUID"
            echo "Remember to update your config in: $CONFIG_FILE"
        else
            echo "Error: mimic-server binary not found in PATH."
        fi
        ;;
    generate-link)
        if command -v mimic-server >/dev/null 2>&1; then
            mimic-server generate-link $CONFIG_FILE
        else
            echo "Error: mimic-server binary not found in PATH."
        fi
        ;;
    config-path)
        echo "Server Configuration file path: $CONFIG_FILE"
        ;;
    edit-config)
        if [ -f "$CONFIG_FILE" ]; then
            ${EDITOR:-nano} "$CONFIG_FILE"
        else
            echo "Error: Configuration file not found at $CONFIG_FILE"
        fi
        ;;
    logs)
        echo "=== Mimic Server Logs (last 50 lines) ==="
        sudo journalctl -u $SERVICE_NAME -n 50 --no-pager
        ;;
    logs-follow)
        echo "=== Following Mimic Server Logs (Ctrl+C to stop) ==="
        sudo journalctl -u $SERVICE_NAME -f
        ;;
    optimize-status)
        echo "============================================="
        echo " System Optimization Status"
        echo "============================================="
        echo ""
        
        # Check BBR
        echo "BBR Congestion Control:"
        if sysctl net.ipv4.tcp_congestion_control 2>/dev/null | grep -q bbr; then
            echo "  ✓ Enabled"
        else
            CURRENT_CC=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo "unknown")
            echo "  ✗ Not enabled (using: $CURRENT_CC)"
        fi
        echo ""
        
        # Check network buffers
        echo "Network Buffers:"
        RMEM_MAX=$(sysctl -n net.core.rmem_max 2>/dev/null || echo "0")
        WMEM_MAX=$(sysctl -n net.core.wmem_max 2>/dev/null || echo "0")
        echo "  rmem_max: $RMEM_MAX bytes"
        echo "  wmem_max: $WMEM_MAX bytes"
        if [ "$RMEM_MAX" -ge 16777216 ] 2>/dev/null; then
            echo "  ✓ Optimized"
        else
            echo "  ⚠ Default (consider running installer for optimizations)"
        fi
        echo ""
        
        # Check file limits
        echo "File Descriptor Limits:"
        FILE_MAX=$(sysctl -n fs.file-max 2>/dev/null || echo "0")
        echo "  fs.file-max: $FILE_MAX"
        if [ "$FILE_MAX" -ge 2097152 ] 2>/dev/null; then
            echo "  ✓ Optimized"
        else
            echo "  ⚠ Default"
        fi
        echo ""
        
        # Check TCP Fast Open
        echo "TCP Fast Open:"
        TFO=$(sysctl -n net.ipv4.tcp_fastopen 2>/dev/null || echo "0")
        echo "  Value: $TFO"
        if [ "$TFO" -ge 2 ] 2>/dev/null; then
            echo "  ✓ Enabled"
        else
            echo "  ⚠ Not fully enabled"
        fi
        echo ""
        
        # Check somaxconn
        echo "Max Connections:"
        SOMAXCONN=$(sysctl -n net.core.somaxconn 2>/dev/null || echo "0")
        echo "  somaxconn: $SOMAXCONN"
        if [ "$SOMAXCONN" -ge 65535 ] 2>/dev/null; then
            echo "  ✓ Optimized"
        else
            echo "  ⚠ Default"
        fi
        echo ""
        
        echo "============================================="
        ;;
    check-bbr)
        echo "Checking BBR congestion control..."
        if sysctl net.ipv4.tcp_congestion_control 2>/dev/null | grep -q bbr; then
            echo "✓ BBR is enabled"
            sysctl net.ipv4.tcp_congestion_control
        else
            CURRENT_CC=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo "unknown")
            echo "✗ BBR is NOT enabled (using: $CURRENT_CC)"
            echo ""
            echo "To enable BBR, run the installer or manually:"
            echo "  echo 'net.core.default_qdisc=fq' | sudo tee -a /etc/sysctl.d/99-mimic-optimizations.conf"
            echo "  echo 'net.ipv4.tcp_congestion_control=bbr' | sudo tee -a /etc/sysctl.d/99-mimic-optimizations.conf"
            echo "  sudo sysctl -p /etc/sysctl.d/99-mimic-optimizations.conf"
        fi
        ;;
    version)
        if command -v mimic-server >/dev/null 2>&1; then
            mimic-server --version 2>/dev/null || mimic-server version 2>/dev/null || echo "mimic-server installed at: $(which mimic-server)"
        else
            echo "Error: mimic-server binary not found in PATH."
        fi
        ;;
    *)
        show_help
        ;;
esac
