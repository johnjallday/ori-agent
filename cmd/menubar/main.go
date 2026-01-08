//go:build darwin
// +build darwin

package main

import (
	"context"
	"fmt"
	"log"

	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/getlantern/systray"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/menubar"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	portutil "github.com/johnjallday/ori-agent/internal/port"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Ori Agent Menu Bar App (systray)...")

	// Change to proper user data directory for macOS
	dataDir := os.Getenv("HOME") + "/Library/Application Support/OriAgent"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}
	if err := os.Chdir(dataDir); err != nil {
		log.Fatalf("Failed to change to data directory: %v", err)
	}
	logger.Debug("Working directory", logger.Fields{"dataDir": dataDir})

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Initialize settings managers first
	onboardingMgr := onboarding.NewManager("app_state.json")
	settingsMgr := menubar.NewSettingsManager(onboardingMgr)

	// Get port from settings (defaults to 8765)
	port := settingsMgr.GetPort()
	logger.Debug("Using port", logger.Fields{"port": port})

	controller := menubar.NewController(port)

	// Initialize LaunchAgent manager
	launchAgentMgr, err := menubar.NewLaunchAgentManager()
	if err != nil {
		logger.Error("Failed to create LaunchAgent manager", logger.Fields{"agent": err})
		log.Println("Auto-start feature will be disabled")
	}

	// Handle signals in a goroutine
	go func() {
		<-sigChan
		log.Println("Received shutdown signal...")

		// Stop server if running
		ctx := context.Background()
		if controller.GetStatus() == menubar.StatusRunning {
			log.Println("Stopping server...")
			if err := controller.StopServer(ctx); err != nil {
				logger.Error("Error stopping server", logger.Fields{"error": err})
			}
		}

		log.Println("Quitting systray...")
		systray.Quit()
	}()

	// Run systray
	onReady := func() {
		log.Println("Systray ready, setting up menu...")
		setupMenuSystray(controller, settingsMgr, launchAgentMgr)
	}

	onExit := func() {
		log.Println("Systray exiting...")
		// Ensure server is stopped
		ctx := context.Background()
		if controller.GetStatus() == menubar.StatusRunning {
			log.Println("Stopping server before exit...")
			_ = controller.StopServer(ctx) // Best effort stop on exit
		}
	}

	systray.Run(onReady, onExit)
	log.Println("Systray app exited")
}

func setupMenuSystray(controller *menubar.Controller, settingsMgr *menubar.SettingsManager, launchAgentMgr *menubar.LaunchAgentManager) {
	// Set initial icon and tooltip
	systray.SetIcon(menubar.GetStoppedIcon())
	systray.SetTooltip("Ori Agent - Server Stopped")

	// Create menu items
	statusItem := systray.AddMenuItem("Status: Stopped", "Server Status")
	statusItem.Disable()

	systray.AddSeparator()

	// Start/Stop Server
	startItem := systray.AddMenuItem("Start Server", "Start the Ori Agent server")
	stopItem := systray.AddMenuItem("Stop Server", "Stop the Ori Agent server")
	stopItem.Disable()

	// Open Browser
	openBrowserItem := systray.AddMenuItem("Open Browser", "Open Ori Agent in browser")
	openBrowserItem.Disable()

	systray.AddSeparator()

	// Auto-start toggle
	autoStartEnabled := false
	if settingsMgr != nil && launchAgentMgr != nil {
		autoStartEnabled = settingsMgr.GetAutoStartEnabled()
	}

	autoStartItem := systray.AddMenuItemCheckbox("Auto-start on Login", "Launch Ori Agent on system startup", autoStartEnabled)
	if launchAgentMgr == nil {
		autoStartItem.Disable()
	}

	// Port configuration
	currentPort := controller.GetPort()
	portItem := systray.AddMenuItem(fmt.Sprintf("Port: %d", currentPort), "Change server port")

	systray.AddSeparator()

	// About
	aboutItem := systray.AddMenuItem("About Ori Agent", "About this application")

	// Quit
	quitItem := systray.AddMenuItem("Quit", "Quit Ori Agent")

	// Watch for status changes
	controller.WatchStatus(func(status menubar.ServerStatus) {
		updateMenuForStatusSystray(status, controller, statusItem, startItem, stopItem, openBrowserItem)
	})

	// Handle menu item clicks in goroutines
	go func() {
		for {
			select {
			case <-startItem.ClickedCh:
				log.Println("Start Server clicked")
				if controller.GetStatus() != menubar.StatusStopped {
					logger.Info("Server already running", nil)
					continue
				}
				port := controller.GetPort()
				if !ensurePortAvailableForStart(port) {
					continue
				}
				ctx := context.Background()
				if err := controller.StartServer(ctx); err != nil {
					logger.Error("Failed to start server", logger.Fields{"server": err})
				}

			case <-stopItem.ClickedCh:
				log.Println("Stop Server clicked")
				ctx := context.Background()
				if err := controller.StopServer(ctx); err != nil {
					logger.Error("Failed to stop server", logger.Fields{"server": err})
				}

			case <-openBrowserItem.ClickedCh:
				log.Println("Open Browser clicked")
				openBrowser(controller.GetPort())

			case <-autoStartItem.ClickedCh:
				log.Println("Auto-start toggle clicked")
				if launchAgentMgr == nil || settingsMgr == nil {
					log.Println("Auto-start feature not available")
					continue
				}

				// Toggle auto-start
				if autoStartItem.Checked() {
					// Currently checked, so uncheck (disable auto-start)
					log.Println("Disabling auto-start...")
					if err := launchAgentMgr.Uninstall(); err != nil {
						logger.Error("Failed to uninstall LaunchAgent", logger.Fields{"agent": err})
					} else {
						if err := settingsMgr.SetAutoStartEnabled(false); err != nil {
							logger.Error("Failed to save auto-start setting", logger.Fields{"err": err})
						}
						autoStartItem.Uncheck()
						log.Println("Auto-start disabled")
					}
				} else {
					// Currently unchecked, so check (enable auto-start)
					log.Println("Enabling auto-start...")
					if err := launchAgentMgr.Install(); err != nil {
						logger.Error("Failed to install LaunchAgent", logger.Fields{"agent": err})
					} else {
						if err := settingsMgr.SetAutoStartEnabled(true); err != nil {
							logger.Error("Failed to save auto-start setting", logger.Fields{"err": err})
						}
						autoStartItem.Check()
						log.Println("Auto-start enabled")
					}
				}

			case <-portItem.ClickedCh:
				log.Println("Port configuration clicked")
				handlePortConfigurationSystray(controller, settingsMgr, portItem)

			case <-aboutItem.ClickedCh:
				log.Println("About clicked")
				showNotification("About Ori Agent", "Ori Agent - AI Agent Framework\\nVersion 0.0.13")

			case <-quitItem.ClickedCh:
				log.Println("Quit clicked")

				// Stop server if running
				ctx := context.Background()
				if controller.GetStatus() == menubar.StatusRunning {
					log.Println("Stopping server before quit...")
					_ = controller.StopServer(ctx) // Ignore error on exit
				}

				systray.Quit()
				return
			}
		}
	}()
}

