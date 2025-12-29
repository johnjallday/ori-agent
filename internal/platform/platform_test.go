package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ===== Path Tests =====

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"simple", "foo/bar", filepath.Join("foo", "bar")},
		{"backslashes", "foo\\bar\\baz", filepath.Join("foo", "bar", "baz")},
		{"mixed", "foo/bar\\baz", filepath.Join("foo", "bar", "baz")},
		{"dot", "./foo", "foo"},
		{"dotdot", "foo/../bar", "bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizePath(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestToUnixPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"foo/bar", "foo/bar"},
		{"foo\\bar", "foo/bar"},
		{"foo\\bar\\baz", "foo/bar/baz"},
		{"C:\\Users\\test", "C:/Users/test"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ToUnixPath(tt.input)
			if result != tt.expected {
				t.Errorf("ToUnixPath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetExtension(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"file.txt", ".txt"},
		{"file.tar.gz", ".gz"},
		{"file", ""},
		{".gitignore", ".gitignore"}, // Go's filepath.Ext considers entire name as extension
		{"path/to/file.jpg", ".jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := GetExtension(tt.input)
			if result != tt.expected {
				t.Errorf("GetExtension(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetBaseName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"file.txt", "file.txt"},
		{"path/to/file.txt", "file.txt"},
		{"/absolute/path/file.txt", "file.txt"},
		{"", "."},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := GetBaseName(tt.input)
			if result != tt.expected {
				t.Errorf("GetBaseName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetBaseNameWithoutExt(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"file.txt", "file"},
		{"file.tar.gz", "file.tar"},
		{"file", "file"},
		{"path/to/file.jpg", "file"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := GetBaseNameWithoutExt(tt.input)
			if result != tt.expected {
				t.Errorf("GetBaseNameWithoutExt(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsAbsolutePath(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"relative/path", false},
		{"./relative", false},
		{"../parent", false},
	}

	// Add platform-specific tests
	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			input    string
			expected bool
		}{"C:\\Windows", true})
	} else {
		tests = append(tests, struct {
			input    string
			expected bool
		}{"/absolute/path", true})
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := IsAbsolutePath(tt.input)
			if result != tt.expected {
				t.Errorf("IsAbsolutePath(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot get home directory")
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"~", home},
		{"~/Documents", filepath.Join(home, "Documents")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ExpandHome(tt.input)
			if err != nil {
				t.Errorf("ExpandHome(%q) error = %v", tt.input, err)
				return
			}
			if result != tt.expected {
				t.Errorf("ExpandHome(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsSubPath(t *testing.T) {
	tests := []struct {
		parent   string
		child    string
		expected bool
	}{
		{"/home/user", "/home/user/file.txt", true},
		{"/home/user", "/home/user/subdir/file.txt", true},
		{"/home/user", "/home/other/file.txt", false},
		{"/home/user", "/home/user", false}, // Same path, not a sub path
	}

	for _, tt := range tests {
		t.Run(tt.child, func(t *testing.T) {
			result, err := IsSubPath(tt.parent, tt.child)
			if err != nil {
				t.Errorf("IsSubPath error: %v", err)
				return
			}
			if result != tt.expected {
				t.Errorf("IsSubPath(%q, %q) = %v, want %v", tt.parent, tt.child, result, tt.expected)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal.txt", "normal.txt"},
		{"file<>:\"/\\|?*.txt", "file_________.txt"},
		{"  spaces  ", "spaces"},
		{"..dots..", "dots"},
		{"CON", "_CON"},
		{"PRN.txt", "_PRN.txt"},
		{"", "unnamed"},
		{"file\x00name", "file_name"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := SanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPathExists(t *testing.T) {
	// Test with temp directory (should exist)
	tmpDir := os.TempDir()
	if !PathExists(tmpDir) {
		t.Errorf("PathExists(%q) = false, want true", tmpDir)
	}

	// Test with non-existent path
	nonExistent := "/this/path/definitely/does/not/exist/12345"
	if PathExists(nonExistent) {
		t.Errorf("PathExists(%q) = true, want false", nonExistent)
	}
}

func TestIsDirectory(t *testing.T) {
	tmpDir := os.TempDir()
	if !IsDirectory(tmpDir) {
		t.Errorf("IsDirectory(%q) = false, want true", tmpDir)
	}
}

func TestPlatformInfo(t *testing.T) {
	info := GetPlatformInfo()

	if info.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", info.OS, runtime.GOOS)
	}

	if info.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", info.Arch, runtime.GOARCH)
	}

	if info.Separator != string(filepath.Separator) {
		t.Errorf("Separator = %q, want %q", info.Separator, string(filepath.Separator))
	}
}

func TestPlatformDetection(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		if !IsMacOS() {
			t.Error("IsMacOS() = false on macOS")
		}
	case "windows":
		if !IsWindows() {
			t.Error("IsWindows() = false on Windows")
		}
	case "linux":
		if !IsLinux() {
			t.Error("IsLinux() = false on Linux")
		}
	}
}

// ===== Symlink Tests =====

func TestCreateSymlink(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "symlink-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file
	srcPath := filepath.Join(tmpDir, "source.txt")
	if err := os.WriteFile(srcPath, []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, "link.txt")

	result := CreateSymlink(srcPath, linkPath, true)

	if !result.Success {
		t.Errorf("CreateSymlink failed: %v", result.Error)
		return
	}

	// Verify the link/copy exists
	if !PathExists(linkPath) {
		t.Error("Link path does not exist after CreateSymlink")
	}

	// Read content through link
	content, err := os.ReadFile(linkPath)
	if err != nil {
		t.Errorf("Failed to read through link: %v", err)
		return
	}

	if string(content) != "test content" {
		t.Errorf("Content = %q, want %q", string(content), "test content")
	}
}

func TestCreateSymlink_EmptyPaths(t *testing.T) {
	result := CreateSymlink("", "/some/path", false)
	if result.Success {
		t.Error("Expected failure with empty target path")
	}

	result = CreateSymlink("/some/path", "", false)
	if result.Success {
		t.Error("Expected failure with empty link path")
	}
}

func TestCreateSymlink_NonExistentTarget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "symlink-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	result := CreateSymlink("/nonexistent/file.txt", filepath.Join(tmpDir, "link.txt"), false)
	if result.Success {
		t.Error("Expected failure with non-existent target")
	}
}

func TestIsSymlink(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "symlink-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create regular file
	regularPath := filepath.Join(tmpDir, "regular.txt")
	if err := os.WriteFile(regularPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	isLink, err := IsSymlink(regularPath)
	if err != nil {
		t.Errorf("IsSymlink error: %v", err)
	}
	if isLink {
		t.Error("IsSymlink returned true for regular file")
	}

	// Create symlink (if supported)
	linkPath := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(regularPath, linkPath); err != nil {
		t.Skip("Symlinks not supported")
	}

	isLink, err = IsSymlink(linkPath)
	if err != nil {
		t.Errorf("IsSymlink error: %v", err)
	}
	if !isLink {
		t.Error("IsSymlink returned false for symlink")
	}
}

func TestIsSymlinkBroken(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "symlink-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file
	srcPath := filepath.Join(tmpDir, "source.txt")
	if err := os.WriteFile(srcPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create symlink
	linkPath := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(srcPath, linkPath); err != nil {
		t.Skip("Symlinks not supported")
	}

	// Link should not be broken
	broken, err := IsSymlinkBroken(linkPath)
	if err != nil {
		t.Errorf("IsSymlinkBroken error: %v", err)
	}
	if broken {
		t.Error("IsSymlinkBroken returned true for valid link")
	}

	// Delete source file
	os.Remove(srcPath)

	// Link should now be broken
	broken, err = IsSymlinkBroken(linkPath)
	if err != nil {
		t.Errorf("IsSymlinkBroken error: %v", err)
	}
	if !broken {
		t.Error("IsSymlinkBroken returned false for broken link")
	}
}

func TestSupportsSymlinks(t *testing.T) {
	// This just tests the function runs without error
	_ = SupportsSymlinks()
}

// ===== Open Tests =====

func TestOpenFolder_EmptyPath(t *testing.T) {
	err := OpenFolder("")
	if err == nil {
		t.Error("Expected error with empty path")
	}
}

func TestOpenFile_EmptyPath(t *testing.T) {
	err := OpenFile("")
	if err == nil {
		t.Error("Expected error with empty path")
	}
}

func TestOpenURL_EmptyPath(t *testing.T) {
	err := OpenURL("")
	if err == nil {
		t.Error("Expected error with empty URL")
	}
}

func TestRevealInFileManager_EmptyPath(t *testing.T) {
	err := RevealInFileManager("")
	if err == nil {
		t.Error("Expected error with empty path")
	}
}

func TestGetParentDir(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/home/user/file.txt", "/home/user"},
		{"/home/user/", "/home/user"},
		{"/file.txt", "/"},
		{"file.txt", "."},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := getParentDir(tt.input)
			if result != tt.expected {
				t.Errorf("getParentDir(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
