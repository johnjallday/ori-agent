package port

import "testing"

func TestIsOriProcessName(t *testing.T) {
	cases := map[string]bool{
		"ori-agent":          true,
		"ORI-AGENT":          true,
		"ori-agent.exe":      true,
		"/usr/bin/ori-agent": true,
		"ori-menubar":        true,
		"ori-menubar.exe":    true,
		"":                   false,
		"main":               false,
		"other-app":          false,
	}

	for input, expected := range cases {
		if got := IsOriProcessName(input); got != expected {
			t.Errorf("IsOriProcessName(%q) = %v, want %v", input, got, expected)
		}
	}
}

func TestFormatProcessSummary(t *testing.T) {
	processes := []ProcessInfo{{PID: 123, Name: "ori-agent"}, {PID: 0, Name: ""}}
	summary := FormatProcessSummary(processes)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if summary == "unknown process" {
		t.Fatalf("unexpected summary %q", summary)
	}
}
