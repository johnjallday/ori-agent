package orchestrationhttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestResponseNeedsUserInput(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     bool
	}{
		{
			name: "clarification request",
			response: `I've received your task.
However, I need clarification to complete this task:
1. Which location should I check?
2. What format do you need?`,
			want: true,
		},
		{
			name: "direct answer",
			response: `Seoul weather summary:
- Temperature: 11C
- Precipitation: 10%
- Wind: 8 km/h`,
			want: false,
		},
		{
			name:     "empty output",
			response: "   ",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := responseNeedsUserInput(tt.response)
			if got != tt.want {
				t.Fatalf("responseNeedsUserInput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveTaskExecutionAttempts(t *testing.T) {
	tests := []struct {
		name string
		task *workspace.Task
		want int
	}{
		{
			name: "default attempts",
			task: &workspace.Task{},
			want: defaultTaskExecutionAttempts,
		},
		{
			name: "string override",
			task: &workspace.Task{
				Context: map[string]interface{}{"max_attempts": "4"},
			},
			want: 4,
		},
		{
			name: "float override",
			task: &workspace.Task{
				Context: map[string]interface{}{"retry_attempts": 2.0},
			},
			want: 2,
		},
		{
			name: "clamped to max",
			task: &workspace.Task{
				Context: map[string]interface{}{"execution_max_attempts": 20},
			},
			want: maxTaskExecutionAttempts,
		},
		{
			name: "invalid keeps default",
			task: &workspace.Task{
				Context: map[string]interface{}{"max_attempts": "invalid"},
			},
			want: defaultTaskExecutionAttempts,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveTaskExecutionAttempts(tt.task)
			if got != tt.want {
				t.Fatalf("resolveTaskExecutionAttempts() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestExtractClarificationQuestion(t *testing.T) {
	response := "I need clarification.\nWhich city should I check weather for?\nPlease confirm."
	got := extractClarificationQuestion(response)
	if got != "Which city should I check weather for?" {
		t.Fatalf("extractClarificationQuestion() = %q, want %q", got, "Which city should I check weather for?")
	}
}
