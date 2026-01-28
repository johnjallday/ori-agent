package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/johnjallday/ori-agent/internal/environ"
	"github.com/johnjallday/ori-agent/internal/logger"
	portutil "github.com/johnjallday/ori-agent/internal/port"
	"github.com/johnjallday/ori-agent/internal/server"
	"github.com/johnjallday/ori-agent/internal/version"
)

func main() {
	// Expand PATH to include common development tool locations
	// This ensures tools like codex, go, node are available when launched from macOS app bundle
	environ.ExpandPath()

	// Define command-line flags
	port := flag.Int("port", 8765, "Port to run server on")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	noBrowser := flag.Bool("no-browser", false, "Don't open browser on startup")
	allowNetwork := flag.Bool("allow-network", false, "Allow connections from network (default: localhost only)")
	versionOverride := flag.String("version", "", "Override version for testing (e.g., v0.0.24)")
	multiAgentMode := flag.String("multi-agent-mode", "", "Multi-agent default mode: auto, force, off")
	multiAgentThreshold := flag.Float64("multi-agent-threshold", 0, "Multi-agent complexity threshold (0-10)")
	shutdownTimeout := flag.Duration("shutdown-timeout", 30*time.Second, "Graceful shutdown timeout (e.g., 30s, 1m)")
	flag.Parse()

	// Check for version as positional argument (e.g., go run ./cmd/server v0.0.24)
	if *versionOverride == "" && flag.NArg() > 0 {
		arg := flag.Arg(0)
		if strings.HasPrefix(arg, "v") || strings.Contains(arg, ".") {
			*versionOverride = arg
		}
	}

	// Apply version override if specified
	if *versionOverride != "" {
		version.Version = *versionOverride
		logger.Info("Version override applied", logger.Fields{"version": *versionOverride})
	}

	// Set verbose mode globally
	_ = os.Setenv("ORI_VERBOSE", fmt.Sprintf("%t", *verbose))

	// Check for PORT environment variable override
	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			*port = p
		}
	}

	// Ensure we're running in a proper data directory
	if err := ensureDataDirectory(); err != nil {
		log.Fatalf("Failed to setup data directory: %v", err)
	}

	// Ensure port is safe to take before starting
	if err := ensurePortAvailable(*port); err != nil {
		log.Fatalf("Port %d is unavailable: %v", *port, err)
	}

	// Kill orphaned plugin processes
	cleanupOrphanedPlugins()

	// Create server with all dependencies
	srv, err := server.New()
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}
	if srv.Core != nil && srv.Core.ConfigManager != nil {
		if *multiAgentMode != "" || *multiAgentThreshold > 0 {
			srv.Core.ConfigManager.SetMultiAgentDefaults(*multiAgentMode, *multiAgentThreshold)
		}
	}

	// Start HTTP server with configured port
	// SECURITY: Bind to localhost only by default to prevent network exposure
	var addr string
	if *allowNetwork {
		addr = fmt.Sprintf(":%d", *port) // 0.0.0.0 - accessible from network
		logger.Warn("Server bound to all interfaces - accessible from network", logger.Fields{"port": *port})
	} else {
		addr = fmt.Sprintf("127.0.0.1:%d", *port) // localhost only
	}
	url := fmt.Sprintf("http://localhost:%d", *port)
	logger.Debug("Listening on", logger.Fields{"url": url})

	// Launch browser in background after a short delay (unless disabled)
	// Skip if --no-browser flag is set or NO_BROWSER env var is set
	if !*noBrowser && os.Getenv("NO_BROWSER") == "" {
		go func() {
			time.Sleep(500 * time.Millisecond) // Wait for server to start
			if err := openBrowser(url); err != nil {
				logger.Debug("Could not auto-open browser", logger.Fields{"err": err})
				logger.Debug("Please open your browser manually and navigate to", logger.Fields{"url": url})
			} else {
				logger.Debug("Browser opened at", logger.Fields{"url": url})
			}
		}()
	} else {
		logger.Debug("Auto-open browser disabled. Navigate to", logger.Fields{"url": url})
	}

	// Create HTTP server
	httpServer := srv.HTTPServer(addr)

	// Set up graceful shutdown on SIGINT/SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in background
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-quit
	logger.Info("Shutting down server...", nil)

	// Clean up plugins and background services
	srv.Shutdown()

	// Graceful HTTP shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		// Context deadline exceeded is expected when SSE/streaming connections are active
		// The server still stops, connections are just closed forcefully
		if err == context.DeadlineExceeded {
			logger.Warn("HTTP server shutdown timeout (active streaming connections were closed forcefully)", nil)
		} else {
			logger.Error("HTTP server shutdown error", logger.Fields{"error": err.Error()})
		}
	}

	logger.Info("Server stopped", nil)
}

