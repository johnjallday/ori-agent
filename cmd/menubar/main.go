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

	"fyne.io/systray"
	"github.com/johnjallday/ori-agent/internal/menubar"
	"github.com/johnjallday/ori-agent/internal/onboarding"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Ori Agent Menu Bar App...")

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

		log.Println("Quitting systray...")
		systray.Quit()
	}()

	// Run the systray app
	onReady := func() {
		log.Println("Systray ready, setting up menu...")
		setupMenu(controller, settingsMgr, launchAgentMgr)
	}

	onExit := func() {
		log.Println("Systray exiting...")
		// Ensure server is stopped
		ctx := context.Background()
		if controller.GetStatus() == menubar.StatusRunning {
			log.Println("Stopping server before exit...")
			controller.StopServer(ctx)
		}
	}

	systray.Run(onReady, onExit)
	log.Println("Menu bar app exited")
}

func setupMenu(controller *menubar.Controller, settingsMgr *menubar.SettingsManager, launchAgentMgr *menubar.LaunchAgentManager) {
	// Set initial icon and tooltip
	systray.SetIcon(menubar.GetStoppedIcon())
	systray.SetTitle("Ori")
	systray.SetTooltip("Ori Agent - Server Stopped")

	// Create menu items
	statusItem := systray.AddMenuItem("Status: Stopped", "Server Status")
	statusItem.Disable()

	systray.AddSeparator()

	startItem := systray.AddMenuItem("Start Server", "Start the Ori Agent server")
	stopItem := systray.AddMenuItem("Stop Server", "Stop the Ori Agent server")
	stopItem.Disable()

	openBrowserItem := systray.AddMenuItem("Open Browser", "Open Ori Agent in browser")
	openBrowserItem.Disable()

	systray.AddSeparator()

	// Settings submenu
	settingsMenu := systray.AddMenuItem("Settings", "Application Settings")

	// Check if auto-start is currently enabled
	autoStartEnabled := false
	if settingsMgr != nil && launchAgentMgr != nil {
		autoStartEnabled = settingsMgr.GetAutoStartEnabled()
	}
	autoStartItem := settingsMenu.AddSubMenuItemCheckbox("Auto-start on Login", "Launch Ori Agent on system startup", autoStartEnabled)

	// Disable auto-start if manager is not available
	if launchAgentMgr == nil {
		autoStartItem.Disable()
	}

	// Port configuration
	currentPort := controller.GetPort()
	portItem := settingsMenu.AddSubMenuItem(fmt.Sprintf("Port: %d", currentPort), "Change server port")

	systray.AddSeparator()

	aboutItem := systray.AddMenuItem("About Ori Agent", "About this application")
	quitItem := systray.AddMenuItem("Quit", "Quit Ori Agent")

	// Watch for status changes
	controller.WatchStatus(func(status menubar.ServerStatus) {
		updateMenuForStatus(status, controller, statusItem, startItem, stopItem, openBrowserItem)
	})

	// Handle menu item clicks
	go func() {
		for {
			select {
			case <-startItem.ClickedCh:
				log.Println("Start Server clicked")
				ctx := context.Background()
				if err := controller.StartServer(ctx); err != nil {
					log.Printf("Failed to start server: %v", err)
				}

			case <-stopItem.ClickedCh:
				log.Println("Stop Server clicked")
				ctx := context.Background()
				if err := controller.StopServer(ctx); err != nil {
					log.Printf("Failed to stop server: %v", err)
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
						log.Printf("Failed to uninstall LaunchAgent: %v", err)
					} else {
						if err := settingsMgr.SetAutoStartEnabled(false); err != nil {
							log.Printf("Failed to save auto-start setting: %v", err)
						}
						autoStartItem.Uncheck()
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
						autoStartItem.Check()
						log.Println("Auto-start enabled")
					}
				}

			case <-portItem.ClickedCh:
				log.Println("Port configuration clicked")
				handlePortConfiguration(controller, settingsMgr, portItem)

			case <-aboutItem.ClickedCh:
				log.Println("About clicked")
				// TODO: Show about dialog (could use notification for now)

			case <-quitItem.ClickedCh:
				log.Println("Quit clicked")

				// Stop server if running
				ctx := context.Background()
				if controller.GetStatus() == menubar.StatusRunning {
					log.Println("Stopping server before quit...")
					controller.StopServer(ctx)
				}

				systray.Quit()
				return
			}
		}
	}()
}

func updateMenuForStatus(status menubar.ServerStatus, controller *menubar.Controller, statusItem, startItem, stopItem, openBrowserItem *systray.MenuItem) {
	log.Printf("Status changed to: %s", status.String())

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

	log.Printf("Opening browser: %s %v", cmd, args)

	if err := exec.Command(cmd, args...).Start(); err != nil {
		log.Printf("Failed to open browser: %v", err)
	}
}

func handlePortConfiguration(controller *menubar.Controller, settingsMgr *menubar.SettingsManager, portItem *systray.MenuItem) {
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
		portItem.SetTitle(fmt.Sprintf("Port: %d", newPort))
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
