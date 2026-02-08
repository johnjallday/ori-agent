// Package platform provides cross-platform utilities for file system operations.
package platform

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// OpenFolder opens the specified folder in the native file manager.
// Returns an error if the folder doesn't exist or cannot be opened.
//
// Platform support:
//   - macOS: Uses "open" command
//   - Windows: Uses "explorer" command
//   - Linux: Uses "xdg-open" command
func OpenFolder(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	case "linux":
		cmd = exec.Command("xdg-open", path)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	// Run and don't wait for the process to complete
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open folder: %w", err)
	}

	// Don't wait for the command to finish - file manager stays open
	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

// OpenFile opens the specified file with the default application.
// Returns an error if the file doesn't exist or cannot be opened.
//
// Platform support:
//   - macOS: Uses "open" command
//   - Windows: Uses "start" command via cmd.exe
//   - Linux: Uses "xdg-open" command
func OpenFile(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		// Windows requires cmd /c start to open files
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "linux":
		cmd = exec.Command("xdg-open", path)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

// OpenURL opens the specified URL in the default web browser.
// Returns an error if the URL cannot be opened.
//
// Platform support:
//   - macOS: Uses "open" command
//   - Windows: Uses "start" command via cmd.exe
//   - Linux: Uses "xdg-open" command
func OpenURL(url string) error {
	if url == "" {
		return fmt.Errorf("URL cannot be empty")
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open URL: %w", err)
	}

	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

// OpenApplication launches a desktop application by name.
// It does not use shell interpolation, so appName is passed as a raw argument.
//
// Platform support:
//   - macOS: Uses "open -a <AppName>"
//   - Windows: Uses "start" via cmd.exe
//   - Linux: Tries "gtk-launch", then falls back to "xdg-open"
func OpenApplication(appName string) error {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return fmt.Errorf("application name cannot be empty")
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-a", appName)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", appName)
	case "linux":
		gtkCmd := exec.Command("gtk-launch", appName)
		if err := gtkCmd.Start(); err == nil {
			go func() { _ = gtkCmd.Wait() }()
			return nil
		}
		cmd = exec.Command("xdg-open", appName)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open application %q: %w", appName, err)
	}

	go func() { _ = cmd.Wait() }()
	return nil
}

// RevealInFileManager opens the file manager and selects the specified file.
// This is useful when you want to show the user where a file is located.
//
// Platform support:
//   - macOS: Uses "open -R" to reveal in Finder
//   - Windows: Uses "explorer /select," to reveal in Explorer
//   - Linux: Falls back to opening the parent folder
func RevealInFileManager(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	case "windows":
		cmd = exec.Command("explorer", "/select,", path)
	case "linux":
		// Linux doesn't have a standard way to select a file,
		// so we just open the parent directory
		parent := getParentDir(path)
		cmd = exec.Command("xdg-open", parent)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to reveal file: %w", err)
	}

	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

// getParentDir returns the parent directory of a path
func getParentDir(path string) string {
	// Simple implementation - find last separator
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			if i == 0 {
				return string(path[0])
			}
			return path[:i]
		}
	}
	return "."
}
