// Package audit writes small, redacted, user-local operation records. It is
// deliberately incapable of accepting prompts, environment values, or terminal
// output as event fields.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const FileName = "events.jsonl"

var tokenPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,79}$`)

type Event struct {
	Timestamp time.Time `json:"timestamp"`
	Operation string    `json:"operation"`
	Feature   string    `json:"feature,omitempty"`
	Role      string    `json:"role,omitempty"`
	Stage     string    `json:"stage"`
	Outcome   string    `json:"outcome"`
	Warning   string    `json:"warning,omitempty"`
}

type Logger struct {
	Dir string
	Now func() time.Time
}

func (l Logger) Path() string { return filepath.Join(l.Dir, FileName) }

func (l Logger) Record(event Event) error {
	if strings.TrimSpace(l.Dir) == "" {
		return fmt.Errorf("audit log directory is required")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = l.now()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	event.Operation = safeToken(event.Operation)
	event.Feature = safeToken(event.Feature)
	event.Role = safeToken(event.Role)
	event.Stage = safeToken(event.Stage)
	event.Outcome = safeToken(event.Outcome)
	event.Warning = safeToken(event.Warning)
	if err := os.MkdirAll(l.Dir, 0700); err != nil {
		return fmt.Errorf("create audit log directory: %w", err)
	}
	file, err := os.OpenFile(l.Path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("secure audit log: %w", err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode audit event: %w", err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("append audit event: %w", err)
	}
	return nil
}

func (l Logger) now() time.Time {
	if l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

func safeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"secret", "token", "password", "apikey", "api_key", "authorization", "bearer", "sk-", "key="} {
		if strings.Contains(lower, forbidden) {
			return "redacted"
		}
	}
	if !tokenPattern.MatchString(value) {
		return "redacted"
	}
	return value
}
