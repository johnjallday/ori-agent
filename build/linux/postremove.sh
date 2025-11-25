#!/bin/sh
set -e

# Reload systemd daemon
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi

# Note: We don't remove the ori-agent user or data directories
# Users may want to keep their data for reinstallation
# To completely remove:
#   sudo userdel ori-agent
#   sudo groupdel ori-agent
#   sudo rm -rf /var/lib/ori-agent
#   sudo rm -rf /var/log/ori-agent
#   sudo rm -rf /etc/ori-agent

echo ""
echo "Ori Agent has been removed."
echo ""
echo "User data and configuration preserved in:"
echo "  /var/lib/ori-agent (data)"
echo "  /etc/ori-agent (configuration)"
echo "  /var/log/ori-agent (logs)"
echo ""
echo "To completely remove all data, run:"
echo "  sudo rm -rf /var/lib/ori-agent /etc/ori-agent /var/log/ori-agent"
echo "  sudo userdel ori-agent"
echo "  sudo groupdel ori-agent"
echo ""

exit 0
