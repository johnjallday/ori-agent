# Ori Agent - Linux Installation Guide

This guide covers installing Ori Agent on Linux using `.deb` (Debian/Ubuntu) or `.rpm` (Red Hat/Fedora) packages.

## Quick Installation

### Debian/Ubuntu (.deb)

```bash
# Download the package (replace with actual version)
wget https://github.com/johnjallday/ori-agent/releases/download/v0.0.12/ori-agent_0.0.12_amd64.deb

# Install
sudo dpkg -i ori-agent_0.0.12_amd64.deb

# Fix dependencies if needed
sudo apt-get install -f
```

### Red Hat/Fedora/CentOS (.rpm)

```bash
# Download the package
wget https://github.com/johnjallday/ori-agent/releases/download/v0.0.12/ori-agent-0.0.12-1.x86_64.rpm

# Install
sudo rpm -ivh ori-agent-0.0.12-1.x86_64.rpm

# Or use dnf/yum
sudo dnf install ./ori-agent-0.0.12-1.x86_64.rpm
```

### ARM64 Systems

For ARM64-based systems (like Raspberry Pi, AWS Graviton):

```bash
# Debian/Ubuntu ARM64
wget https://github.com/johnjallday/ori-agent/releases/download/v0.0.12/ori-agent_0.0.12_arm64.deb
sudo dpkg -i ori-agent_0.0.12_arm64.deb

# Red Hat/Fedora ARM64
wget https://github.com/johnjallday/ori-agent/releases/download/v0.0.12/ori-agent-0.0.12-1.aarch64.rpm
sudo dnf install ./ori-agent-0.0.12-1.aarch64.rpm
```

## What Gets Installed

### Binary

- `/usr/bin/ori-agent` - Main executable

### Systemd Service

- `/lib/systemd/system/ori-agent.service` - Service definition

### Configuration

- `/etc/ori-agent/` - Configuration directory
- `/etc/ori-agent/environment` - Environment variables (API keys)

### Data Directories

- `/var/lib/ori-agent/` - Application data
- `/var/log/ori-agent/` - Log files

### Desktop Integration

- `/usr/share/applications/ori-agent.desktop` - Application launcher

### Documentation

- `/usr/share/doc/ori-agent/README.md`
- `/usr/share/doc/ori-agent/LICENSE`

### System User

- User: `ori-agent` (system user, no login)
- Group: `ori-agent`

## Configuration

### Setting API Keys

Edit the environment file:

```bash
sudo nano /etc/ori-agent/environment
```

Add your API keys:

```bash
# OpenAI API Key
OPENAI_API_KEY=your-openai-api-key-here

# Anthropic API Key (optional)
ANTHROPIC_API_KEY=your-anthropic-api-key-here

# Server Port (optional, default: 8765)
PORT=8765
```

Save and exit (Ctrl+X, then Y, then Enter).

### File Permissions

The environment file is protected:
- Owner: `root:ori-agent`
- Permissions: `640` (root can write, ori-agent group can read)

## Running as a Service

### Start the Service

```bash
sudo systemctl start ori-agent
```

### Check Status

```bash
sudo systemctl status ori-agent
```

You should see:
```
● ori-agent.service - Ori Agent - Modular AI Agent Framework
     Loaded: loaded (/lib/systemd/system/ori-agent.service; enabled; vendor preset: enabled)
     Active: active (running) since...
```

### Enable Auto-Start on Boot

```bash
sudo systemctl enable ori-agent
```

### Stop the Service

```bash
sudo systemctl stop ori-agent
```

### Restart the Service

```bash
sudo systemctl restart ori-agent
```

## Viewing Logs

### Real-time Logs

```bash
sudo journalctl -u ori-agent -f
```

### Recent Logs

```bash
sudo journalctl -u ori-agent -n 100
```

### Logs from Today

```bash
sudo journalctl -u ori-agent --since today
```

### Logs from Specific Time

```bash
sudo journalctl -u ori-agent --since "2025-01-01 00:00:00"
```

## Accessing the Web UI

Once the service is running, access the web interface:

