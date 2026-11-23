//go:build darwin
// +build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getlantern/systray"
	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/environ"
	"github.com/johnjallday/ori-agent/internal/fileparser"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/menubar"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	portutil "github.com/johnjallday/ori-agent/internal/port"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/version"
)

// Guard tests replace only construction, never native credentials or systray.
var newShellOnboarding = onboarding.NewManager
var menuBeforeStores = settingsreset.BeforeStores
var menuPortCheck = ensurePortAvailableForStart
var menuInputDialog = showInputDialog

func main() {
	// The embedded server uses this same image for metadata helpers. Exit before
	// shell settings, credentials, logging, ports or native UI initialization.
	if handled, code := fileparser.RunReviewPDFWorker(os.Args[1:]); handled {
		os.Exit(code)
	}
	if handled, code := backuprecovery.RunHelperProcess(os.Args[1:]); handled {
		os.Exit(code)
	}

	args := os.Args[1:]
	if len(args) != 0 && args[0] == nativeWatchArg {
		if err := runNativeResetWatcher(args[1:]); err != nil {
			log.Fatalf("Cannot reopen Ori after reset: %v. Reopen the same app manually.", err)
		}
		return
	}
	var expectedOperation string
	var restartPort int
	if len(args) != 0 && args[0] == nativeBootArg {
		var err error
		expectedOperation, restartPort, err = parseNativeResetArgs(args[1:])
		if err != nil {
			log.Fatal(err)
		}
	}
	startOnLaunch := len(args) == 1 && args[0] == "--start-server"

	// Expand PATH to include common development tool locations
	// This ensures tools like codex, go, node are available when launched from macOS app bundle
	environ.ExpandPath()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Ori Agent Menu Bar App (systray)...")

	nativeLaunch, launchErr := identifyNativeResetLaunch(runningNativeBundle())
	var relaunchListener net.Listener
	if expectedOperation != "" {
		if launchErr != nil {
			log.Fatalf("Ori reset expected the original macOS app bundle: %v", launchErr)
		}
		var err error
		relaunchListener, err = reserveNativeResetPort(restartPort)
		if err != nil {
			log.Fatalf("Cannot reopen Ori on the original port: %v. Free the port and relaunch manually.", err)
		}
	}
	var dataDir string
	var err error
	if expectedOperation != "" {
		dataDir, err = existingMenubarResetDirectory()
	} else {
		dataDir, err = activateMenubarDataDirectory()
	}
	if err != nil {
		log.Fatalf("Failed to activate data directory: %v", err)
	}
	// The shell has its own settings writer. Acquire BEFORE constructing it,
	// not in Controller.StartServer. Stop/Start Server cannot release this lease;
	// even Shutdown leaves writers alive, so only full process exit may do so.
	var lease *resetstate.Lease
	if expectedOperation != "" {
		lease, err = settingsreset.BeforeStoresForRestart(context.Background(), dataDir, expectedOperation)
	} else {
		lease, err = menuBeforeStores(context.Background(), dataDir)
	}
	if err != nil {
		if lease != nil && errors.Is(err, settingsreset.ErrRecoveryIncomplete) {
			if recoveryErr := runMenubarResetRecovery(lease, relaunchListener); recoveryErr != nil {
				log.Fatalf("Reset recovery host failed: %v", recoveryErr)
			}
			return
		}
		log.Fatalf("Cannot open installation: %v", err)
	}
	logger.Debug("Working directory", logger.Fields{"dataDir": dataDir})

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Initialize settings managers first
	settingsMgr, err := initializeMenubarSettings(lease)
	if err != nil {
		log.Fatalf("Cannot initialize shell settings: %v", err)
	}

	// Get port from settings (defaults to 8765)
	port := settingsMgr.GetPort()
	if restartPort != 0 {
		port = restartPort
	}
	logger.Debug("Using port", logger.Fields{"port": port})

	controller := menubar.NewControllerWithResetLease(port, lease)
	restart := make(chan string, 1)
	var restartUnavailable atomic.Bool
	if launchErr != nil {
		log.Printf("Automatic reset restart is unavailable: %v", launchErr)
	} else {
		controller.SetResetRestartHandoff(settingsreset.NewRestartHandoff(func() bool {
			return !restartUnavailable.Load() && nativeLaunch.unchanged()
		}, func(id string) { restart <- id }))
	}
	manualQuit := make(chan struct{}, 1)
	quitOnDemand := func() {
		select {
		case manualQuit <- struct{}{}:
		default:
		}
	}
	finishRestart := make(chan func(bool) error, 1)

	// Initialize LaunchAgent manager
	launchAgentMgr, err := menubar.NewLaunchAgentManager()
	if err != nil {
		logger.Error("Failed to create LaunchAgent manager", logger.Fields{"error": err})
		log.Println("Auto-start feature will be disabled")
	}

	// Run systray. No ordinary launch changes whether the embedded server
	// starts by default; the reset handoff (or explicit CLI opt-in) starts it.
	onReady := func() {
		log.Println("Systray ready, setting up menu...")
		setupMenuSystray(controller, settingsMgr, launchAgentMgr, quitOnDemand)
		go runMenubarQuitLoop(controller, sigChan, manualQuit, restart, finishRestart, &restartUnavailable, nativeLaunch)
		if relaunchListener != nil || startOnLaunch {
			go func() {
				var err error
				if relaunchListener != nil {
					err = controller.StartAfterReset(context.Background(), relaunchListener)
				} else {
					// Explicit launch-time start is strict: never prompt for takeover.
					err = controller.StartServer(context.Background())
				}
				if err != nil {
					logger.Error("Could not start Ori server; check the port or start it from the menu", logger.Fields{"error": err})
				}
			}()
		}
	}

	onExit := func() {
		log.Println("Systray exiting...")
		// Ensure server is stopped
		ctx := context.Background()
		if controller.GetStatus() == menubar.StatusRunning {
			log.Println("Stopping server before exit...")
			_ = controller.StopServer(ctx) // Best effort stop on exit
		}
		// AppKit terminates after applicationWillTerminate: systray.Run does
		// not return on that path. Release the armed helper here, and it still
		// waits for the exact kernel-reported exit before opening anything.
		select {
		case finish := <-finishRestart:
			commit := true
			select {
			case <-sigChan:
				commit = false
			case <-manualQuit:
				commit = false
			default:
			}
			if err := finish(commit); err != nil {
				logger.Error("Could not hand off reset restart; reopen Ori manually", logger.Fields{"error": err})
			}
		default:
		}
	}

	systray.Run(onReady, onExit)
	log.Println("Systray app exited")
}

