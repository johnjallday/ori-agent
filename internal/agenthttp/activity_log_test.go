package agenthttp

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestFormatLogEntry_EvolutionFeed(t *testing.T) {
	entry := FormatLogEntry(types.ActivityLog{
		ID:        "1",
		AgentName: "alpha",
		EventType: types.ActivityEventEvolutionFeed,
		Timestamp: time.Now(),
		Details: map[string]interface{}{
			"source": "manual",
		},
	})

	if entry.EventTitle != "Agent Fed" {
		t.Fatalf("expected EventTitle Agent Fed, got %q", entry.EventTitle)
	}
	if entry.Description == "" {
		t.Fatal("expected description to be set")
	}
}

func TestFormatLogEntry_EvolutionStage(t *testing.T) {
	entry := FormatLogEntry(types.ActivityLog{
		ID:        "2",
		AgentName: "alpha",
		EventType: types.ActivityEventEvolutionStage,
		Timestamp: time.Now(),
		Details: map[string]interface{}{
			"old_stage": "spark",
			"new_stage": "infant",
		},
	})

	if entry.EventTitle != "Stage Evolved" {
		t.Fatalf("expected EventTitle Stage Evolved, got %q", entry.EventTitle)
	}
	if entry.Description != "Stage changed from 'spark' to 'infant'" {
		t.Fatalf("unexpected description: %q", entry.Description)
	}
}

func TestFormatLogEntry_EvolutionPath(t *testing.T) {
	entry := FormatLogEntry(types.ActivityLog{
		ID:        "3",
		AgentName: "alpha",
		EventType: types.ActivityEventEvolutionPath,
		Timestamp: time.Now(),
		Details: map[string]interface{}{
			"path": "coder",
		},
	})

	if entry.EventTitle != "Path Selected" {
		t.Fatalf("expected EventTitle Path Selected, got %q", entry.EventTitle)
	}
	if entry.Description != "Evolution path set to 'coder'" {
		t.Fatalf("unexpected description: %q", entry.Description)
	}
}
