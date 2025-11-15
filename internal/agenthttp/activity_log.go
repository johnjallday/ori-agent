package agenthttp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ActivityLogger handles logging and retrieval of agent activity logs
type ActivityLogger struct {
	logDir string
	mu     sync.RWMutex
}

// NewActivityLogger creates a new activity logger instance
func NewActivityLogger(logDir string) (*ActivityLogger, error) {
	// Create log directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	return &ActivityLogger{
		logDir: logDir,
	}, nil
}

// LogActivity logs an activity event for an agent
func (al *ActivityLogger) LogActivity(agentName string, eventType types.ActivityEventType, details map[string]interface{}, user string) error {
	al.mu.Lock()
	defer al.mu.Unlock()

	// Create activity log entry
	entry := types.ActivityLog{
		ID:        uuid.New().String(),
		AgentName: agentName,
		EventType: eventType,
		Timestamp: time.Now(),
		Details:   details,
		User:      user,
	}

	// Marshal to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal activity log: %w", err)
	}

	// Get log file path
	logFile := al.getLogFilePath(agentName)

	// Open file in append mode
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	// Write log entry (one per line - JSONL format)
	if _, err := f.WriteString(string(data) + "\n"); err != nil {
		return fmt.Errorf("failed to write log entry: %w", err)
	}

	return nil
}

// GetActivityLog retrieves activity logs for an agent with pagination and filtering
func (al *ActivityLogger) GetActivityLog(agentName string, limit, offset int, eventType types.ActivityEventType, startDate, endDate time.Time) ([]types.ActivityLog, int, error) {
	al.mu.RLock()
	defer al.mu.RUnlock()

	logFile := al.getLogFilePath(agentName)

	// Check if log file exists
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		return []types.ActivityLog{}, 0, nil // No logs yet
	}

	// Open and read log file
	f, err := os.Open(logFile)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	var allLogs []types.ActivityLog
	scanner := bufio.NewScanner(f)

	// Read all log entries
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var entry types.ActivityLog
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Skip malformed entries
			continue
		}

		// Apply filters
		if eventType != "" && entry.EventType != eventType {
			continue
		}

		if !startDate.IsZero() && entry.Timestamp.Before(startDate) {
			continue
		}

		if !endDate.IsZero() && entry.Timestamp.After(endDate) {
			continue
		}

		allLogs = append(allLogs, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("failed to read log file: %w", err)
	}

	// Sort by timestamp (newest first)
	sort.Slice(allLogs, func(i, j int) bool {
		return allLogs[i].Timestamp.After(allLogs[j].Timestamp)
	})

	// Apply pagination
	total := len(allLogs)

	if offset >= total {
		return []types.ActivityLog{}, total, nil
	}

	end := offset + limit
	if end > total {
		end = total
	}

	if limit <= 0 {
		// Return all logs if no limit specified
		return allLogs[offset:], total, nil
	}

	return allLogs[offset:end], total, nil
}

// GetRecentActivity gets the most recent N activity logs across all agents
func (al *ActivityLogger) GetRecentActivity(limit int) ([]types.ActivityLog, error) {
	al.mu.RLock()
	defer al.mu.RUnlock()

	var allLogs []types.ActivityLog

	// Read all log files in the directory
	entries, err := os.ReadDir(al.logDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read log directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		logFile := filepath.Join(al.logDir, entry.Name())
		f, err := os.Open(logFile)
		if err != nil {
			continue // Skip files we can't read
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}

			var logEntry types.ActivityLog
			if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
				continue // Skip malformed entries
			}

			allLogs = append(allLogs, logEntry)
		}

		f.Close()
	}

	// Sort by timestamp (newest first)
	sort.Slice(allLogs, func(i, j int) bool {
		return allLogs[i].Timestamp.After(allLogs[j].Timestamp)
	})

	// Return top N logs
	if limit > 0 && limit < len(allLogs) {
		return allLogs[:limit], nil
	}

	return allLogs, nil
}

