package workspace

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

const (
	// MaxFileSize is the maximum file size allowed for store writes (10MB)
	MaxFileSize = 10 * 1024 * 1024
)

// ValidateBaseDir validates that a base directory is safe (prevents directory traversal)
// Users can specify any absolute path they choose (e.g., Documents, project folders)
// For relative paths, they must start with allowed prefixes
func ValidateBaseDir(baseDir string, allowedRelativeDirs []string) error {
	if baseDir == "" {
		return fmt.Errorf("base directory cannot be empty")
	}

	// Clean the path to normalize it
	cleanPath := filepath.Clean(baseDir)

	// Check for directory traversal patterns
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("base directory contains invalid path traversal sequence: %s", baseDir)
	}

	// Absolute paths are allowed - user's choice where to store files
	if filepath.IsAbs(cleanPath) {
		return nil
	}

	// For relative paths, ensure they start with allowed prefixes
	allowed := false
	for _, allowedDir := range allowedRelativeDirs {
		allowedClean := filepath.Clean(allowedDir)
		if !filepath.IsAbs(allowedClean) && strings.HasPrefix(cleanPath, allowedClean) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("relative path %s must start with one of: %v", cleanPath, allowedRelativeDirs)
	}

	return nil
}

// ValidateFilePath ensures the agent-provided file path is safe
func ValidateFilePath(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Reject absolute paths
	if filepath.IsAbs(filePath) {
		return fmt.Errorf("file path must be relative, got absolute path: %s", filePath)
	}

	// Clean the path
	cleanPath := filepath.Clean(filePath)

	// Check for directory traversal
	if strings.Contains(cleanPath, "..") || strings.HasPrefix(cleanPath, "../") {
		return fmt.Errorf("file path contains invalid directory traversal: %s", filePath)
	}

	// Additional check: ensure cleaned path doesn't start with /
	if strings.HasPrefix(cleanPath, "/") {
		return fmt.Errorf("file path must not start with /: %s", cleanPath)
	}

	return nil
}

// BuildFinalPath safely joins base directory and file path, then validates the result
func BuildFinalPath(baseDir, filePath string) (string, error) {
	// Validate inputs first
	if baseDir == "" {
		return "", fmt.Errorf("base directory is empty")
	}
	if filePath == "" {
		return "", fmt.Errorf("file path is empty")
	}

	// Clean both paths
	cleanBase := filepath.Clean(baseDir)
	cleanFile := filepath.Clean(filePath)

	// Join paths
	finalPath := filepath.Join(cleanBase, cleanFile)

	// Critical security check: ensure final path is still within base directory
	// Get absolute paths for comparison
	absBase, err := filepath.Abs(cleanBase)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}

	absFinal, err := filepath.Abs(finalPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve final path: %w", err)
	}

	// Ensure final path starts with base directory
	if !strings.HasPrefix(absFinal, absBase) {
		return "", fmt.Errorf("file path escapes base directory: %s", filePath)
	}

	return finalPath, nil
}

// validateJSONData validates that the data is valid JSON
func validateJSONData(data string) error {
	var js json.RawMessage
	if err := json.Unmarshal([]byte(data), &js); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

// formatData converts data to bytes based on the specified format
func formatData(data string, format string) ([]byte, error) {
	switch format {
	case "json":
		// Validate JSON first
		if err := validateJSONData(data); err != nil {
			return nil, err
		}
		// Pretty-print JSON with 2-space indentation
		var obj any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}
		formatted, err := json.MarshalIndent(obj, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to format JSON: %w", err)
		}
		return formatted, nil

	case "text", "markdown", "csv":
		// Return as-is, preserve newlines
		return []byte(data), nil

	case "binary":
		// Decode base64-encoded data
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 data: %w", err)
		}
		return decoded, nil

	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

// createDirectories creates parent directories for the given path
func createDirectories(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}
	return nil
}

// WriteToStore writes data to a store node with the specified file path
func WriteToStore(node *StoreNode, filePath, data string) error {
	// Validate file path
	if err := ValidateFilePath(filePath); err != nil {
		return fmt.Errorf("invalid file path: %w", err)
	}

	// Build final path
	finalPath, err := BuildFinalPath(node.BaseDir, filePath)
	if err != nil {
		return fmt.Errorf("path validation failed: %w", err)
	}

	// Format data according to node's format setting
	formattedData, err := formatData(data, node.Format)
	if err != nil {
		return fmt.Errorf("data formatting failed: %w", err)
	}

	// Check file size limit
	if len(formattedData) > MaxFileSize {
		return fmt.Errorf("data size (%d bytes) exceeds maximum allowed size (%d bytes)", len(formattedData), MaxFileSize)
	}

	// Create directories if auto-create is enabled
	if node.AutoCreateDir {
		if err := createDirectories(finalPath); err != nil {
			return err
		}
	}

	// Write based on mode
	if node.WriteMode == "append" {
		// Append mode: open file in append mode, create if doesn't exist
		f, err := os.OpenFile(finalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open file for append: %w", err)
		}
		defer func() { _ = f.Close() }()

		if info, err := f.Stat(); err == nil && info.Size() > 0 {
			// Add newline separator before appending
			if _, err := f.Write([]byte("\n")); err != nil {
				return fmt.Errorf("failed to write newline separator: %w", err)
			}
		}
		if _, err := f.Write(formattedData); err != nil {
			return fmt.Errorf("failed to append data: %w", err)
		}
	} else {
		// Overwrite mode: atomic write using temp file
		tempPath := finalPath + ".tmp"
		if err := os.WriteFile(tempPath, formattedData, 0644); err != nil {
			return fmt.Errorf("failed to write temp file: %w", err)
		}
		if err := os.Rename(tempPath, finalPath); err != nil {
			_ = os.Remove(tempPath) // Clean up temp file on error
			return fmt.Errorf("failed to rename temp file: %w", err)
		}
	}

	// Update node stats
	node.LastWriteTime = time.Now()
	node.WriteCount++
	node.LastFilePath = filePath
	node.LastError = ""
	node.UpdatedAt = time.Now()

	logger.Debug("Store write successful", logger.Fields{
		"store_node_id": node.ID,
		"file_path":     filePath,
		"final_path":    finalPath,
		"size_bytes":    len(formattedData),
		"write_count":   node.WriteCount,
	})

	return nil
}
