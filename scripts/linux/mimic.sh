#!/bin/bash
# Mimic Protocol Server Management Script

CONFIG_FILE="/etc/mimic/server.yaml"
SERVICE_NAME="mimic-server.service"

show_help() {
    echo "Usage: mimic <command>"
    echo "Commands:"
    echo "  start-server    - Starts the Mimic server service"
    echo "  stop-server     - Stops the Mimic server service"
    echo "  restart-server  - Restarts the Mimic server service"
    echo "  reload-server   - Reloads Systemd daemon and restarts server"
    echo "  status-server   - Prints the current status of the server"
    echo "  generate-uuid   - Generates a new MTP compatible UUID"
    echo "  generate-link   - Generates a mimic:// connection URI for clients"
    echo "  config_path     - Prints the location of the configuration file"
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
    config_path)
        echo "Server Configuration file path: $CONFIG_FILE"
        ;;
    *)
        show_help
        ;;
esac
