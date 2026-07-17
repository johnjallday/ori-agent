package session

import "testing"

func TestNormalizeWorkspaceDesignation(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  WorkspaceDesignation
	}{
		{"empty", "", ""},
		{"personal_hq", "personal_hq", WorkspaceDesignationPersonalHQ},
		{"whitespace only", "   ", ""},
		{"padded personal_hq", "  personal_hq  ", WorkspaceDesignationPersonalHQ},
		{"unknown value", "team_hq", ""},
		{"wrong case", "Personal_HQ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeWorkspaceDesignation(tt.input); got != tt.want {
				t.Errorf("NormalizeWorkspaceDesignation(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWorkspaceIsPersonalHQ(t *testing.T) {
	tests := []struct {
		name        string
		designation WorkspaceDesignation
		want        bool
	}{
		{"empty", "", false},
		{"personal_hq", WorkspaceDesignationPersonalHQ, true},
		{"unknown value", WorkspaceDesignation("team_hq"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := Workspace{Designation: tt.designation}
			if got := w.IsPersonalHQ(); got != tt.want {
				t.Errorf("IsPersonalHQ() with designation %q = %v, want %v", tt.designation, got, tt.want)
			}
		})
	}
}