func runMenubarQuitLoop(controller *menubar.Controller, signals <-chan os.Signal, manual <-chan struct{}, restart <-chan string, finishRestart chan<- func(bool) error, unavailable *atomic.Bool, launch nativeResetLaunch) {
	for {
		var id string
		select {
		case <-signals:
		case <-manual:
		case id = <-restart:
			select {
			case <-signals:
				id = ""
			case <-manual:
				id = ""
			case <-time.After(2 * time.Second):
			}
		}
		if id == "" {
			if controller.GetStatus() == menubar.StatusRunning {
				_ = controller.StopServer(context.Background())
			}
			systray.Quit()
			return
		}
		finish, err := launch.startWatcher(id, controller.GetPort())
		if err == nil {
			err = controller.StopServer(context.Background())
		}
		if err != nil {
			unavailable.Store(true)
			if finish != nil {
				_ = finish(false)
			}
			logger.Error("Could not restart Ori automatically; quit and reopen the same app", logger.Fields{"error": err})
			continue
		}
		finishRestart <- finish
		systray.Quit()
		return
	}
}

func runMenubarResetRecovery(lease *resetstate.Lease, listener net.Listener) error {
	port := menubarRecoveryPort(lease.Path())
	if tcp, ok := listenerAddr(listener).(*net.TCPAddr); ok {
		port = tcp.Port
	}
	address := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://localhost:%d/", port)
	httpServer := settingsreset.RecoveryHTTPServer(lease, address)
	if listener != nil {
		// The pre-bound embedded-server address may be wildcard. Keep the
		// recovery-only surface local, as on a manual recovery launch.
		recovery := httpServer.Handler
		httpServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil || !net.ParseIP(host).IsLoopback() {
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, "Reset recovery is available on this Mac only.", http.StatusForbidden)
				return
			}
			recovery.ServeHTTP(w, r)
		})
	}
	serveErr := make(chan error, 1)
	go func() {
		serve := httpServer.ListenAndServe
		if listener != nil {
			serve = func() error { return httpServer.Serve(listener) }
		}
		err := serve()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()
	logger.Warn("Ori started in reset recovery mode; shell and application stores remain closed", logger.Fields{"url": url})
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)
	result := make(chan error, 1)
	systray.Run(func() {
		systray.SetIcon(menubar.GetStoppedIcon())
		systray.SetTooltip("Ori Agent - Reset recovery")
		open := systray.AddMenuItem("Open Reset Status", "View the reset recovery result")
		quitItem := systray.AddMenuItem("Quit", "Quit Ori Agent")
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					openBrowser(port)
					continue
				case <-quitItem.ClickedCh:
				case <-quit:
				case err := <-serveErr:
					result <- err
				}
				systray.Quit()
				return
			}
		}()
	}, func() {})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var serveFailure error
	select {
	case serveFailure = <-result:
	default:
	}
	return errors.Join(serveFailure, httpServer.Shutdown(ctx))
}

func listenerAddr(listener net.Listener) net.Addr {
	if listener == nil {
		return nil
	}
	return listener.Addr()
}

// Converge shell CWD and server ORI_DATA_DIR, including explicit overrides.
// No settings manager, credential backend or native UI is initialized here.
func activateMenubarDataDirectory() (string, error) {
	dataDir, err := filepath.Abs(config.DefaultDataDir())
	if err != nil {
		return "", err
	}
	if _, err := onboarding.CreateInstallRoot(dataDir); err != nil {
		return "", err
	}
	dataDir, err = filepath.EvalSymlinks(dataDir)
	if err != nil {
		return "", err
	}
	return enterMenubarDataDirectory(dataDir)
}

