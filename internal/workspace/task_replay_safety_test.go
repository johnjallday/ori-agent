package workspace

import "testing"

// Replay safety must answer "is repeating this safe?" from evidence, never from
// optimism. The asymmetry is deliberate: a wrongly-blocked replay costs the user
// one click, while a wrongly-allowed replay can send a second email or move a
// file twice.

func TestEvaluateReplaySafety(t *testing.T) {
	cases := []struct {
		name        string
		attempts    []ToolAttempt
		wantSafe    bool
		wantUnsafe  []string
		wantReasonA string // substring the reason must contain
	}{
		{
			name:        "no tools ran at all",
			wantSafe:    true,
			wantReasonA: "no tools",
		},
		{
			name: "only completed reads",
			attempts: []ToolAttempt{
				{Name: "read_file", Class: ToolSideEffectRead, Completed: true},
				{Name: "mail_search_threads", Class: ToolSideEffectRead, Completed: true},
			},
			wantSafe:    true,
			wantReasonA: "read-only",
		},
		{
			name: "a read that failed midway is still safe",
			attempts: []ToolAttempt{
				{Name: "read_file", Class: ToolSideEffectRead, Completed: false},
			},
			wantSafe: true,
		},
		{
			name: "a completed write blocks replay",
			attempts: []ToolAttempt{
				{Name: "read_file", Class: ToolSideEffectRead, Completed: true},
				{Name: "write_file", Class: ToolSideEffectWrite, Completed: true},
			},
			wantUnsafe:  []string{"write_file"},
			wantReasonA: "already ran",
		},
		{
			// The worst case: it may or may not have taken effect, and nothing in
			// the failure says which.
			name: "an ambiguous failed write blocks replay and says so",
			attempts: []ToolAttempt{
				{Name: "move_file", Class: ToolSideEffectWrite, Completed: false},
			},
			wantUnsafe:  []string{"move_file"},
			wantReasonA: "effect is unknown",
		},
		{
			name: "a draft proposal counts as a mutation",
			attempts: []ToolAttempt{
				{Name: "mail_draft_reply", Class: ToolSideEffectConfirm, Completed: true},
			},
			wantUnsafe: []string{"mail_draft_reply"},
		},
		{
			// Absence of classification is not evidence of safety.
			name: "an unclassified tool blocks replay",
			attempts: []ToolAttempt{
				{Name: "mystery_tool", Class: ToolSideEffectUnknown, Completed: true},
			},
			wantUnsafe: []string{"mystery_tool"},
		},
		{
			name: "unsafe tools are reported once, sorted",
			attempts: []ToolAttempt{
				{Name: "write_file", Class: ToolSideEffectWrite, Completed: true},
				{Name: "delete_file", Class: ToolSideEffectWrite, Completed: true},
				{Name: "write_file", Class: ToolSideEffectWrite, Completed: true},
			},
			wantUnsafe: []string{"delete_file", "write_file"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TaskAttemptEvidence{Attempts: tc.attempts}.EvaluateReplaySafety()
			if got.Safe != tc.wantSafe {
				t.Fatalf("safe = %v, want %v (reason %q)", got.Safe, tc.wantSafe, got.Reason)
			}
			if len(got.UnsafeTools) != len(tc.wantUnsafe) {
				t.Fatalf("unsafe tools = %v, want %v", got.UnsafeTools, tc.wantUnsafe)
			}
			for i, want := range tc.wantUnsafe {
				if got.UnsafeTools[i] != want {
					t.Fatalf("unsafe tools = %v, want %v", got.UnsafeTools, tc.wantUnsafe)
				}
			}
			if tc.wantReasonA != "" && !contains(got.Reason, tc.wantReasonA) {
				t.Fatalf("reason = %q, want it to mention %q", got.Reason, tc.wantReasonA)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestClassifyToolSideEffect(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		declared SideEffect
		want     ToolSideEffectClass
	}{
		// A declared classification always wins: the workspace owner said so, and
		// a mixed-capability server can only be classified per tool.
		{"declared read wins", "write_file", SideEffectRead, ToolSideEffectRead},
		{"declared write wins", "read_file", SideEffectWrite, ToolSideEffectWrite},
		{"external counts as write", "http_post", SideEffectExternal, ToolSideEffectWrite},

		// Undeclared falls back to known names.
		{"known read", "read_file", "", ToolSideEffectRead},
		{"known read, mail", "mail_search_threads", "", ToolSideEffectRead},
		{"known write", "move_file", "", ToolSideEffectWrite},
		{"draft is confirm-gated", "mail_draft_reply", "", ToolSideEffectConfirm},

		// Namespaced runtime MCP tools classify on the bare name.
		{"namespaced read", "ws:w1:mcp:filesystem:b1__read_file", "", ToolSideEffectRead},
		{"namespaced write", "ws:w1:mcp:filesystem:b1__write_file", "", ToolSideEffectWrite},

		// Case and padding.
		{"upper case", "  READ_FILE ", "", ToolSideEffectRead},

		// Everything else fails closed.
		{"unknown tool", "frobnicate", "", ToolSideEffectUnknown},
		{"empty name", "", "", ToolSideEffectUnknown},
		// A name that merely looks readable is not evidence.
		{"reader-sounding but unknown", "read_the_room", "", ToolSideEffectUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyToolSideEffect(tc.tool, tc.declared); got != tc.want {
				t.Fatalf("class = %q, want %q", got, tc.want)
			}
		})
	}
}