// ensureDataDirectory checks if runtime data files exist in current directory.
// If they don't exist and we're running as a standalone binary, create an ori-data folder.
func ensureDataDirectory() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// Check if we're already in a data directory or if data files exist
	baseName := filepath.Base(cwd)
	hasDataFiles := fileExists("agents.json") ||
		fileExists("local_plugin_registry.json") ||
		fileExists("plugin_cache") ||
		fileExists("uploaded_plugins")

	// If already in ori-data directory (or OriAgent for installed app) or data files exist, we're good
	if baseName == "ori-data" || baseName == "OriAgent" || hasDataFiles {
		return nil
	}

	// If running from a macOS app bundle, use Application Support directory
	if runtime.GOOS == "darwin" {
		if exePath, err := os.Executable(); err == nil && strings.Contains(exePath, ".app/Contents/MacOS") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			dataDir := filepath.Join(homeDir, "Library", "Application Support", "OriAgent")
			if err := os.MkdirAll(dataDir, 0755); err != nil {
				return err
			}
			if err := os.Chdir(dataDir); err != nil {
				return err
			}
			logger.Debug("Working directory", logger.Fields{"dataDir": dataDir})
			return nil
		}
	}

	// Create ori-data directory and change into it
	dataDir := filepath.Join(cwd, "ori-data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	logger.Info("Created data directory", logger.Fields{"dataDir": dataDir})

	// Change working directory to the data directory
	if err := os.Chdir(dataDir); err != nil {
		return err
	}

	logger.Debug("Working directory", logger.Fields{"dataDir": dataDir})
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// openBrowser opens the specified URL in the default browser
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin": // macOS
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return nil // Unsupported platform, silently skip
	}

	// Set a timeout to prevent hanging
	if err := cmd.Start(); err != nil {
		// If fork/exec fails due to resource limits, return a helpful error
		return fmt.Errorf("unable to open browser (you may need to open it manually): %w", err)
	}

	// Don't wait for the command to finish - let it run in background
	// Use a goroutine to clean up the process without blocking
	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

