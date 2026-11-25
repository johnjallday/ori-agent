package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/environ"
	"github.com/johnjallday/ori-agent/internal/fileparser"
	"github.com/johnjallday/ori-agent/internal/gateway/channels/console"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	portutil "github.com/johnjallday/ori-agent/internal/port"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/server"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/version"
)

// Private construction seams let owned-process startup tests fail safely before
// any port inspection or native/provider initialization if ordering regresses.
var startupPortCheck = ensurePortAvailable
var startupBeforeStores = settingsreset.BeforeStores
var startupServer = server.NewWithResetLease

func main() {
	// Private metadata children must never initialize dotenv, application
	// persistence, native secret discovery, HTTP ports or browser launching.
	if handled, code := fileparser.RunReviewPDFWorker(os.Args[1:]); handled {
		os.Exit(code)
	}
	if handled, code := backuprecovery.RunHelperProcess(os.Args[1:]); handled {
		os.Exit(code)
	}

	supervisor := resetSupervisorFromEnvironment()
	if supervisor == nil {
		// Resolve launch environment once, before entering the canonical data
		// directory. Managed children must not read a different .env there.
		environ.LoadDotEnv(".")
		environ.ExpandPath()
	}

	// Define command-line flags
	port := flag.Int("port", 8765, "Port to run server on")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	noBrowser := flag.Bool("no-browser", false, "Don't open browser on startup")
	allowNetwork := flag.Bool("allow-network", false, "Allow connections from network (default: localhost only)")
	consoleEnabled := flag.Bool("console", false, "Enable console interaction channel")
	versionOverride := flag.String("version", "", "Override version for testing (e.g., v0.0.24)")
	multiAgentMode := flag.String("multi-agent-mode", "", "Multi-agent default mode: auto, force, off")
	multiAgentThreshold := flag.Float64("multi-agent-threshold", 0, "Multi-agent complexity threshold (0-10)")
	managedRestart := flag.Bool("managed-restart", false, "Run under a local supervisor that can relaunch Ori after a reviewed reset")
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

	if (*managedRestart || supervisor != nil) && *allowNetwork {
		log.Fatal("Managed reset restart is only available on the local listener.")
	}
	if *managedRestart && supervisor == nil {
		if err := runResetSupervisor(*port); err != nil {
			log.Fatal(err)
		}
		return
	}

	addr := standaloneAddress(*port, *allowNetwork)
	listener, bindErr := reserveStartupListener(addr, supervisor != nil)
	if bindErr != nil {
		log.Fatalf("Cannot bind Ori server: %v. Choose a free port or stop only your own Ori process.", bindErr)
	}
	if listener != nil {
		defer func() { _ = listener.Close() }()
	}

	// Own the installation before any persistence constructor or port
	// takeover. The lease is pinned until process exit, not until Shutdown.
	var lease *resetstate.Lease
	var err error
	dataDir := os.Getenv("ORI_DATA_DIR")
	var expected string
	if supervisor != nil {
		expected = os.Getenv(expectedResetEnv)
	}
	switch {
	case expected != "" && os.Getenv(expectedStateEnv) == canceledHandoff:
		// A recovery host canceled this reset; the replacement must find that
		// exact operation durably canceled and applies nothing.
		lease, err = settingsreset.BeforeStoresAfterCancel(context.Background(), dataDir, expected)
	case expected != "":
		lease, err = settingsreset.BeforeStoresForRestart(context.Background(), dataDir, expected)
	default:
		lease, err = startupBeforeStores(context.Background(), dataDir)
	}
	if err != nil {
		if lease != nil && errors.Is(err, settingsreset.ErrRecoveryIncomplete) {
			if recoveryErr := runResetRecoveryServer(lease, *port, *noBrowser || expected != "", listener, supervisor); recoveryErr != nil {
				log.Fatalf("Reset recovery host failed: %v", recoveryErr)
			}
			return
		}
		log.Fatalf("Cannot open installation: %v", err)
	}

	// A managed restart or demo already owns its exact address.
	if listener == nil {
		if err := startupPortCheck(*port); err != nil {
			log.Fatalf("Port %d is unavailable: %v", *port, err)
		}
	}

	// Create server with all dependencies
	srv, err := startupServer(lease)
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	if *consoleEnabled {
		// Register console channel
		ctx := context.Background()
		c := console.NewConsoleChannel("console-prod", logger.New("console-channel"))
		if err := srv.Core.Gateway.RegisterChannel(ctx, c); err != nil {
			logger.Error("Failed to register console channel", logger.Fields{"error": err})
		}
	}

	if srv.Core != nil && srv.Core.ConfigManager != nil {
		if *multiAgentMode != "" || *multiAgentThreshold > 0 {
			srv.Core.ConfigManager.SetMultiAgentDefaults(*multiAgentMode, *multiAgentThreshold)
		}
	}

	if *allowNetwork {
		logger.Warn("Server bound to all interfaces - accessible from network", logger.Fields{"port": *port})
	}
	restart := make(chan string, 1)
	if supervisor != nil {
		srv.Handlers.Reset.SetRestartHandoff(settingsreset.NewRestartHandoff(supervisor.ready, func(id string) {
			restart <- id // At most one enqueue; never blocks the confirmation response.
		}))
	}
	url := fmt.Sprintf("http://localhost:%d", *port)
	logger.Debug("Listening on", logger.Fields{"url": url})

	// Launch browser in background after a short delay (unless disabled)
	// Skip if --no-browser flag is set or NO_BROWSER env var is set
	if expected == "" && !*noBrowser && os.Getenv("NO_BROWSER") == "" {
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
		if err := serveStandalone(httpServer, listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	}()

	var restartID string
	select {
	case <-quit:
	case restartID = <-restart:
		// Normal response delivery must not wait for this timer; it gives a
		// same-origin tab time to load the lightweight status page.
		select {
		case <-time.After(restartReplyGrace):
		case <-quit:
			restartID = "" // Explicit shutdown takes precedence.
		}
	}
	logger.Info("Shutting down server...", nil)

	// Clean up plugins and background services
	srv.Shutdown()

	// Graceful HTTP shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		// Context deadline exceeded is expected when SSE/streaming connections are active
		// The server still stops, connections are just closed forcefully
		if restartID != "" {
			log.Fatalf("Could not finish managed restart: %v. Relaunch Ori manually.", err)
		}
		if err == context.DeadlineExceeded {
			logger.Warn("HTTP server shutdown timeout (active streaming connections were closed forcefully)", nil)
		} else {
			logger.Error("HTTP server shutdown error", logger.Fields{"error": err.Error()})
		}
	}

	logger.Info("Server stopped", nil)
	if restartID != "" {
		select {
		case <-quit:
			return
		default:
		}
		if err := supervisor.releaseOperation(restartID); err != nil {
			log.Fatalf("Managed restart handoff failed: %v. Relaunch Ori manually.", err)
		}
	}
}

