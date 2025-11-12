package menubar

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	launchAgentLabel    = "com.ori.menubar"
	launchAgentFileName = "com.ori.menubar.plist"
)

// LaunchAgentManager manages the macOS LaunchAgent for auto-start on login
type LaunchAgentManager struct {
	plistPath      string
	executablePath string
}

// NewLaunchAgentManager creates a new LaunchAgent manager
func NewLaunchAgentManager() (*LaunchAgentManager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	// Get the path to the current executable
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks to get the real path
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve executable path: %w", err)
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", launchAgentFileName)

	return &LaunchAgentManager{
		plistPath:      plistPath,
		executablePath: execPath,
	}, nil
}

// IsInstalled checks if the LaunchAgent is currently installed
func (m *LaunchAgentManager) IsInstalled() bool {
	_, err := os.Stat(m.plistPath)
	return err == nil
}

// Install creates and loads the LaunchAgent plist file
func (m *LaunchAgentManager) Install() error {
	// Ensure LaunchAgents directory exists
	launchAgentsDir := filepath.Dir(m.plistPath)
	if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
		return fmt.Errorf("failed to create LaunchAgents directory: %w", err)
	}

	// Generate plist content
	plistContent := m.generatePlist()

	// Write plist file
	if err := os.WriteFile(m.plistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("failed to write plist file: %w", err)
	}

	// Load the LaunchAgent using launchctl
	if err := m.load(); err != nil {
		// Clean up the plist file if load fails
		os.Remove(m.plistPath)
		return fmt.Errorf("failed to load LaunchAgent: %w", err)
	}

	return nil
}

// Uninstall unloads and removes the LaunchAgent plist file
func (m *LaunchAgentManager) Uninstall() error {
	// First unload the LaunchAgent if it's loaded
	if m.IsInstalled() {
		if err := m.unload(); err != nil {
			// Continue even if unload fails (might not be loaded)
			fmt.Printf("Warning: failed to unload LaunchAgent: %v\n", err)
		}
	}

	// Remove the plist file
	if err := os.Remove(m.plistPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove plist file: %w", err)
		}
	}

	return nil
}

// load uses launchctl to load the LaunchAgent
func (m *LaunchAgentManager) load() error {
	cmd := exec.Command("launchctl", "load", m.plistPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl load failed: %w (output: %s)", err, string(output))
	}
	return nil
}

// unload uses launchctl to unload the LaunchAgent
func (m *LaunchAgentManager) unload() error {
	cmd := exec.Command("launchctl", "unload", m.plistPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if it's just because it's not loaded
		if strings.Contains(string(output), "Could not find specified service") {
			return nil // Not an error if it wasn't loaded
		}
		return fmt.Errorf("launchctl unload failed: %w (output: %s)", err, string(output))
	}
	return nil
}

// generatePlist creates the LaunchAgent plist XML content
func (m *LaunchAgentManager) generatePlist() string {
	homeDir, _ := os.UserHomeDir()
	logDir := filepath.Join(homeDir, "Library", "Logs")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>StandardOutPath</key>
    <string>%s/ori-menubar.log</string>
    <key>StandardErrorPath</key>
    <string>%s/ori-menubar.error.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    </dict>
</dict>
</plist>
`,
		launchAgentLabel,
		m.executablePath,
		logDir,
		logDir,
	)

	return plist
}

// GetPlistPath returns the path to the LaunchAgent plist file
func (m *LaunchAgentManager) GetPlistPath() string {
	return m.plistPath
}

// GetExecutablePath returns the path to the executable that will be launched
func (m *LaunchAgentManager) GetExecutablePath() string {
	return m.executablePath
}