```bash
# From the same machine
http://localhost:8765

# From another machine (replace with server IP)
http://192.168.1.100:8765
```

### Firewall Configuration

If you can't access from another machine, open the firewall:

**UFW (Ubuntu):**
```bash
sudo ufw allow 8765/tcp
```

**firewalld (Fedora/CentOS):**
```bash
sudo firewall-cmd --permanent --add-port=8765/tcp
sudo firewall-cmd --reload
```

## Running Manually

If you prefer not to use the service:

```bash
# Run as the ori-agent user
sudo -u ori-agent /usr/bin/ori-agent

# Or run as your own user
/usr/bin/ori-agent
```

## Uninstalling

### Debian/Ubuntu

```bash
# Remove package but keep configuration
sudo apt-get remove ori-agent

# Remove package and configuration
sudo apt-get purge ori-agent

# Remove package, configuration, and data
sudo apt-get purge ori-agent
sudo rm -rf /var/lib/ori-agent /var/log/ori-agent /etc/ori-agent
sudo userdel ori-agent
sudo groupdel ori-agent
```

### Red Hat/Fedora

```bash
# Remove package
sudo rpm -e ori-agent
# Or
sudo dnf remove ori-agent

# Remove data (after uninstall)
sudo rm -rf /var/lib/ori-agent /var/log/ori-agent /etc/ori-agent
sudo userdel ori-agent
sudo groupdel ori-agent
```

## Upgrading

### Debian/Ubuntu

```bash
# Download new version
wget https://github.com/johnjallday/ori-agent/releases/download/v0.0.13/ori-agent_0.0.13_amd64.deb

# Upgrade
sudo dpkg -i ori-agent_0.0.13_amd64.deb

# Restart service
sudo systemctl restart ori-agent
```

### Red Hat/Fedora

```bash
# Download new version
wget https://github.com/johnjallday/ori-agent/releases/download/v0.0.13/ori-agent-0.0.13-1.x86_64.rpm

# Upgrade
sudo rpm -Uvh ori-agent-0.0.13-1.x86_64.rpm

# Restart service
sudo systemctl restart ori-agent
```

**Note**: Your configuration and data are preserved during upgrades.

## Troubleshooting

### Service Won't Start

**Check logs:**
```bash
sudo journalctl -u ori-agent -n 50
```

**Common issues:**
1. **Missing API key**: Edit `/etc/ori-agent/environment`
2. **Port in use**: Change `PORT` in `/etc/ori-agent/environment`
3. **Permissions**: Check `/var/lib/ori-agent` ownership

**Fix permissions:**
```bash
sudo chown -R ori-agent:ori-agent /var/lib/ori-agent
sudo chown -R ori-agent:ori-agent /var/log/ori-agent
```

### Can't Access Web UI

**1. Check if service is running:**
```bash
sudo systemctl status ori-agent
```

**2. Check which port it's using:**
```bash
sudo netstat -tlnp | grep ori-agent
```

**3. Check firewall:**
```bash
# UFW
sudo ufw status

# firewalld
sudo firewall-cmd --list-ports
```

### Package Installation Fails

**Debian/Ubuntu:**
```bash
# Check for dependency issues
sudo apt-get install -f

# View detailed error
sudo dpkg -i ori-agent_0.0.12_amd64.deb
```

**Red Hat/Fedora:**
```bash
# Check dependencies
sudo dnf check

# Force installation (not recommended)
sudo rpm -ivh --nodeps ori-agent-0.0.12-1.x86_64.rpm
```

### systemd Service Issues

**Reload systemd after manual changes:**
```bash
sudo systemctl daemon-reload
sudo systemctl restart ori-agent
```

**Check service file syntax:**
```bash
systemd-analyze verify /lib/systemd/system/ori-agent.service
```

### Permission Denied Errors

**If ori-agent can't write to its directories:**
```bash
sudo chmod 755 /var/lib/ori-agent
sudo chmod 755 /var/log/ori-agent
sudo chown ori-agent:ori-agent /var/lib/ori-agent
sudo chown ori-agent:ori-agent /var/log/ori-agent
```

