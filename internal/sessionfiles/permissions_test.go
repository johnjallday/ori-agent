package sessionfiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilePermission_IsValid(t *testing.T) {
	tests := []struct {
		perm  FilePermission
		valid bool
	}{
		{PermissionNone, true},
		{PermissionReadOnly, true},
		{PermissionReadWrite, true},
		{PermissionFull, true},
		{FilePermission("invalid"), false},
		{FilePermission(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.perm), func(t *testing.T) {
			if got := tt.perm.IsValid(); got != tt.valid {
				t.Errorf("IsValid() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestFilePermission_CanRead(t *testing.T) {
	tests := []struct {
		perm    FilePermission
		canRead bool
	}{
		{PermissionNone, false},
		{PermissionReadOnly, true},
		{PermissionReadWrite, true},
		{PermissionFull, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.perm), func(t *testing.T) {
			if got := tt.perm.CanRead(); got != tt.canRead {
				t.Errorf("CanRead() = %v, want %v", got, tt.canRead)
			}
		})
	}
}

func TestFilePermission_CanWrite(t *testing.T) {
	tests := []struct {
		perm     FilePermission
		canWrite bool
	}{
		{PermissionNone, false},
		{PermissionReadOnly, false},
		{PermissionReadWrite, true},
		{PermissionFull, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.perm), func(t *testing.T) {
			if got := tt.perm.CanWrite(); got != tt.canWrite {
				t.Errorf("CanWrite() = %v, want %v", got, tt.canWrite)
			}
		})
	}
}

func TestFilePermission_CanDelete(t *testing.T) {
	tests := []struct {
		perm      FilePermission
		canDelete bool
	}{
		{PermissionNone, false},
		{PermissionReadOnly, false},
		{PermissionReadWrite, false},
		{PermissionFull, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.perm), func(t *testing.T) {
			if got := tt.perm.CanDelete(); got != tt.canDelete {
				t.Errorf("CanDelete() = %v, want %v", got, tt.canDelete)
			}
		})
	}
}

func TestPermissionChecker_CanRead(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "perm-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name       string
		permission FilePermission
		filePath   string
		allowed    bool
	}{
		{"full can read", PermissionFull, filepath.Join(tmpDir, "test.txt"), true},
		{"readwrite can read", PermissionReadWrite, filepath.Join(tmpDir, "test.txt"), true},
		{"readonly can read", PermissionReadOnly, filepath.Join(tmpDir, "test.txt"), true},
		{"none cannot read", PermissionNone, filepath.Join(tmpDir, "test.txt"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := NewPermissionChecker(tmpDir, PermissionConfig{
				Permission: tt.permission,
			})

			allowed, _ := pc.CanRead(tt.filePath)
			if allowed != tt.allowed {
				t.Errorf("CanRead() = %v, want %v", allowed, tt.allowed)
			}
		})
	}
}

func TestPermissionChecker_CanWrite(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "perm-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name       string
		permission FilePermission
		filePath   string
		size       int64
		allowed    bool
	}{
		{"full can write", PermissionFull, filepath.Join(tmpDir, "test.txt"), 1024, true},
		{"readwrite can write", PermissionReadWrite, filepath.Join(tmpDir, "test.txt"), 1024, true},
		{"readonly cannot write", PermissionReadOnly, filepath.Join(tmpDir, "test.txt"), 1024, false},
		{"none cannot write", PermissionNone, filepath.Join(tmpDir, "test.txt"), 1024, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := NewPermissionChecker(tmpDir, PermissionConfig{
				Permission: tt.permission,
			})

			allowed, _ := pc.CanWrite(tt.filePath, tt.size)
			if allowed != tt.allowed {
				t.Errorf("CanWrite() = %v, want %v", allowed, tt.allowed)
			}
		})
	}
}

func TestPermissionChecker_CanDelete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "perm-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name       string
		permission FilePermission
		filePath   string
		allowed    bool
	}{
		{"full can delete", PermissionFull, filepath.Join(tmpDir, "test.txt"), true},
		{"readwrite cannot delete", PermissionReadWrite, filepath.Join(tmpDir, "test.txt"), false},
		{"readonly cannot delete", PermissionReadOnly, filepath.Join(tmpDir, "test.txt"), false},
		{"none cannot delete", PermissionNone, filepath.Join(tmpDir, "test.txt"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := NewPermissionChecker(tmpDir, PermissionConfig{
				Permission: tt.permission,
			})

			allowed, _ := pc.CanDelete(tt.filePath)
			if allowed != tt.allowed {
				t.Errorf("CanDelete() = %v, want %v", allowed, tt.allowed)
			}
		})
	}
}

func TestPermissionChecker_SessionOverride(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "perm-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Agent has full permission
	pc := NewPermissionChecker(tmpDir, PermissionConfig{
		Permission: PermissionFull,
	})

	filePath := filepath.Join(tmpDir, "test.txt")

	// Initially can delete
	allowed, _ := pc.CanDelete(filePath)
	if !allowed {
		t.Error("expected delete to be allowed with full permission")
	}

	// Session restricts to read-only
	pc.SetSessionConfig(PermissionConfig{
		Permission: PermissionReadOnly,
	})

	// Now cannot delete
	allowed, _ = pc.CanDelete(filePath)
	if allowed {
		t.Error("expected delete to be denied with session read-only override")
	}

	// But can still read
	allowed, _ = pc.CanRead(filePath)
	if !allowed {
		t.Error("expected read to be allowed with read-only permission")
	}
}