func updateMenuForStatusSystray(status menubar.ServerStatus, controller *menubar.Controller, statusItem, startItem, stopItem, openBrowserItem *systray.MenuItem) {
	logger.Debug("Status changed to", logger.Fields{"status": status.String()})

	switch status {
	case menubar.StatusStopped:
		systray.SetIcon(menubar.GetStoppedIcon())
		systray.SetTooltip("Ori Agent - Server Stopped")
		statusItem.SetTitle("Status: Stopped")
		startItem.Enable()
		stopItem.Disable()
		openBrowserItem.Disable()

	case menubar.StatusStarting:
		systray.SetIcon(menubar.GetStartingIcon())
		systray.SetTooltip("Ori Agent - Server Starting...")
		statusItem.SetTitle("Status: Starting...")
		startItem.Disable()
		stopItem.Disable()
		openBrowserItem.Disable()

	case menubar.StatusRunning:
		systray.SetIcon(menubar.GetRunningIcon())
		systray.SetTooltip("Ori Agent - Server Running")
		statusItem.SetTitle("Status: Running")
		startItem.Disable()
		stopItem.Enable()
		openBrowserItem.Enable()

	case menubar.StatusStopping:
		systray.SetIcon(menubar.GetStoppingIcon())
		systray.SetTooltip("Ori Agent - Server Stopping...")
		statusItem.SetTitle("Status: Stopping...")
		startItem.Disable()
		stopItem.Disable()
		openBrowserItem.Disable()

	case menubar.StatusError:
		systray.SetIcon(menubar.GetErrorIcon())
		errMsg := controller.GetErrorMessage()
		systray.SetTooltip("Ori Agent - Error: " + errMsg)
		statusItem.SetTitle("Status: Error - " + errMsg)
		startItem.Enable()
		stopItem.Disable()
		openBrowserItem.Disable()
	}
}

func openBrowser(port int) {
	url := fmt.Sprintf("http://localhost:%d", port)

	// Open browser based on OS
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		cmd = "open" // Default to macOS
		args = []string{url}
	}

	logger.Debug("Opening browser", logger.Fields{"cmd": cmd, "args": args})

	if err := exec.Command(cmd, args...).Start(); err != nil {
		logger.Error("Failed to open browser", logger.Fields{"err": err})
	}
}

