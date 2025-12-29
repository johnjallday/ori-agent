package platform

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// SymlinkResult represents the result of a symlink creation attempt
type SymlinkResult struct {
	// Success indicates whether the operation succeeded
	Success bool
	// IsSymlink is true if a symlink was created, false if a copy was made
	IsSymlink bool
	// Path is the path to the created symlink or copy
	Path string
	// Error contains any error that occurred (nil on success)
	Error error
}

// CreateSymlink creates a symbolic link at linkPath pointing to targetPath.
// On Windows, if symlink creation fails due to permissions, it falls back to copying the file.
//
// Parameters:
//   - targetPath: The file or directory the symlink should point to (must exist)
//   - linkPath: Where to create the symlink
//   - fallbackToCopy: If true, copy the file on Windows when symlink fails
//
// Returns:
//   - SymlinkResult with details about what was created
func CreateSymlink(targetPath, linkPath string, fallbackToCopy bool) SymlinkResult {
	result := SymlinkResult{
		Path: linkPath,
	}

	// Validate inputs
	if targetPath == "" || linkPath == "" {
		result.Error = fmt.Errorf("target and link paths cannot be empty")
		return result
	}

	// Check if target exists
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		result.Error = fmt.Errorf("target does not exist: %w", err)
		return result
	}

	// Ensure parent directory of link exists
	linkDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		result.Error = fmt.Errorf("failed to create link directory: %w", err)
		return result
	}

	// Remove existing file/link at linkPath if it exists
	if _, err := os.Lstat(linkPath); err == nil {
		if err := os.Remove(linkPath); err != nil {
			result.Error = fmt.Errorf("failed to remove existing file at link path: %w", err)
			return result
		}
	}

	// Try to create symlink
	err = os.Symlink(targetPath, linkPath)
	if err == nil {
		result.Success = true
		result.IsSymlink = true
		return result
	}

	// Symlink failed - check if we should fallback to copy
	if !fallbackToCopy {
		result.Error = fmt.Errorf("failed to create symlink: %w", err)
		return result
	}

	// On Windows, symlinks often fail due to permissions
	// Fall back to copying the file
	if runtime.GOOS == "windows" || fallbackToCopy {
		if targetInfo.IsDir() {
			result.Error = fmt.Errorf("cannot copy directory, symlink failed: %w", err)
			return result
		}

		if copyErr := copyFile(targetPath, linkPath); copyErr != nil {
			result.Error = fmt.Errorf("symlink failed and copy fallback also failed: symlink error: %v, copy error: %w", err, copyErr)
			return result
		}

		result.Success = true
		result.IsSymlink = false
		return result
	}

	result.Error = fmt.Errorf("failed to create symlink: %w", err)
	return result
}

// IsSymlink checks if the given path is a symbolic link
func IsSymlink(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}

// ReadSymlink returns the target path of a symbolic link
func ReadSymlink(path string) (string, error) {
	return os.Readlink(path)
}

// IsSymlinkBroken checks if a symlink points to a non-existent target
func IsSymlinkBroken(path string) (bool, error) {
	isLink, err := IsSymlink(path)
	if err != nil {
		return false, err
	}
	if !isLink {
		return false, nil
	}

	// Try to stat the target (follows symlink)
	_, err = os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}

	return false, nil
}

// ResolveSymlink resolves a symlink to its absolute target path
// Returns the original path if it's not a symlink
func ResolveSymlink(path string) (string, error) {
	isLink, err := IsSymlink(path)
	if err != nil {
		return "", err
	}

	if !isLink {
		return filepath.Abs(path)
	}

	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}

	// If target is relative, resolve it relative to the symlink's directory
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}

	return filepath.Abs(target)
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source: %w", err)
	}
	defer sourceFile.Close()

	// Get source file info for permissions
	sourceInfo, err := sourceFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source: %w", err)
	}

	destFile, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, sourceInfo.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return fmt.Errorf("failed to copy content: %w", err)
	}

	return nil
}

// SupportsSymlinks returns true if the current platform supports symlinks
// without requiring elevated permissions
func SupportsSymlinks() bool {
	switch runtime.GOOS {
	case "darwin", "linux", "freebsd", "openbsd", "netbsd":
		return true
	case "windows":
		// Windows requires Developer Mode or elevated permissions
		// We can't easily check this, so we return false and rely on fallback
		return false
	default:
		return false
	}
}