// ensurePortAvailable stops prior ori processes when safe and prompts before
// terminating non-ori processes that occupy the port.
func ensurePortAvailable(port int) error {
	processes, err := portutil.FindPortProcesses(port)
	if err != nil {
		logger.Debug("Failed to inspect port owners", logger.Fields{"port": port, "error": err.Error()})
	}

	if len(processes) == 0 {
		if portutil.IsPortAvailable(port) {
			return nil
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
		logger.Info("Port is in use by another process", logger.Fields{"port": port, "processes": summary})
		if !isInteractive() {
			return fmt.Errorf("port %d is in use by another process and no TTY is available", port)
		}
		confirmed, promptErr := promptToStopProcesses(port, summary)
		if promptErr != nil {
			return promptErr
		}
		if !confirmed {
			return fmt.Errorf("port %d is in use by another process", port)
		}
	}

	if err := portutil.TerminateProcesses(processes); err != nil {
		return fmt.Errorf("failed to stop process on port %d: %w", port, err)
	}

	for i := 0; i < 5; i++ {
		if portutil.IsPortAvailable(port) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("port %d is still in use", port)
}

func isInteractive() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}

func promptToStopProcesses(port int, summary string) (bool, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Port %d is used by %s. Stop it to continue? [y/N]: ", port, summary)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes", nil
}

// cleanupOrphanedPlugins kills only truly orphaned plugin processes.
// A plugin is considered orphaned if its parent process is init/launchd (PID 1),
// meaning the original ori-agent server that spawned it has died.
// This prevents killing plugins belonging to other running ori-agent instances.
func cleanupOrphanedPlugins() {
	candidatePids := make(map[int]struct{})

	// Get the current working directory to make matching more specific
	cwd, err := os.Getwd()
	if err != nil {
		logger.Debug("Could not get working directory for plugin cleanup", logger.Fields{"error": err.Error()})
		cwd = ""
	}

	switch runtime.GOOS {
	case "darwin", "linux":
		// Use pgrep for cleaner process matching
		var pattern string
		if cwd != "" {
			pattern = fmt.Sprintf("%s/(uploaded_plugins|example_plugins|plugin_cache)/", cwd)
		} else {
			pattern = "(uploaded_plugins|example_plugins|plugin_cache)/"
		}
		cmd := exec.Command("pgrep", "-f", pattern)
		output, err := cmd.Output()
		if err != nil {
			logger.Debug("No plugin processes found", nil)
			return
		}
		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			pidStr := strings.TrimSpace(scanner.Text())
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				candidatePids[pid] = struct{}{}
			}
		}

	case "windows":
		var pattern string
		if cwd != "" {
			escapedCwd := strings.ReplaceAll(cwd, `\`, `\\`)
			pattern = fmt.Sprintf(`%s\\(uploaded_plugins|example_plugins|plugin_cache)\\`, escapedCwd)
		} else {
			pattern = `(uploaded_plugins|example_plugins|plugin_cache)\\`
		}
		psCmd := fmt.Sprintf(`Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -match '%s' } | Select-Object -ExpandProperty ProcessId`, pattern)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		output, err := cmd.Output()
		if err != nil {
			logger.Debug("No plugin processes found", nil)
			return
		}
		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			pidStr := strings.TrimSpace(scanner.Text())
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				candidatePids[pid] = struct{}{}
			}
		}

	default:
		return
	}

	if len(candidatePids) == 0 {
		return
	}

	// Filter to only truly orphaned processes (parent is init/launchd, PID 1)
	orphanedPids := make([]int, 0)
	for pid := range candidatePids {
		if isOrphanedProcess(pid) {
			orphanedPids = append(orphanedPids, pid)
		}
	}

	if len(orphanedPids) == 0 {
		logger.Debug("Plugin processes found but none are orphaned", logger.Fields{
			"total": len(candidatePids),
		})
		return
	}

	logger.Info("Found orphaned plugin process(es), killing...", logger.Fields{
		"count": len(orphanedPids),
	})

	// Kill only orphaned processes
	for _, pid := range orphanedPids {
		pidStr := strconv.Itoa(pid)
		var killCmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin", "linux":
			killCmd = exec.Command("kill", pidStr)
		case "windows":
			killCmd = exec.Command("taskkill", "/F", "/PID", pidStr)
		}
		if killCmd != nil {
			_ = killCmd.Run()
		}
	}
}

// isOrphanedProcess checks if a process is orphaned by checking its parent PID.
// On Unix, orphaned processes get reparented to init (PID 1) or launchd.
// On Windows, we check if the parent process no longer exists.
func isOrphanedProcess(pid int) bool {
	switch runtime.GOOS {
	case "darwin", "linux":
		// Get parent PID using ps
		cmd := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid))
		output, err := cmd.Output()
		if err != nil {
			// Process may have exited, consider it orphaned
			return true
		}
		ppidStr := strings.TrimSpace(string(output))
		ppid, err := strconv.Atoi(ppidStr)
		if err != nil {
			return true
		}
		// PID 1 is init/launchd - process was reparented, meaning parent died
		return ppid == 1

	case "windows":
		// On Windows, check if parent process exists
		psCmd := fmt.Sprintf(`(Get-CimInstance Win32_Process -Filter "ProcessId=%d").ParentProcessId`, pid)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		output, err := cmd.Output()
		if err != nil {
			return true
		}
		ppidStr := strings.TrimSpace(string(output))
		ppid, err := strconv.Atoi(ppidStr)
		if err != nil {
			return true
		}
		// Check if parent process still exists
		checkCmd := exec.Command("powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`Get-Process -Id %d -ErrorAction SilentlyContinue`, ppid))
		if err := checkCmd.Run(); err != nil {
			// Parent doesn't exist, process is orphaned
			return true
		}
		return false

	default:
		return false
	}
}
