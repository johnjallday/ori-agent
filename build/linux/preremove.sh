#!/bin/sh
set -e

# Stop and disable the service
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet ori-agent.service; then
        systemctl stop ori-agent.service || true
    fi
    systemctl disable ori-agent.service || true
fi

exit 0
