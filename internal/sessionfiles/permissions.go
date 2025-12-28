package sessionfiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FilePermission represents the level of file access granted
type FilePermission string

const (
	// PermissionNone means no file access
	PermissionNone FilePermission = "none"
	// PermissionReadOnly allows reading files but not modifying
	PermissionReadOnly FilePermission = "read_only"
	// PermissionReadWrite allows reading and writing files
	PermissionReadWrite FilePermission = "read_write"
	// PermissionFull allows all operations including delete
	PermissionFull FilePermission = "full"
)

// ValidPermissions returns all valid permission levels
func ValidPermissions() []FilePermission {
	return []FilePermission{
		PermissionNone,
		PermissionReadOnly,
		PermissionReadWrite,
		PermissionFull,
	}
}

// IsValid checks if the permission is a valid value
func (p FilePermission) IsValid() bool {
	for _, valid := range ValidPermissions() {
		if p == valid {
			return true
		}
	}
	return false
}

// CanRead returns true if this permission level allows reading
func (p FilePermission) CanRead() bool {
	return p == PermissionReadOnly || p == PermissionReadWrite || p == PermissionFull
}

// CanWrite returns true if this permission level allows writing
func (p FilePermission) CanWrite() bool {
	return p == PermissionReadWrite || p == PermissionFull
}

// CanDelete returns true if this permission level allows deleting
func (p FilePermission) CanDelete() bool {
	return p == PermissionFull
}

// PermissionConfig holds permission settings for file access
type PermissionConfig struct {
	// Permission is the base permission level
	Permission FilePermission `json:"permission"`
	// AllowedPaths are additional paths outside the session folder that can be accessed
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	// AllowedExtensions restricts which file extensions can be accessed (empty = all)
	AllowedExtensions []string `json:"allowed_extensions,omitempty"`
	// DeniedExtensions are file extensions that cannot be accessed
	DeniedExtensions []string `json:"denied_extensions,omitempty"`
	// MaxFileSize is the maximum file size in bytes (0 = no limit)
	MaxFileSize int64 `json:"max_file_size,omitempty"`
}

// DefaultPermissionConfig returns the default permission configuration
func DefaultPermissionConfig() PermissionConfig {
	return PermissionConfig{
		Permission:        PermissionReadWrite,
		AllowedPaths:      []string{},
		AllowedExtensions: []string{},
		DeniedExtensions:  []string{".exe", ".dll", ".so", ".dylib"}, // Deny executables by default
		MaxFileSize:       100 * 1024 * 1024,                         // 100MB default
	}
}

// PermissionChecker validates file operations against permission settings
type PermissionChecker struct {
	agentConfig   PermissionConfig
	sessionConfig *PermissionConfig // Optional session-level override
	sessionDir    string
	auditLogger   *AuditLogger
	mu            sync.RWMutex
}

// NewPermissionChecker creates a new permission checker
func NewPermissionChecker(sessionDir string, agentConfig PermissionConfig) *PermissionChecker {
	return &PermissionChecker{
		agentConfig: agentConfig,
		sessionDir:  sessionDir,
	}
}

// SetSessionConfig sets session-level permission overrides
// Session config can only be MORE restrictive than agent config
func (pc *PermissionChecker) SetSessionConfig(config PermissionConfig) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.sessionConfig = &config
}

// SetAuditLogger sets the audit logger for recording access attempts
func (pc *PermissionChecker) SetAuditLogger(logger *AuditLogger) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.auditLogger = logger
}

// getEffectivePermission returns the effective permission level
// Session config can only make permissions MORE restrictive
func (pc *PermissionChecker) getEffectivePermission() FilePermission {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	agentPerm := pc.agentConfig.Permission
	if pc.sessionConfig == nil {
		return agentPerm
	}

	sessionPerm := pc.sessionConfig.Permission

	// Order of restrictiveness: none < read_only < read_write < full
	permOrder := map[FilePermission]int{
		PermissionNone:      0,
		PermissionReadOnly:  1,
		PermissionReadWrite: 2,
		PermissionFull:      3,
	}

	// Return the more restrictive permission
	if permOrder[sessionPerm] < permOrder[agentPerm] {
		return sessionPerm
	}
	return agentPerm
}

