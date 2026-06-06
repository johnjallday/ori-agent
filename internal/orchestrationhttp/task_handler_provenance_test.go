package orchestrationhttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestResolveCreateTaskProvenance(t *testing.T) {
	tests := []struct {
		name           string
		mode           string
		assignedBy     string
		reason         string
		wantMode       workspace.TaskAssignmentMode
		wantAssignedBy string
		wantReason     string
	}{
		{
			name:           "explicit static_plan honored",
			mode:           "static_plan",
			assignedBy:     "Manager",
			reason:         "coordinator plan",
			wantMode:       workspace.TaskAssignmentModeStaticPlan,
			wantAssignedBy: "Manager",
			wantReason:     "coordinator plan",
		},
		{
			name:           "empty defaults to manual",
			mode:           "",
			assignedBy:     "ignored",
			reason:         "user note",
			wantMode:       workspace.TaskAssignmentModeManual,
			wantAssignedBy: workspace.TaskAssignedByManual,
			wantReason:     "user note",
		},
		{
			name:           "legacy_unknown is not honored as explicit",
			mode:           "legacy_unknown",
			wantMode:       workspace.TaskAssignmentModeManual,
			wantAssignedBy: workspace.TaskAssignedByManual,
		},
		{
			name:           "unknown mode defaults to manual",
			mode:           "bogus",
			wantMode:       workspace.TaskAssignmentModeManual,
			wantAssignedBy: workspace.TaskAssignedByManual,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, assignedBy, reason := resolveCreateTaskProvenance(tt.mode, tt.assignedBy, tt.reason)
			if mode != tt.wantMode || assignedBy != tt.wantAssignedBy || reason != tt.wantReason {
				t.Fatalf("resolveCreateTaskProvenance(%q,%q,%q) = (%q,%q,%q), want (%q,%q,%q)",
					tt.mode, tt.assignedBy, tt.reason, mode, assignedBy, reason,
					tt.wantMode, tt.wantAssignedBy, tt.wantReason)
			}
		})
	}
}
