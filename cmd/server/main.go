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

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/server"
	"github.com/johnjallday/ori-agent/internal/version"
)

func main() {
	// Define command-line flags
	port := flag.Int("port", 8765, "Port to run server on")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	noBrowser := flag.Bool("no-browser", false, "Don't open browser on startup")
	allowNetwork := flag.Bool("allow-network", false, "Allow connections from network (default: localhost only)")
	versionOverride := flag.String("version", "", "Override version for testing (e.g., v0.0.24)")
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

	// Kill any existing process on the port before starting
	cleanupPort(*port)

	// Kill orphaned plugin processes
	cleanupOrphanedPlugins()

	// Create server with all dependencies
	srv, err := server.New()
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("HTTP server shutdown error", logger.Fields{"error": err.Error()})
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

// cleanupPort kills any process currently using the specified port
func cleanupPort(port int) {
	pidSet := make(map[int]struct{}) // Use map for deduplication

	switch runtime.GOOS {
	case "darwin", "linux":
		// Use lsof to find processes on the port
		cmd := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port))
		output, err := cmd.Output()
		if err != nil {
			// No process found on port, or lsof failed - either way, continue
			logger.Debug("No existing process found on port", logger.Fields{"port": port})
			return
		}
		// Parse PIDs from output (one per line)
		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			pidStr := strings.TrimSpace(scanner.Text())
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				pidSet[pid] = struct{}{}
			}
		}

	case "windows":
		// Use PowerShell (netstat parsing) to find process on port
		psCmd := fmt.Sprintf(`Get-NetTCPConnection -LocalPort %d -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess`, port)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		output, err := cmd.Output()
		if err != nil {
			logger.Debug("No existing process found on port", logger.Fields{"port": port})
			return
		}
		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			pidStr := strings.TrimSpace(scanner.Text())
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				pidSet[pid] = struct{}{}
			}
		}

	default:
		return
	}

	if len(pidSet) == 0 {
		return
	}

	// Convert to slice for logging
	var pids []string
	for pid := range pidSet {
		pids = append(pids, strconv.Itoa(pid))
	}

	logger.Info("Found existing process(es) on port, killing...", logger.Fields{
		"port": port,
		"pids": strings.Join(pids, ", "),
	})

	// Kill each process
	for pid := range pidSet {
		pidStr := strconv.Itoa(pid)
		switch runtime.GOOS {
		case "darwin", "linux":
			// Try graceful termination first (SIGTERM)
			termCmd := exec.Command("kill", "-15", pidStr)
			if err := termCmd.Run(); err == nil {
				// Wait briefly for graceful shutdown
				time.Sleep(200 * time.Millisecond)
				// Check if still running
				checkCmd := exec.Command("kill", "-0", pidStr)
				if checkCmd.Run() == nil {
					// Process still running, force kill
					forceCmd := exec.Command("kill", "-9", pidStr)
					_ = forceCmd.Run()
				}
			}
		case "windows":
			killCmd := exec.Command("taskkill", "/F", "/PID", pidStr)
			_ = killCmd.Run()
		}
		logger.Debug("Killed process", logger.Fields{"pid": pid})
	}

	// Give the OS a moment to release the port
	time.Sleep(100 * time.Millisecond)
}

// cleanupOrphanedPlugins kills any orphaned plugin processes from previous runs
func cleanupOrphanedPlugins() {
	pidSet := make(map[int]struct{}) // Use map for deduplication and validation

	// Get the current working directory to make matching more specific
	cwd, err := os.Getwd()
	if err != nil {
		logger.Debug("Could not get working directory for plugin cleanup", logger.Fields{"error": err.Error()})
		cwd = "" // Fall back to less specific matching
	}

	switch runtime.GOOS {
	case "darwin", "linux":
		// Use pgrep for cleaner process matching
		var pattern string
		if cwd != "" {
			// Match plugins in our specific directory
			pattern = fmt.Sprintf("%s/(uploaded_plugins|example_plugins|plugin_cache)/", cwd)
		} else {
			// Fallback to generic plugin directory matching
			pattern = "(uploaded_plugins|example_plugins|plugin_cache)/"
		}
		cmd := exec.Command("pgrep", "-f", pattern)
		output, err := cmd.Output()
		if err != nil {
			// pgrep returns exit code 1 when no processes found
			logger.Debug("No orphaned plugin processes found", nil)
			return
		}
		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			pidStr := strings.TrimSpace(scanner.Text())
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				pidSet[pid] = struct{}{}
			}
		}

	case "windows":
		// Use PowerShell for Windows compatibility
		var pattern string
		if cwd != "" {
			// Escape backslashes for PowerShell regex
			escapedCwd := strings.ReplaceAll(cwd, `\`, `\\`)
			pattern = fmt.Sprintf(`%s\\(uploaded_plugins|example_plugins|plugin_cache)\\`, escapedCwd)
		} else {
			pattern = `(uploaded_plugins|example_plugins|plugin_cache)\\`
		}
		psCmd := fmt.Sprintf(`Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -match '%s' } | Select-Object -ExpandProperty ProcessId`, pattern)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
		output, err := cmd.Output()
		if err != nil {
			logger.Debug("No orphaned plugin processes found", nil)
			return
		}
		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			pidStr := strings.TrimSpace(scanner.Text())
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				pidSet[pid] = struct{}{}
			}
		}

	default:
		return
	}

	if len(pidSet) == 0 {
		return
	}

	logger.Info("Found orphaned plugin process(es), killing...", logger.Fields{
		"count": len(pidSet),
	})

	// Kill each process
	for pid := range pidSet {
		pidStr := strconv.Itoa(pid)
		var killCmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin", "linux":
			killCmd = exec.Command("kill", pidStr)
		case "windows":
			killCmd = exec.Command("taskkill", "/F", "/PID", pidStr)
		}
		if killCmd != nil {
			_ = killCmd.Run() // Ignore errors - process may have already exited
		}
	}
}
