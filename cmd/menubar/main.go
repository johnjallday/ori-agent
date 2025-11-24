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

	"github.com/johnjallday/ori-agent/internal/menubar"
	"github.com/johnjallday/ori-agent/internal/nativemenubar"
	"github.com/johnjallday/ori-agent/internal/onboarding"
)

func main() {
	// Lock main goroutine to main OS thread for Cocoa
	// This is REQUIRED for macOS NSApplication to work properly
	runtime.LockOSThread()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Ori Agent Menu Bar App (Native)...")

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Initialize settings managers first
	onboardingMgr := onboarding.NewManager("app_state.json")
	settingsMgr := menubar.NewSettingsManager(onboardingMgr)

	// Get port from settings (defaults to 8765)
	port := settingsMgr.GetPort()
	log.Printf("Using port: %d", port)

	controller := menubar.NewController(port)

	// Initialize LaunchAgent manager
	launchAgentMgr, err := menubar.NewLaunchAgentManager()
	if err != nil {
		log.Printf("Warning: Failed to create LaunchAgent manager: %v", err)
		log.Println("Auto-start feature will be disabled")
	}

	// Initialize native menu bar
	mb := nativemenubar.Initialize()

	// Handle signals in a goroutine
	go func() {
		<-sigChan
		log.Println("Received shutdown signal...")

		// Stop server if running
		ctx := context.Background()
		if controller.GetStatus() == menubar.StatusRunning {
			log.Println("Stopping server...")
			if err := controller.StopServer(ctx); err != nil {
				log.Printf("Error stopping server: %v", err)
			}
		}

		log.Println("Quitting menu bar...")
		mb.Quit()
	}()

	// Run the menu bar app
	onReady := func() {
		log.Println("Menu bar ready, setting up menu...")
		setupMenuNative(mb, controller, settingsMgr, launchAgentMgr)
	}

	onExit := func() {
		log.Println("Menu bar exiting...")
		// Ensure server is stopped
		ctx := context.Background()
		if controller.GetStatus() == menubar.StatusRunning {
			log.Println("Stopping server before exit...")
			_ = controller.StopServer(ctx) // Best effort stop on exit
		}
	}

	mb.Run(onReady, onExit)
	log.Println("Menu bar app exited")
}

func setupMenuNative(mb *nativemenubar.MenuBar, controller *menubar.Controller, settingsMgr *menubar.SettingsManager, launchAgentMgr *menubar.LaunchAgentManager) {
	// Set initial icon and tooltip
	mb.SetIcon(menubar.GetStoppedIcon())
	mb.SetTooltip("Ori Agent - Server Stopped")

	// Create menu items
	statusItem := mb.AddMenuItem("Status: Stopped", "Server Status", nil)
	mb.SetItemEnabled(statusItem, false)

	mb.AddSeparator()

	// Start/Stop Server
	startItem := mb.AddMenuItem("Start Server", "Start the Ori Agent server", func() {
		log.Println("Start Server clicked")
		ctx := context.Background()
		if err := controller.StartServer(ctx); err != nil {
			log.Printf("Failed to start server: %v", err)
		}
	})

	stopItem := mb.AddMenuItem("Stop Server", "Stop the Ori Agent server", func() {
		log.Println("Stop Server clicked")
		ctx := context.Background()
		if err := controller.StopServer(ctx); err != nil {
			log.Printf("Failed to stop server: %v", err)
		}
	})
	mb.SetItemEnabled(stopItem, false)

	// Open Browser
	openBrowserItem := mb.AddMenuItem("Open Browser", "Open Ori Agent in browser", func() {
		log.Println("Open Browser clicked")
		openBrowser(controller.GetPort())
	})
	mb.SetItemEnabled(openBrowserItem, false)

	mb.AddSeparator()

	// Auto-start toggle
	autoStartEnabled := false
	if settingsMgr != nil && launchAgentMgr != nil {
		autoStartEnabled = settingsMgr.GetAutoStartEnabled()
	}

	var autoStartItem *nativemenubar.MenuItem
	autoStartItem = mb.AddMenuItem("Auto-start on Login", "Launch Ori Agent on system startup", func() {
		log.Println("Auto-start toggle clicked")
		if launchAgentMgr == nil || settingsMgr == nil {
			log.Println("Auto-start feature not available")
			return
		}

		// Toggle auto-start
		if autoStartItem.Checked {
			// Currently checked, so uncheck (disable auto-start)
			log.Println("Disabling auto-start...")
			if err := launchAgentMgr.Uninstall(); err != nil {
				log.Printf("Failed to uninstall LaunchAgent: %v", err)
			} else {
				if err := settingsMgr.SetAutoStartEnabled(false); err != nil {
					log.Printf("Failed to save auto-start setting: %v", err)
				}
				mb.SetItemChecked(autoStartItem, false)
				log.Println("Auto-start disabled")
			}
		} else {
			// Currently unchecked, so check (enable auto-start)
			log.Println("Enabling auto-start...")
			if err := launchAgentMgr.Install(); err != nil {
				log.Printf("Failed to install LaunchAgent: %v", err)
			} else {
				if err := settingsMgr.SetAutoStartEnabled(true); err != nil {
					log.Printf("Failed to save auto-start setting: %v", err)
				}
				mb.SetItemChecked(autoStartItem, true)
				log.Println("Auto-start enabled")
			}
		}
	})
	mb.SetItemChecked(autoStartItem, autoStartEnabled)

	if launchAgentMgr == nil {
		mb.SetItemEnabled(autoStartItem, false)
	}

	// Port configuration
	currentPort := controller.GetPort()
	var portItem *nativemenubar.MenuItem
	portItem = mb.AddMenuItem(fmt.Sprintf("Port: %d", currentPort), "Change server port", func() {
		log.Println("Port configuration clicked")
		handlePortConfigurationNative(mb, controller, settingsMgr, portItem)
	})

	mb.AddSeparator()

	// About
	mb.AddMenuItem("About Ori Agent", "About this application", func() {
		log.Println("About clicked")
		showNotification("About Ori Agent", "Ori Agent - AI Agent Framework\nVersion 0.0.13")
	})

	// Quit
	mb.AddMenuItem("Quit", "Quit Ori Agent", func() {
		log.Println("Quit clicked")

		// Stop server if running
		ctx := context.Background()
		if controller.GetStatus() == menubar.StatusRunning {
			log.Println("Stopping server before quit...")
			_ = controller.StopServer(ctx) // Ignore error on exit
		}

		mb.Quit()
	})

	// Watch for status changes
	controller.WatchStatus(func(status menubar.ServerStatus) {
		updateMenuForStatusNative(mb, status, controller, statusItem, startItem, stopItem, openBrowserItem)
	})
}

