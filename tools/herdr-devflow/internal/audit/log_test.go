package audit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoggerWritesStableRedactedLocalEvents(t *testing.T) {
	directory := t.TempDir()
	when := time.Date(2026, 7, 23, 14, 5, 6, 0, time.UTC)
	logger := Logger{Dir: directory, Now: func() time.Time { return when }}
	if err := logger.Record(Event{
		Operation: "schedule",
		Feature:   "bridge",
		Role:      "builder",
		Stage:     "delivery",
		Outcome:   "delivered",
	}); err != nil {
		t.Fatal(err)
	}
	if err := logger.Record(Event{
		Operation: "prompt with terminal output $ OPENAI_API_KEY=sk-super-secret",
		Feature:   "bridge",
		Role:      "builder",
		Stage:     "API_KEY",
		Outcome:   "failed\noutput=secret-value",
		Warning:   "Bearer another-secret",
	}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "sk-super-secret") || strings.Contains(string(contents), "OPENAI_API_KEY") || strings.Contains(string(contents), "secret-value") {
		t.Fatalf("audit log exposed sensitive text: %q", contents)
	}
	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(lines) != 2 {
		t.Fatalf("events = %q", contents)
	}
	var first, second Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if first.Timestamp != when || first.Operation != "schedule" || first.Feature != "bridge" || first.Role != "builder" || first.Stage != "delivery" || first.Outcome != "delivered" {
		t.Fatalf("first event = %#v", first)
	}
	if second.Operation != "redacted" || second.Stage != "redacted" || second.Outcome != "redacted" || second.Warning != "redacted" {
		t.Fatalf("second event = %#v", second)
	}
	info, err := os.Stat(logger.Path())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("audit log permissions = %v, %v", info, err)
	}
}