// CanRead checks if reading the specified file is allowed
func (pc *PermissionChecker) CanRead(filePath string) (bool, string) {
	perm := pc.getEffectivePermission()
	if !perm.CanRead() {
		pc.logAccess("read", filePath, false, "permission denied: no read access")
		return false, "permission denied: no read access"
	}

	if !pc.isPathAllowed(filePath) {
		pc.logAccess("read", filePath, false, "path not allowed")
		return false, "path not allowed"
	}

	if !pc.isExtensionAllowed(filePath) {
		pc.logAccess("read", filePath, false, "file extension not allowed")
		return false, "file extension not allowed"
	}

	pc.logAccess("read", filePath, true, "")
	return true, ""
}

// CanWrite checks if writing to the specified file is allowed
func (pc *PermissionChecker) CanWrite(filePath string, size int64) (bool, string) {
	perm := pc.getEffectivePermission()
	if !perm.CanWrite() {
		pc.logAccess("write", filePath, false, "permission denied: no write access")
		return false, "permission denied: no write access"
	}

	if !pc.isPathAllowed(filePath) {
		pc.logAccess("write", filePath, false, "path not allowed")
		return false, "path not allowed"
	}

	if !pc.isExtensionAllowed(filePath) {
		pc.logAccess("write", filePath, false, "file extension not allowed")
		return false, "file extension not allowed"
	}

	if !pc.isSizeAllowed(size) {
		pc.logAccess("write", filePath, false, "file size exceeds limit")
		return false, "file size exceeds limit"
	}

	pc.logAccess("write", filePath, true, "")
	return true, ""
}

// CanDelete checks if deleting the specified file is allowed
func (pc *PermissionChecker) CanDelete(filePath string) (bool, string) {
	perm := pc.getEffectivePermission()
	if !perm.CanDelete() {
		pc.logAccess("delete", filePath, false, "permission denied: no delete access")
		return false, "permission denied: no delete access"
	}

	if !pc.isPathAllowed(filePath) {
		pc.logAccess("delete", filePath, false, "path not allowed")
		return false, "path not allowed"
	}

	pc.logAccess("delete", filePath, true, "")
	return true, ""
}

// CanAccessPath checks if a path outside the session folder is accessible
func (pc *PermissionChecker) CanAccessPath(externalPath string) (bool, string) {
	pc.mu.RLock()
	allowedPaths := pc.agentConfig.AllowedPaths
	if pc.sessionConfig != nil && len(pc.sessionConfig.AllowedPaths) > 0 {
		// Session config can only restrict, so use intersection
		allowedPaths = intersectPaths(allowedPaths, pc.sessionConfig.AllowedPaths)
	}
	pc.mu.RUnlock()

	// Normalize the external path
	absPath, err := filepath.Abs(externalPath)
	if err != nil {
		return false, "invalid path"
	}

	// Check if path is within any allowed path
	for _, allowed := range allowedPaths {
		allowedAbs, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}

		// Check if absPath is under allowedAbs
		if strings.HasPrefix(absPath, allowedAbs+string(filepath.Separator)) || absPath == allowedAbs {
			return true, ""
		}
	}

	return false, "path not in allowed paths list"
}

// isPathAllowed checks if the file path is allowed (within session or allowed paths)
func (pc *PermissionChecker) isPathAllowed(filePath string) bool {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}

	// Always allow paths within session directory
	sessionAbs, err := filepath.Abs(pc.sessionDir)
	if err != nil {
		return false
	}

	if strings.HasPrefix(absPath, sessionAbs+string(filepath.Separator)) || absPath == sessionAbs {
		return true
	}

	// Check allowed external paths
	allowed, _ := pc.CanAccessPath(filePath)
	return allowed
}

// isExtensionAllowed checks if the file extension is allowed
func (pc *PermissionChecker) isExtensionAllowed(filePath string) bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == "" {
		return true // No extension, allow by default
	}

	// Check denied extensions first
	deniedExts := pc.agentConfig.DeniedExtensions
	if pc.sessionConfig != nil && len(pc.sessionConfig.DeniedExtensions) > 0 {
		// Merge denied extensions (union)
		deniedExts = append(deniedExts, pc.sessionConfig.DeniedExtensions...)
	}

	for _, denied := range deniedExts {
		if strings.EqualFold(ext, denied) {
			return false
		}
	}

	// Check allowed extensions (if specified)
	allowedExts := pc.agentConfig.AllowedExtensions
	if pc.sessionConfig != nil && len(pc.sessionConfig.AllowedExtensions) > 0 {
		allowedExts = pc.sessionConfig.AllowedExtensions
	}

	if len(allowedExts) == 0 {
		return true // No restrictions
	}

	for _, allowed := range allowedExts {
		if strings.EqualFold(ext, allowed) {
			return true
		}
	}

	return false
}

