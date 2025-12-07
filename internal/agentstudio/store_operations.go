package agentstudio

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MaxFileSize is the maximum allowed file size for writes (10MB)
	MaxFileSize = 10 * 1024 * 1024
)

// ValidateBaseDir validates that the base directory is in the allowed list
func ValidateBaseDir(baseDir string, allowedDirs []string) error {
	// Normalize the base directory
	baseDir = filepath.Clean(baseDir)

	// Ensure it ends with a separator for prefix matching
	if !strings.HasSuffix(baseDir, string(filepath.Separator)) {
		baseDir += string(filepath.Separator)
	}

	// Check if it matches any allowed prefix
	for _, allowed := range allowedDirs {
		// Normalize allowed directory
		allowed = filepath.Clean(allowed)
		if !strings.HasSuffix(allowed, string(filepath.Separator)) {
			allowed += string(filepath.Separator)
		}

		if strings.HasPrefix(baseDir, allowed) {
			return nil
		}
	}

	return fmt.Errorf("base directory '%s' not in allowed list: %v", baseDir, allowedDirs)
}

// ValidateFilePath validates that the file path doesn't contain directory traversal attempts
func ValidateFilePath(filePath string) error {
	// Reject paths that are absolute
	if filepath.IsAbs(filePath) {
		return fmt.Errorf("file path must be relative, not absolute: %s", filePath)
	}

	// Check for directory traversal attempts
	cleanPath := filepath.Clean(filePath)
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("file path contains directory traversal: %s", filePath)
	}

	// Ensure cleaned path doesn't escape current directory
	if strings.HasPrefix(cleanPath, "..") || strings.HasPrefix(cleanPath, string(filepath.Separator)) {
		return fmt.Errorf("file path attempts to escape base directory: %s", filePath)
	}

	return nil
}

// BuildFinalPath constructs and validates the final file path
func BuildFinalPath(baseDir, filePath string) (string, error) {
	// Clean both paths
	baseDir = filepath.Clean(baseDir)
	filePath = filepath.Clean(filePath)

	// Build the final path
	finalPath := filepath.Join(baseDir, filePath)

	// Get absolute paths for comparison
	absBase, err := filepath.Abs(baseDir)
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
		var obj interface{}
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}
		formatted, err := json.MarshalIndent(obj, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to format JSON: %w", err)
		}
		return formatted, nil

	case "text", "markdown":
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

	// Check data size
	if len(formattedData) > MaxFileSize {
		return fmt.Errorf("data size %d bytes exceeds maximum allowed size %d bytes", len(formattedData), MaxFileSize)
	}

	// Create parent directories if enabled
	if node.AutoCreateDir {
		if err := createDirectories(finalPath); err != nil {
			return err
		}
	}

	// Write based on mode
	var writeErr error
	switch node.WriteMode {
	case "overwrite":
		// Atomic write using temp file + rename
		tempPath := finalPath + ".tmp"
		if err := os.WriteFile(tempPath, formattedData, 0644); err != nil {
			return fmt.Errorf("failed to write temp file: %w", err)
		}
		if err := os.Rename(tempPath, finalPath); err != nil {
			os.Remove(tempPath) // Clean up temp file
			return fmt.Errorf("failed to rename temp file: %w", err)
		}

	case "append":
		// Open file in append mode, create if doesn't exist
		f, err := os.OpenFile(finalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open file for append: %w", err)
		}
		defer f.Close()

		// Add newline before data for append mode
		if _, err := f.Write([]byte("\n")); err != nil {
			return fmt.Errorf("failed to write newline: %w", err)
		}
		if _, err := f.Write(formattedData); err != nil {
			return fmt.Errorf("failed to append data: %w", err)
		}

	default:
		return fmt.Errorf("unsupported write mode: %s", node.WriteMode)
	}

	if writeErr != nil {
		return writeErr
	}

	// Update node statistics
	node.LastWriteTime = time.Now()
	node.WriteCount++
	node.LastFilePath = filePath
	node.LastError = "" // Clear any previous error
	node.UpdatedAt = time.Now()

	return nil
}