func handlePortConfigurationSystray(controller *menubar.Controller, settingsMgr *menubar.SettingsManager, portItem *systray.MenuItem) {
	// Check if server is running
	if controller.GetStatus() != menubar.StatusStopped {
		log.Println("Cannot change port while server is running")
		showNotification("Port Configuration", "Please stop the server before changing the port")
		return
	}

	// Get current port
	currentPort := controller.GetPort()

	// Show input dialog (macOS only for now)
	if runtime.GOOS == "darwin" {
		newPortStr, err := showInputDialog("Server Port Configuration", fmt.Sprintf("Enter new port number (current: %d):", currentPort), fmt.Sprintf("%d", currentPort))
		if err != nil {
			logger.Error("Failed to show port dialog", logger.Fields{"err": err})
			return
		}

		// User cancelled
		if newPortStr == "" {
			log.Println("Port configuration cancelled")
			return
		}

		// Parse the new port
		var newPort int
		if _, err := fmt.Sscanf(newPortStr, "%d", &newPort); err != nil {
			logger.Debug("Invalid port number", logger.Fields{"newPortStr": newPortStr})
			showNotification("Invalid Port", "Please enter a valid port number")
			return
		}

		// Validate port range
		if newPort < 1024 || newPort > 65535 {
			logger.Debug("Port out of range", logger.Fields{"newPort": newPort})
			showNotification("Invalid Port", "Port must be between 1024 and 65535")
			return
		}

		// Update controller
		if err := controller.SetPort(newPort); err != nil {
			logger.Error("Failed to set port on controller", logger.Fields{"err": err})
			showNotification("Error", fmt.Sprintf("Failed to set port: %v", err))
			return
		}

		// Save to settings
		if err := settingsMgr.SetPort(newPort); err != nil {
			logger.Error("Failed to save port to settings", logger.Fields{"err": err})
			showNotification("Error", fmt.Sprintf("Failed to save port: %v", err))
			return
		}

		// Update menu item
		portItem.SetTitle(fmt.Sprintf("Port: %d", newPort))
		logger.Debug("Port updated to", logger.Fields{"newPort": newPort})
		showNotification("Port Updated", fmt.Sprintf("Server port changed to %d", newPort))
	} else {
		log.Println("Port configuration dialog not supported on this platform")
		showNotification("Not Supported", "Port configuration dialog is only supported on macOS")
	}
}

func showInputDialog(title, prompt, defaultValue string) (string, error) {
	script := fmt.Sprintf(`display dialog "%s" default answer "%s" with title "%s"`, prompt, defaultValue, title)
	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		// User cancelled or error
		return "", err
	}

	// Parse output: "button returned:OK, text returned:8080"
	outputStr := strings.TrimSpace(string(output))

	// Find "text returned:" and extract everything after it
	textReturnedPrefix := "text returned:"
	if idx := strings.Index(outputStr, textReturnedPrefix); idx != -1 {
		result := outputStr[idx+len(textReturnedPrefix):]
		return strings.TrimSpace(result), nil
	}

	return "", fmt.Errorf("failed to parse dialog output: %s", outputStr)
}

func showNotification(title, message string) {
	if runtime.GOOS == "darwin" {
		script := fmt.Sprintf(`display notification "%s" with title "%s"`, message, title)
		cmd := exec.Command("osascript", "-e", script)
		if err := cmd.Run(); err != nil {
			logger.Error("Failed to show notification", logger.Fields{"err": err})
		}
	}
}

func ensurePortAvailableForStart(port int) bool {
	processes, err := portutil.FindPortProcesses(port)
	if err != nil {
		logger.Error("Failed to inspect port owners", logger.Fields{"port": port, "error": err.Error()})
	}

	if len(processes) == 0 {
		if portutil.IsPortAvailable(port) {
			return true
		}
		processes = []portutil.ProcessInfo{{PID: 0, Name: ""}}
	}

	allOri := true
	for _, process := range processes {
		if !portutil.IsOriProcessName(process.Name) {
			allOri = false
			break
		}
	}

	summary := portutil.FormatProcessSummary(processes)
	if allOri {
		logger.Info("Found existing ori process(es) on port, stopping...", logger.Fields{"port": port, "processes": summary})
	} else {
		confirmed, dialogErr := confirmStopProcessDialog(port, summary)
		if dialogErr != nil {
			logger.Error("Failed to show confirmation dialog", logger.Fields{"error": dialogErr.Error()})
			showNotification("Port In Use", fmt.Sprintf("Port %d is used by %s.", port, summary))
			return false
		}
		if !confirmed {
			logger.Info("User declined to stop process on port", logger.Fields{"port": port, "processes": summary})
			return false
		}
	}

	if err := portutil.TerminateProcesses(processes); err != nil {
		logger.Error("Failed to stop process on port", logger.Fields{"port": port, "error": err.Error()})
		showNotification("Port In Use", fmt.Sprintf("Failed to stop process on port %d.", port))
		return false
	}

	for i := 0; i < 5; i++ {
		if portutil.IsPortAvailable(port) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}

	showNotification("Port In Use", fmt.Sprintf("Port %d is still in use.", port))
	return false
}

func confirmStopProcessDialog(port int, summary string) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, fmt.Errorf("confirmation dialog not supported on this platform")
	}
	prompt := fmt.Sprintf("Port %d is used by %s. Stop it to continue?", port, summary)
	script := fmt.Sprintf(`display dialog "%s" with title "Port In Use" buttons {"Cancel", "Stop"} default button "Cancel"`, escapeAppleScriptString(prompt))
	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	outputStr := strings.TrimSpace(string(output))
	return strings.Contains(outputStr, "button returned:Stop"), nil
}

func escapeAppleScriptString(value string) string {
	return strings.ReplaceAll(value, "\"", "\\\"")
}