func updateMenuForStatusNative(mb *nativemenubar.MenuBar, status menubar.ServerStatus, controller *menubar.Controller, statusItem, startItem, stopItem, openBrowserItem *nativemenubar.MenuItem) {
	log.Printf("Status changed to: %s", status.String())

	switch status {
	case menubar.StatusStopped:
		mb.SetIcon(menubar.GetStoppedIcon())
		mb.SetTooltip("Ori Agent - Server Stopped")
		mb.SetItemTitle(statusItem, "Status: Stopped")
		mb.SetItemEnabled(startItem, true)
		mb.SetItemEnabled(stopItem, false)
		mb.SetItemEnabled(openBrowserItem, false)

	case menubar.StatusStarting:
		mb.SetIcon(menubar.GetStartingIcon())
		mb.SetTooltip("Ori Agent - Server Starting...")
		mb.SetItemTitle(statusItem, "Status: Starting...")
		mb.SetItemEnabled(startItem, false)
		mb.SetItemEnabled(stopItem, false)
		mb.SetItemEnabled(openBrowserItem, false)

	case menubar.StatusRunning:
		mb.SetIcon(menubar.GetRunningIcon())
		mb.SetTooltip("Ori Agent - Server Running")
		mb.SetItemTitle(statusItem, "Status: Running")
		mb.SetItemEnabled(startItem, false)
		mb.SetItemEnabled(stopItem, true)
		mb.SetItemEnabled(openBrowserItem, true)

	case menubar.StatusStopping:
		mb.SetIcon(menubar.GetStoppingIcon())
		mb.SetTooltip("Ori Agent - Server Stopping...")
		mb.SetItemTitle(statusItem, "Status: Stopping...")
		mb.SetItemEnabled(startItem, false)
		mb.SetItemEnabled(stopItem, false)
		mb.SetItemEnabled(openBrowserItem, false)

	case menubar.StatusError:
		mb.SetIcon(menubar.GetErrorIcon())
		errMsg := controller.GetErrorMessage()
		mb.SetTooltip("Ori Agent - Error: " + errMsg)
		mb.SetItemTitle(statusItem, "Status: Error - "+errMsg)
		mb.SetItemEnabled(startItem, true)
		mb.SetItemEnabled(stopItem, false)
		mb.SetItemEnabled(openBrowserItem, false)
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

	log.Printf("Opening browser: %s %v", cmd, args)

	if err := exec.Command(cmd, args...).Start(); err != nil {
		log.Printf("Failed to open browser: %v", err)
	}
}

func handlePortConfigurationNative(mb *nativemenubar.MenuBar, controller *menubar.Controller, settingsMgr *menubar.SettingsManager, portItem *nativemenubar.MenuItem) {
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
			log.Printf("Failed to show port dialog: %v", err)
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
			log.Printf("Invalid port number: %s", newPortStr)
			showNotification("Invalid Port", "Please enter a valid port number")
			return
		}

		// Validate port range
		if newPort < 1024 || newPort > 65535 {
			log.Printf("Port out of range: %d", newPort)
			showNotification("Invalid Port", "Port must be between 1024 and 65535")
			return
		}

		// Update controller
		if err := controller.SetPort(newPort); err != nil {
			log.Printf("Failed to set port on controller: %v", err)
			showNotification("Error", fmt.Sprintf("Failed to set port: %v", err))
			return
		}

		// Save to settings
		if err := settingsMgr.SetPort(newPort); err != nil {
			log.Printf("Failed to save port to settings: %v", err)
			showNotification("Error", fmt.Sprintf("Failed to save port: %v", err))
			return
		}

		// Update menu item
		mb.SetItemTitle(portItem, fmt.Sprintf("Port: %d", newPort))
		log.Printf("Port updated to %d", newPort)
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
			log.Printf("Failed to show notification: %v", err)
		}
	}
}