func standaloneAddress(port int, allowNetwork bool) string {
	if allowNetwork {
		return fmt.Sprintf(":%d", port)
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func serveStandalone(server *http.Server, listener net.Listener) error {
	if listener != nil {
		return server.Serve(listener)
	}
	return server.ListenAndServe()
}

// runResetRecoveryServer serves the read-only recovery page. When this process
// is a managed child, supervisor lets a durably canceled reset reopen Ori once
// automatically; a manual launch has none, and the page shows exact relaunch
// steps instead. Nothing here starts the application against the installation:
// the replacement is a new process the supervisor starts after this one exits.
func runResetRecoveryServer(lease *resetstate.Lease, port int, noBrowser bool, listener net.Listener, supervisor *inheritedResetSupervisor) error {
	if listener == nil {
		if err := startupPortCheck(port); err != nil {
			return err
		}
	}
	address := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://localhost:%d/", port)
	var handoff *settingsreset.RestartHandoff
	canceled := make(chan string, 1)
	if supervisor != nil {
		handoff = settingsreset.NewRestartHandoff(supervisor.ready, func(id string) { canceled <- id })
	}
	httpServer := settingsreset.RecoveryHTTPServerWith(lease, address, handoff)
	serveErr := make(chan error, 1)
	go func() {
		err := serveStandalone(httpServer, listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()
	logger.Warn("Ori started in reset recovery mode; application stores remain closed", logger.Fields{"url": url})
	if !noBrowser && os.Getenv("NO_BROWSER") == "" {
		go func() {
			if err := openBrowser(url); err != nil {
				logger.Debug("Could not open reset recovery page", logger.Fields{"error": err})
			}
		}()
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	var canceledID string
	select {
	case err := <-serveErr:
		return err
	case <-quit:
	case canceledID = <-canceled:
		// The canceled receipt is already durable and acknowledged. Give the
		// tab time to read the reply, then stop serving before asking for the
		// one replacement; an explicit quit takes precedence.
		select {
		case <-time.After(restartReplyGrace):
		case <-quit:
			canceledID = ""
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		if canceledID != "" {
			return fmt.Errorf("could not finish managed restart: %w; relaunch Ori manually", err)
		}
		return err
	}
	if canceledID != "" {
		if err := supervisor.releaseCanceled(canceledID); err != nil {
			return fmt.Errorf("managed restart handoff failed: %w; relaunch Ori manually", err)
		}
	}
	return nil
}

// dataDirectoryInputs contains the process facts used to choose the one runtime
// data root. Keeping resolution pure makes the launch-mode precedence testable
// without reading or mutating a developer's real HOME.
type dataDirectoryInputs struct {
	configuredDir  string
	executablePath string
	goos           string
	homeDir        string
	workingDir     string
	hasDataFiles   bool
}

// ensureDataDirectory resolves and activates one canonical runtime root before
// server.New constructs SQLite, config, agent, vault, reset, or workspace
// stores. Publishing the absolute root through ORI_DATA_DIR is the ownership
// contract shared by packages that historically chose different CWD/HOME
// fallbacks.
func ensureDataDirectory() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve launch directory: %w", err)
	}

	executablePath, _ := os.Executable()
	homeDir, _ := os.UserHomeDir()
	dataDir, err := resolveDataDirectory(dataDirectoryInputs{
		configuredDir:  os.Getenv("ORI_DATA_DIR"),
		executablePath: executablePath,
		goos:           runtime.GOOS,
		homeDir:        homeDir,
		workingDir:     cwd,
		hasDataFiles:   hasRecognizedDataFiles(cwd),
	})
	if err != nil {
		return err
	}

	return activateDataDirectory(dataDir)
}

// resolveDataDirectory applies launch precedence without touching the file
// system: explicit override, installed macOS app, existing data directory, then
// a standalone launch-local ori-data directory.
func resolveDataDirectory(inputs dataDirectoryInputs) (string, error) {
	workingDir := strings.TrimSpace(inputs.workingDir)
	if workingDir == "" {
		return "", errors.New("working directory is required")
	}
	workingDir, err := filepath.Abs(workingDir)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %q: %w", inputs.workingDir, err)
	}

	if configuredDir := strings.TrimSpace(inputs.configuredDir); configuredDir != "" {
		if !filepath.IsAbs(configuredDir) {
			configuredDir = filepath.Join(workingDir, configuredDir)
		}
		return filepath.Clean(configuredDir), nil
	}

	if inputs.goos == "darwin" && strings.Contains(inputs.executablePath, ".app/Contents/MacOS") {
		homeDir := strings.TrimSpace(inputs.homeDir)
		if homeDir == "" {
			return "", errors.New("resolve installed app data directory: home directory is unavailable")
		}
		return filepath.Join(homeDir, "Library", "Application Support", "OriAgent"), nil
	}

	baseName := filepath.Base(workingDir)
	if baseName == "ori-data" || baseName == "OriAgent" || inputs.hasDataFiles {
		return workingDir, nil
	}

	return filepath.Join(workingDir, "ori-data"), nil
}

// activateDataDirectory creates and enters the selected root, then publishes
// its absolute path so every downstream package resolves the same profile.
func activateDataDirectory(dataDir string) error {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return errors.New("data directory is required")
	}
	absoluteDir, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("resolve data directory %q: %w", dataDir, err)
	}

	createdDataDir, err := onboarding.CreateInstallRoot(absoluteDir)
	if err != nil {
		return fmt.Errorf("prepare data directory: %w", err)
	}
	physicalDir, err := filepath.EvalSymlinks(absoluteDir)
	if err != nil {
		return fmt.Errorf("canonicalize data directory %q: %w", absoluteDir, err)
	}
	absoluteDir = physicalDir

	if err := os.MkdirAll(filepath.Join(absoluteDir, "vaults"), 0o750); err != nil {
		return fmt.Errorf("create vault directory: %w", err)
	}

	previousCWD, _ := os.Getwd()
	if err := os.Chdir(absoluteDir); err != nil {
		return fmt.Errorf("enter data directory %q: %w", absoluteDir, err)
	}
	if err := os.Setenv("ORI_DATA_DIR", absoluteDir); err != nil {
		if previousCWD != "" {
			_ = os.Chdir(previousCWD)
		}
		return fmt.Errorf("publish data directory: %w", err)
	}

	if createdDataDir {
		logger.Info("Created data directory", logger.Fields{"dataDir": absoluteDir})
	} else {
		logger.Info("Using existing data directory", logger.Fields{"dataDir": absoluteDir})
	}
	logger.Debug("Working directory", logger.Fields{"dataDir": absoluteDir})
	return nil
}

func hasRecognizedDataFiles(dir string) bool {
	for _, name := range []string{"agents.json", "settings.json", "sessions.db"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
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

// Reserve the exact listener before touching state. Neither reset recovery nor
// a disposable demo may inspect or evict an existing Ori process.
func reserveStartupListener(addr string, managed bool) (net.Listener, error) {
	if !managed && os.Getenv("ORI_DEMO_REQUIRE_FREE_PORT") != "1" {
		return nil, nil
	}
	return (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
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
