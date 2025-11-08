package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
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

	// Create server controller
	port := 8765
	if portEnv := os.Getenv("PORT"); portEnv != "" {
		// Could parse portEnv if needed, but 8765 is default
	}

	controller := menubar.NewController(port)

	// Initialize settings managers
	onboardingMgr := onboarding.NewManager("app_state.json")
	settingsMgr := menubar.NewSettingsManager(onboardingMgr)

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

	// Check if auto-start is currently enabled
	autoStartEnabled := false
	if settingsMgr != nil && launchAgentMgr != nil {
		autoStartEnabled = settingsMgr.GetAutoStartEnabled()
	}
	autoStartItem := systray.AddMenuItemCheckbox("Auto-start on Login", "Launch Ori Agent on system startup", autoStartEnabled)

	// Disable auto-start if manager is not available
	if launchAgentMgr == nil {
		autoStartItem.Disable()
	}

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
