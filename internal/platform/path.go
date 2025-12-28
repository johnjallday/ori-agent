package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// NormalizePath normalizes a file path for the current platform.
// It converts separators, resolves . and .., and cleans up the path.
func NormalizePath(path string) string {
	if path == "" {
		return ""
	}

	// Convert to forward slashes first for consistency
	path = strings.ReplaceAll(path, "\\", "/")

	// Clean the path (resolves . and ..)
	path = filepath.Clean(path)

	// Convert to native separators
	return filepath.FromSlash(path)
}

// ToUnixPath converts a path to Unix-style (forward slashes)
func ToUnixPath(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

// ToNativePath converts a path to the native separator for the current OS
func ToNativePath(path string) string {
	return filepath.FromSlash(ToUnixPath(path))
}

// JoinPath joins path elements using the native separator
func JoinPath(elem ...string) string {
	return filepath.Join(elem...)
}

// SplitPath splits a path into directory and file components
func SplitPath(path string) (dir, file string) {
	return filepath.Split(path)
}

// GetExtension returns the file extension including the dot
func GetExtension(path string) string {
	return filepath.Ext(path)
}

// GetBaseName returns the file name without the directory path
func GetBaseName(path string) string {
	return filepath.Base(path)
}

// GetBaseNameWithoutExt returns the file name without directory or extension
func GetBaseNameWithoutExt(path string) string {
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext)
}

// GetParentDir returns the parent directory of a path
func GetParentDir(path string) string {
	return filepath.Dir(path)
}

// IsAbsolutePath checks if a path is absolute
func IsAbsolutePath(path string) bool {
	return filepath.IsAbs(path)
}

// ToAbsolutePath converts a path to absolute, resolving relative paths
func ToAbsolutePath(path string) (string, error) {
	return filepath.Abs(path)
}

// ExpandHome expands ~ to the user's home directory
func ExpandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if path == "~" {
		return home, nil
	}

	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		return filepath.Join(home, path[2:]), nil
	}

	return path, nil
}

// IsSubPath checks if child is a subdirectory or file within parent
func IsSubPath(parent, child string) (bool, error) {
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return false, err
	}

	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false, err
	}

	// Normalize paths
	parentAbs = NormalizePath(parentAbs)
	childAbs = NormalizePath(childAbs)

	// Ensure parent ends with separator for proper prefix matching
	if !strings.HasSuffix(parentAbs, string(filepath.Separator)) {
		parentAbs += string(filepath.Separator)
	}

	return strings.HasPrefix(childAbs, parentAbs), nil
}

// PathExists checks if a path exists (file or directory)
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDirectory checks if a path is a directory
func IsDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// IsFile checks if a path is a regular file
func IsFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// EnsureDirectory creates a directory if it doesn't exist
func EnsureDirectory(path string) error {
	return os.MkdirAll(path, 0755)
}

// GetTempDir returns the system's temporary directory
func GetTempDir() string {
	return os.TempDir()
}

// GetHomeDir returns the user's home directory
func GetHomeDir() (string, error) {
	return os.UserHomeDir()
}

// GetWorkingDir returns the current working directory
func GetWorkingDir() (string, error) {
	return os.Getwd()
}

// SanitizeFilename removes or replaces characters that are invalid in filenames
func SanitizeFilename(name string) string {
	// Characters that are invalid on Windows (and some on other platforms)
	invalidChars := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*", "\x00"}

	result := name
	for _, char := range invalidChars {
		result = strings.ReplaceAll(result, char, "_")
	}

	// Remove leading/trailing spaces and dots (Windows issues)
	result = strings.Trim(result, " .")

	// Replace reserved Windows names
	reservedNames := []string{
		"CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
	}

	upper := strings.ToUpper(result)
	for _, reserved := range reservedNames {
		if upper == reserved || strings.HasPrefix(upper, reserved+".") {
			result = "_" + result
			break
		}
	}

	// Ensure name is not empty
	if result == "" {
		result = "unnamed"
	}

	return result
}

// GetPlatformInfo returns information about the current platform
func GetPlatformInfo() PlatformInfo {
	return PlatformInfo{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Separator: string(filepath.Separator),
	}
}

// PlatformInfo contains information about the current platform
type PlatformInfo struct {
	OS        string // "darwin", "windows", "linux", etc.
	Arch      string // "amd64", "arm64", etc.
	Separator string // Path separator ("/" or "\\")
}

// IsMacOS returns true if running on macOS
func IsMacOS() bool {
	return runtime.GOOS == "darwin"
}

// IsWindows returns true if running on Windows
func IsWindows() bool {
	return runtime.GOOS == "windows"
}

// IsLinux returns true if running on Linux
func IsLinux() bool {
	return runtime.GOOS == "linux"
}