func existingMenubarResetDirectory() (string, error) {
	root := os.Getenv("ORI_DATA_DIR")
	if !filepath.IsAbs(root) {
		return "", errors.New("original Ori data directory was not preserved by relaunch")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return "", errors.New("original Ori data directory changed during relaunch")
	}
	return enterMenubarDataDirectory(root)
}

func enterMenubarDataDirectory(dataDir string) (string, error) {
	if err := os.Chdir(dataDir); err != nil {
		return "", err
	}
	if err := os.Setenv("ORI_DATA_DIR", dataDir); err != nil {
		return "", err
	}
	return dataDir, nil
}

// Recheck the shared admission/root boundary immediately before shell owners.
func initializeMenubarSettings(lease *resetstate.Lease) (*menubar.SettingsManager, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	release, err := lease.EnterRuntime(cwd, os.Getenv("ORI_DATA_DIR"))
	if err != nil {
		return nil, err
	}
	defer release()
	manager := newShellOnboarding("app_state.json")
	return menubar.NewSettingsManagerWithAdmission(manager, lease.WorkGate()), nil
}

func startMenuServer(controller *menubar.Controller, settingsMgr *menubar.SettingsManager) error {
	return settingsMgr.WithMutation(func() error {
		return controller.StartServerWithPreflight(context.Background(), func(port int) error {
			if !menuPortCheck(port) {
				return fmt.Errorf("server start cancelled during port preflight")
			}
			return nil
		})
	})
}

func setupMenuSystray(controller *menubar.Controller, settingsMgr *menubar.SettingsManager, launchAgentMgr *menubar.LaunchAgentManager, quit func()) {
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
				if err := startMenuServer(controller, settingsMgr); err != nil {
					logger.Error("Failed to start server", logger.Fields{"error": err})
				}

			case <-stopItem.ClickedCh:
				log.Println("Stop Server clicked")
				ctx := context.Background()
				if err := controller.StopServer(ctx); err != nil {
					logger.Error("Failed to stop server", logger.Fields{"error": err})
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

				if err := settingsMgr.WithMutation(func() error {
					// Keep OS registration and the shell save under one permit.
					if autoStartItem.Checked() {
						// Currently checked, so uncheck (disable auto-start)
						log.Println("Disabling auto-start...")
						if err := launchAgentMgr.Uninstall(); err != nil {
							logger.Error("Failed to uninstall LaunchAgent", logger.Fields{"error": err})
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
							logger.Error("Failed to install LaunchAgent", logger.Fields{"error": err})
						} else {
							if err := settingsMgr.SetAutoStartEnabled(true); err != nil {
								logger.Error("Failed to save auto-start setting", logger.Fields{"err": err})
							}
							autoStartItem.Check()
							log.Println("Auto-start enabled")
						}
					}
					return nil
				}); err != nil {
					logger.Error("Auto-start change blocked", logger.Fields{"error": err})
				}

			case <-portItem.ClickedCh:
				log.Println("Port configuration clicked")
				handlePortConfigurationSystray(controller, settingsMgr, portItem)

			case <-aboutItem.ClickedCh:
				log.Println("About clicked")
				showNotification("About Ori Agent", fmt.Sprintf("Ori Agent - AI Agent Framework\nVersion %s", version.GetVersion()))

			case <-quitItem.ClickedCh:
				log.Println("Quit clicked")

				quit()
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
		if controller.CanStart() {
			startItem.Enable()
		} else {
			startItem.Disable()
		}
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
		if controller.CanStart() {
			startItem.Enable()
		} else {
			startItem.Disable()
		}
		if controller.CanStop() {
			stopItem.Enable()
		} else {
			stopItem.Disable()
		}
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
	if err := settingsMgr.WithMutation(func() error {
		handleAdmittedPortConfiguration(controller, settingsMgr, portItem)
		return nil
	}); err != nil {
		logger.Error("Port change blocked", logger.Fields{"error": err})
	}
}

func handleAdmittedPortConfiguration(controller *menubar.Controller, settingsMgr *menubar.SettingsManager, portItem *systray.MenuItem) {
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
		newPortStr, err := menuInputDialog("Server Port Configuration", fmt.Sprintf("Enter new port number (current: %d):", currentPort), fmt.Sprintf("%d", currentPort))
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
	script := fmt.Sprintf(`display dialog "%s" default answer "%s" with title "%s"`,
		escapeAppleScriptString(prompt), escapeAppleScriptString(defaultValue), escapeAppleScriptString(title))
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
		script := fmt.Sprintf(`display notification "%s" with title "%s"`,
			escapeAppleScriptString(message), escapeAppleScriptString(title))
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
	// Backslashes must be escaped before quotes, or the quote escaping
	// itself gets double-escaped.
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "\"", "\\\"")
}
