#!/bin/sh
set -e

# Create ori-agent user and group if they don't exist
if ! getent group ori-agent >/dev/null 2>&1; then
    groupadd --system ori-agent
fi

if ! getent passwd ori-agent >/dev/null 2>&1; then
    useradd --system --gid ori-agent --no-create-home \
        --home-dir /var/lib/ori-agent --shell /bin/false \
        --comment "Ori Agent Service User" ori-agent
fi

# Create necessary directories
mkdir -p /var/lib/ori-agent
mkdir -p /var/log/ori-agent
mkdir -p /etc/ori-agent

# Set permissions
chown ori-agent:ori-agent /var/lib/ori-agent
chown ori-agent:ori-agent /var/log/ori-agent
chmod 755 /var/lib/ori-agent
chmod 755 /var/log/ori-agent
chmod 755 /etc/ori-agent

# Create environment file template if it doesn't exist
if [ ! -f /etc/ori-agent/environment ]; then
    cat > /etc/ori-agent/environment <<EOF
# Ori Agent Environment Configuration
# Uncomment and set your API keys below

# OpenAI API Key
#OPENAI_API_KEY=your-openai-api-key-here

# Anthropic API Key
#ANTHROPIC_API_KEY=your-anthropic-api-key-here

# Server Port (default: 8765)
#PORT=8765
EOF
    chmod 640 /etc/ori-agent/environment
    chown root:ori-agent /etc/ori-agent/environment
fi

# Reload systemd daemon
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload

    # Enable but don't start the service automatically
    # Users can start it manually with: systemctl start ori-agent
    systemctl enable ori-agent.service || true

    echo ""
    echo "================================"
    echo "Ori Agent installed successfully!"
    echo "================================"
    echo ""
    echo "To configure API keys, edit:"
    echo "  /etc/ori-agent/environment"
    echo ""
    echo "To start the service:"
    echo "  sudo systemctl start ori-agent"
    echo ""
    echo "To check status:"
    echo "  sudo systemctl status ori-agent"
    echo ""
    echo "To view logs:"
    echo "  sudo journalctl -u ori-agent -f"
    echo ""
fi

exit 0
