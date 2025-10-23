package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/johnjallday/dolphin-agent/internal/server"
)

func main() {
	// Ensure we're running in a proper data directory
	if err := ensureDataDirectory(); err != nil {
		log.Fatalf("Failed to setup data directory: %v", err)
	}

	// Create server with all dependencies
	srv, err := server.New()
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	// Start HTTP server
	addr := ":8080"
	url := "http://localhost" + addr
	log.Printf("Listening on %s", url)

	// Launch browser in background after a short delay
	go func() {
		time.Sleep(500 * time.Millisecond) // Wait for server to start
		if err := openBrowser(url); err != nil {
			log.Printf("Failed to open browser: %v", err)
		}
	}()

	log.Fatal(srv.HTTPServer(addr).ListenAndServe())
}

// ensureDataDirectory checks if runtime data files exist in current directory.
// If they don't exist and we're running as a standalone binary, create a dolphin-agent folder.
func ensureDataDirectory() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// Check if we're already in a dolphin-agent directory or if data files exist
	baseName := filepath.Base(cwd)
	hasDataFiles := fileExists("agents.json") ||
		fileExists("local_plugin_registry.json") ||
		fileExists("plugin_cache") ||
		fileExists("uploaded_plugins")

	// If already in dolphin-agent directory or data files exist, we're good
	if baseName == "dolphin-agent" || hasDataFiles {
		return nil
	}

	// Create dolphin-agent directory and change into it
	dataDir := filepath.Join(cwd, "dolphin-agent")
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

	return cmd.Start()
}