## Advanced Configuration

### Custom Port

Edit `/etc/ori-agent/environment`:
```bash
PORT=9000
```

Restart:
```bash
sudo systemctl restart ori-agent
```

### Multiple Instances

To run multiple instances (not recommended):

1. Copy service file:
   ```bash
   sudo cp /lib/systemd/system/ori-agent.service /lib/systemd/system/ori-agent-2.service
   ```

2. Edit new service file:
   ```bash
   sudo nano /lib/systemd/system/ori-agent-2.service
   ```

3. Change:
   - `WorkingDirectory=/var/lib/ori-agent-2`
   - Add `Environment="PORT=8766"`

4. Create directory:
   ```bash
   sudo mkdir /var/lib/ori-agent-2
   sudo chown ori-agent:ori-agent /var/lib/ori-agent-2
   ```

5. Start:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl start ori-agent-2
   ```

### Running as Different User

**Not recommended**, but possible:

Edit `/lib/systemd/system/ori-agent.service`:
```ini
[Service]
User=youruser
Group=yourgroup
```

Then:
```bash
sudo systemctl daemon-reload
sudo systemctl restart ori-agent
```

### Reverse Proxy (Nginx)

To run behind Nginx:

```nginx
server {
    listen 80;
    server_name ori-agent.example.com;

    location / {
        proxy_pass http://localhost:8765;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
    }
}
```

Reload Nginx:
```bash
sudo nginx -t
sudo systemctl reload nginx
```

## Security Considerations

### Running as Non-Root

The service runs as the `ori-agent` system user (not root) for security.

### File Permissions

- Config: `640` (root can write, ori-agent can read)
- Data: `755` (ori-agent owns, others can read)
- Logs: `755` (ori-agent owns, others can read)

### Firewall

Only open port 8765 if you need external access:

```bash
# Local access only (default - no firewall rule needed)
# External access
sudo ufw allow 8765/tcp
```

### API Keys

Keep your API keys secure:
- Stored in `/etc/ori-agent/environment`
- Only readable by `root` and `ori-agent` group
- Never commit to version control
- Rotate regularly

## System Requirements

### Minimum

- **OS**: Debian 10+, Ubuntu 20.04+, Fedora 34+, CentOS 8+
- **CPU**: 1 core (x86_64 or ARM64)
- **RAM**: 512 MB
- **Disk**: 100 MB for application, additional for data

### Recommended

- **OS**: Debian 11+, Ubuntu 22.04+, Fedora 38+
- **CPU**: 2+ cores
- **RAM**: 2 GB
- **Disk**: 1 GB+ for application and data
- **systemd**: Required for service management

## Package Contents

### .deb Package

```
/usr/bin/ori-agent                                    # Binary
/lib/systemd/system/ori-agent.service                 # Service
/usr/share/applications/ori-agent.desktop             # Launcher
/usr/share/doc/ori-agent/README.md                    # Docs
/usr/share/doc/ori-agent/LICENSE                      # License
/var/lib/ori-agent/                                   # Data dir
/var/log/ori-agent/                                   # Logs
/etc/ori-agent/                                       # Config
```

### .rpm Package

Same structure as `.deb`, RPM format for Red Hat-based distributions.

## Getting Help

- **Documentation**: https://github.com/johnjallday/ori-agent
- **Issues**: https://github.com/johnjallday/ori-agent/issues
- **Discussions**: https://github.com/johnjallday/ori-agent/discussions

## Testing Your Installation

```bash
# 1. Check binary version
/usr/bin/ori-agent --version

# 2. Check service status
sudo systemctl status ori-agent

# 3. Check if port is open
sudo netstat -tlnp | grep 8765

# 4. Test web UI
curl http://localhost:8765

# 5. Check logs
sudo journalctl -u ori-agent -n 10
```

---

**Last Updated**: November 18, 2025
**Version**: Covers Ori Agent 0.0.11+
**Supported**: Debian, Ubuntu, Fedora, CentOS, Red Hat Enterprise Linux