func TestPermissionChecker_AllowedPaths(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "perm-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	allowedDir, err := os.MkdirTemp("", "allowed-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(allowedDir)

	pc := NewPermissionChecker(tmpDir, PermissionConfig{
		Permission:   PermissionReadWrite,
		AllowedPaths: []string{allowedDir},
	})

	// Can access file in allowed path
	allowed, _ := pc.CanAccessPath(filepath.Join(allowedDir, "file.txt"))
	if !allowed {
		t.Error("expected access to allowed path")
	}

	// Cannot access file outside allowed paths
	allowed, _ = pc.CanAccessPath("/some/other/path/file.txt")
	if allowed {
		t.Error("expected denial for path outside allowed list")
	}
}

func TestPermissionChecker_DeniedExtensions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "perm-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	pc := NewPermissionChecker(tmpDir, PermissionConfig{
		Permission:       PermissionFull,
		DeniedExtensions: []string{".exe", ".dll"},
	})

	tests := []struct {
		filename string
		allowed  bool
	}{
		{"script.py", true},
		{"document.txt", true},
		{"malware.exe", false},
		{"library.dll", false},
		{"MALWARE.EXE", false}, // Case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			filePath := filepath.Join(tmpDir, tt.filename)
			allowed, _ := pc.CanRead(filePath)
			if allowed != tt.allowed {
				t.Errorf("CanRead(%s) = %v, want %v", tt.filename, allowed, tt.allowed)
			}
		})
	}
}

func TestPermissionChecker_AllowedExtensions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "perm-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	pc := NewPermissionChecker(tmpDir, PermissionConfig{
		Permission:        PermissionFull,
		AllowedExtensions: []string{".txt", ".md", ".json"},
	})

	tests := []struct {
		filename string
		allowed  bool
	}{
		{"document.txt", true},
		{"readme.md", true},
		{"config.json", true},
		{"script.py", false},
		{"image.png", false},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			filePath := filepath.Join(tmpDir, tt.filename)
			allowed, _ := pc.CanRead(filePath)
			if allowed != tt.allowed {
				t.Errorf("CanRead(%s) = %v, want %v", tt.filename, allowed, tt.allowed)
			}
		})
	}
}

func TestPermissionChecker_MaxFileSize(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "perm-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	pc := NewPermissionChecker(tmpDir, PermissionConfig{
		Permission:  PermissionReadWrite,
		MaxFileSize: 1024, // 1KB limit
	})

	filePath := filepath.Join(tmpDir, "test.txt")

	// Small file allowed
	allowed, _ := pc.CanWrite(filePath, 512)
	if !allowed {
		t.Error("expected small file to be allowed")
	}

	// Large file denied
	allowed, reason := pc.CanWrite(filePath, 2048)
	if allowed {
		t.Error("expected large file to be denied")
	}
	if reason != "file size exceeds limit" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestAuditLogger(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	logger, err := NewAuditLogger(tmpDir, "session-1", "agent-1")
	if err != nil {
		t.Fatalf("failed to create audit logger: %v", err)
	}

	// Log some entries
	logger.Log(AuditEntry{
		Operation: "read",
		Path:      "/test/file.txt",
		Allowed:   true,
	})

	logger.Log(AuditEntry{
		Operation: "write",
		Path:      "/test/file.txt",
		Allowed:   false,
		Reason:    "permission denied",
	})

	// Check entries before flush
	entries := logger.GetEntries()
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	// Flush and verify
	if err := logger.Flush(); err != nil {
		t.Fatalf("failed to flush: %v", err)
	}

	// Entries should be cleared after flush
	entries = logger.GetEntries()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after flush, got %d", len(entries))
	}

	// Verify log file was created
	files, _ := os.ReadDir(tmpDir)
	if len(files) == 0 {
		t.Error("expected audit log file to be created")
	}
}

func TestPermissionChecker_WithAuditLogger(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "perm-audit-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	logDir := filepath.Join(tmpDir, "logs")
	logger, err := NewAuditLogger(logDir, "session-1", "agent-1")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	pc := NewPermissionChecker(tmpDir, PermissionConfig{
		Permission: PermissionReadOnly,
	})
	pc.SetAuditLogger(logger)

	filePath := filepath.Join(tmpDir, "test.txt")

	// This should succeed and be logged
	pc.CanRead(filePath)

	// This should fail and be logged
	pc.CanWrite(filePath, 1024)

	entries := logger.GetEntries()
	if len(entries) != 2 {
		t.Errorf("expected 2 audit entries, got %d", len(entries))
	}

	// Check first entry (read - allowed)
	if entries[0].Operation != "read" || !entries[0].Allowed {
		t.Error("expected read operation to be allowed")
	}

	// Check second entry (write - denied)
	if entries[1].Operation != "write" || entries[1].Allowed {
		t.Error("expected write operation to be denied")
	}
}

func TestDefaultPermissionConfig(t *testing.T) {
	config := DefaultPermissionConfig()

	if config.Permission != PermissionReadWrite {
		t.Errorf("expected default permission to be read_write, got %s", config.Permission)
	}

	if config.MaxFileSize != 100*1024*1024 {
		t.Errorf("expected default max file size to be 100MB, got %d", config.MaxFileSize)
	}

	// Check denied extensions include common executables
	hasDenied := false
	for _, ext := range config.DeniedExtensions {
		if ext == ".exe" {
			hasDenied = true
			break
		}
	}
	if !hasDenied {
		t.Error("expected .exe to be in denied extensions")
	}
}