// DeleteAgentLogs deletes all activity logs for a specific agent
func (al *ActivityLogger) DeleteAgentLogs(agentName string) error {
	al.mu.Lock()
	defer al.mu.Unlock()

	logFile := al.getLogFilePath(agentName)

	// Check if file exists
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		return nil // No logs to delete
	}

	// Delete the log file
	if err := os.Remove(logFile); err != nil {
		return fmt.Errorf("failed to delete log file: %w", err)
	}

	return nil
}

// getLogFilePath returns the path to the log file for a specific agent
func (al *ActivityLogger) getLogFilePath(agentName string) string {
	// Sanitize agent name for filename
	safeName := strings.ReplaceAll(agentName, "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")
	return filepath.Join(al.logDir, safeName+".jsonl")
}

// FormatLogEntry formats an activity log into a human-readable entry
func FormatLogEntry(log types.ActivityLog) types.ActivityLogEntry {
	entry := types.ActivityLogEntry{
		ID:        log.ID,
		AgentName: log.AgentName,
		EventType: string(log.EventType),
		Timestamp: log.Timestamp,
		User:      log.User,
	}

	// Set event-specific title, description, icon, and color
	switch log.EventType {
	case types.ActivityEventCreated:
		entry.EventTitle = "Agent Created"
		entry.Description = fmt.Sprintf("Agent '%s' was created", log.AgentName)
		entry.Icon = "➕"
		entry.Color = "#28a745" // Green

	case types.ActivityEventUpdated:
		entry.EventTitle = "Agent Updated"
		entry.Description = fmt.Sprintf("Agent '%s' configuration was updated", log.AgentName)
		entry.Icon = "✏️"
		entry.Color = "#007bff" // Blue

		// Add details about what was updated
		if log.Details != nil {
			if fields, ok := log.Details["fields"].([]interface{}); ok && len(fields) > 0 {
				entry.Description += fmt.Sprintf(" (%v)", fields)
			}
		}

	case types.ActivityEventDeleted:
		entry.EventTitle = "Agent Deleted"
		entry.Description = fmt.Sprintf("Agent '%s' was deleted", log.AgentName)
		entry.Icon = "🗑️"
		entry.Color = "#dc3545" // Red

	case types.ActivityEventMessageSent:
		entry.EventTitle = "Message Sent"
		entry.Description = fmt.Sprintf("Chat message sent to '%s'", log.AgentName)
		entry.Icon = "💬"
		entry.Color = "#17a2b8" // Cyan

		// Add message details
		if log.Details != nil {
			if tokens, ok := log.Details["tokens"].(float64); ok {
				entry.Description += fmt.Sprintf(" (%d tokens)", int(tokens))
			}
		}

	case types.ActivityEventPluginEnabled:
		entry.EventTitle = "Plugin Enabled"
		entry.Icon = "🔌"
		entry.Color = "#28a745" // Green

		if log.Details != nil {
			if plugin, ok := log.Details["plugin"].(string); ok {
				entry.Description = fmt.Sprintf("Plugin '%s' enabled for '%s'", plugin, log.AgentName)
			}
		}

	case types.ActivityEventPluginDisabled:
		entry.EventTitle = "Plugin Disabled"
		entry.Icon = "🔌"
		entry.Color = "#ffc107" // Yellow

		if log.Details != nil {
			if plugin, ok := log.Details["plugin"].(string); ok {
				entry.Description = fmt.Sprintf("Plugin '%s' disabled for '%s'", plugin, log.AgentName)
			}
		}

	case types.ActivityEventStatusChanged:
		entry.EventTitle = "Status Changed"
		entry.Icon = "🔄"
		entry.Color = "#6c757d" // Gray

		if log.Details != nil {
			oldStatus, hasOld := log.Details["old_status"].(string)
			newStatus, hasNew := log.Details["new_status"].(string)

			if hasOld && hasNew {
				entry.Description = fmt.Sprintf("Status changed from '%s' to '%s'", oldStatus, newStatus)
			} else if hasNew {
				entry.Description = fmt.Sprintf("Status changed to '%s'", newStatus)
			}
		}

	default:
		entry.EventTitle = string(log.EventType)
		entry.Description = fmt.Sprintf("Activity on agent '%s'", log.AgentName)
		entry.Icon = "📝"
		entry.Color = "#6c757d" // Gray
	}

	return entry
}
