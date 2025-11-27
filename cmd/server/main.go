package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/johnjallday/ori-agent/internal/server"
)

func main() {
	// Define command-line flags
	port := flag.Int("port", 8765, "Port to run server on")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	noBrowser := flag.Bool("no-browser", false, "Don't open browser on startup")
	flag.Parse()

	// Set verbose mode globally
	os.Setenv("ORI_VERBOSE", fmt.Sprintf("%t", *verbose))

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

	// Create server with all dependencies
	srv, err := server.New()
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	// Start HTTP server with configured port
	addr := fmt.Sprintf(":%d", *port)
	url := fmt.Sprintf("http://localhost:%d", *port)
	log.Printf("Listening on %s", url)

	// Launch browser in background after a short delay (unless disabled)
	// Skip if --no-browser flag is set or NO_BROWSER env var is set
	if !*noBrowser && os.Getenv("NO_BROWSER") == "" {
		go func() {
			time.Sleep(500 * time.Millisecond) // Wait for server to start
			if err := openBrowser(url); err != nil {
				log.Printf("Could not auto-open browser: %v", err)
				log.Printf("Please open your browser manually and navigate to: %s", url)
			} else {
				log.Printf("Browser opened at: %s", url)
			}
		}()
	} else {
		log.Printf("Auto-open browser disabled. Navigate to: %s", url)
	}

	log.Fatal(srv.HTTPServer(addr).ListenAndServe())
}

// ensureDataDirectory checks if runtime data files exist in current directory.
// If they don't exist and we're running as a standalone binary, create an ori-agent folder.
func ensureDataDirectory() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// Check if we're already in an ori-agent directory or if data files exist
	baseName := filepath.Base(cwd)
	hasDataFiles := fileExists("agents.json") ||
		fileExists("local_plugin_registry.json") ||
		fileExists("plugin_cache") ||
		fileExists("uploaded_plugins")

	// If already in ori-agent directory or data files exist, we're good
	if baseName == "ori-agent" || hasDataFiles {
		return nil
	}

	// Create ori-agent directory and change into it
	dataDir := filepath.Join(cwd, "ori-agent")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	log.Printf("Created data directory: %s", dataDir)

	// Change working directory to the data directory
	if err := os.Chdir(dataDir); err != nil {
		return err
	}

	log.Printf("Working directory: %s", dataDir)
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