// isSizeAllowed checks if the file size is within limits
func (pc *PermissionChecker) isSizeAllowed(size int64) bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	maxSize := pc.agentConfig.MaxFileSize
	if pc.sessionConfig != nil && pc.sessionConfig.MaxFileSize > 0 {
		// Use the more restrictive (smaller) limit
		if pc.sessionConfig.MaxFileSize < maxSize || maxSize == 0 {
			maxSize = pc.sessionConfig.MaxFileSize
		}
	}

	if maxSize == 0 {
		return true // No limit
	}

	return size <= maxSize
}

// logAccess logs an access attempt to the audit logger
func (pc *PermissionChecker) logAccess(operation, path string, allowed bool, reason string) {
	pc.mu.RLock()
	logger := pc.auditLogger
	pc.mu.RUnlock()

	if logger != nil {
		logger.Log(AuditEntry{
			Timestamp: time.Now(),
			Operation: operation,
			Path:      path,
			Allowed:   allowed,
			Reason:    reason,
		})
	}
}

// intersectPaths returns paths that exist in both lists
func intersectPaths(a, b []string) []string {
	result := []string{}
	bSet := make(map[string]bool)
	for _, p := range b {
		abs, err := filepath.Abs(p)
		if err == nil {
			bSet[abs] = true
		}
	}

	for _, p := range a {
		abs, err := filepath.Abs(p)
		if err == nil {
			if bSet[abs] {
				result = append(result, p)
			}
		}
	}

	return result
}

// AuditEntry represents a single audit log entry
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Operation string    `json:"operation"`
	Path      string    `json:"path"`
	Allowed   bool      `json:"allowed"`
	Reason    string    `json:"reason,omitempty"`
	AgentName string    `json:"agent_name,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
}

// AuditLogger handles audit logging for file operations
type AuditLogger struct {
	logDir    string
	sessionID string
	agentName string
	entries   []AuditEntry
	mu        sync.Mutex
}

// NewAuditLogger creates a new audit logger
func NewAuditLogger(logDir, sessionID, agentName string) (*AuditLogger, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create audit log directory: %w", err)
	}

	return &AuditLogger{
		logDir:    logDir,
		sessionID: sessionID,
		agentName: agentName,
		entries:   []AuditEntry{},
	}, nil
}

// Log records an audit entry
func (al *AuditLogger) Log(entry AuditEntry) {
	al.mu.Lock()
	defer al.mu.Unlock()

	entry.SessionID = al.sessionID
	entry.AgentName = al.agentName
	al.entries = append(al.entries, entry)

	// Auto-flush every 100 entries
	if len(al.entries) >= 100 {
		al.flushLocked()
	}
}

// Flush writes all pending entries to disk
func (al *AuditLogger) Flush() error {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.flushLocked()
}

// flushLocked writes entries to disk (must be called with lock held)
func (al *AuditLogger) flushLocked() error {
	if len(al.entries) == 0 {
		return nil
	}

	// Create log file with date-based name
	logFile := filepath.Join(al.logDir, fmt.Sprintf("audit_%s.jsonl", time.Now().Format("2006-01-02")))

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open audit log: %w", err)
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	for _, entry := range al.entries {
		if err := encoder.Encode(entry); err != nil {
			return fmt.Errorf("failed to write audit entry: %w", err)
		}
	}

	al.entries = al.entries[:0] // Clear entries
	return nil
}

// GetEntries returns all entries (for testing)
func (al *AuditLogger) GetEntries() []AuditEntry {
	al.mu.Lock()
	defer al.mu.Unlock()
	return append([]AuditEntry{}, al.entries...)
}

// Close flushes any pending entries and closes the logger
func (al *AuditLogger) Close() error {
	return al.Flush()
}
