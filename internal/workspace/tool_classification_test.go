package workspace

import "testing"

func TestSuggestSideEffect_ReadPrefixes(t *testing.T) {
	cases := []struct {
		name string
		want SideEffect
	}{
		{"read_file", SideEffectRead},
		{"Read_File", SideEffectRead}, // case-insensitive
		{"get_user", SideEffectRead},
		{"list_workspaces", SideEffectRead},
		{"search_issues", SideEffectRead},
		{"find_by_id", SideEffectRead},
		{"describe_schema", SideEffectRead},
		{"inspect_object", SideEffectRead},
		{"fetch_remote", SideEffectRead},
		{"query_db", SideEffectRead},
		{"show_config", SideEffectRead},
	}
	for _, c := range cases {
		if got := SuggestSideEffect(c.name); got != c.want {
			t.Errorf("SuggestSideEffect(%q) = %q; want %q", c.name, got, c.want)
		}
	}
}

func TestSuggestSideEffect_NonReadReturnsEmpty(t *testing.T) {
	// Anything we can't confidently classify must return empty so the user
	// is forced to decide. Heuristic must not guess at write/external.
	cases := []string{
		"write_file",
		"delete_branch",
		"create_issue",
		"update_config",
		"post_message",
		"send_email",
		"foo",          // unprefixed
		"reader_clone", // does not start with read_
		"",
	}
	for _, name := range cases {
		if got := SuggestSideEffect(name); got != "" {
			t.Errorf("SuggestSideEffect(%q) = %q; want empty (no heuristic)", name, got)
		}
	}
}

func TestResolveSideEffect_OverrideWinsOverDefault(t *testing.T) {
	overrides := map[string]SideEffect{
		"read_file": SideEffectRead,
		"http_post": SideEffectExternal,
	}
	if got := ResolveSideEffect(SideEffectWrite, overrides, "read_file"); got != SideEffectRead {
		t.Errorf("override should win; got %q", got)
	}
	if got := ResolveSideEffect(SideEffectWrite, overrides, "http_post"); got != SideEffectExternal {
		t.Errorf("override should win; got %q", got)
	}
}

func TestResolveSideEffect_FallsBackToDefault(t *testing.T) {
	overrides := map[string]SideEffect{"specific_tool": SideEffectRead}
	if got := ResolveSideEffect(SideEffectWrite, overrides, "some_other_tool"); got != SideEffectWrite {
		t.Errorf("expected fallback to default; got %q", got)
	}
}

func TestResolveSideEffect_NoOverridesOrDefault(t *testing.T) {
	if got := ResolveSideEffect("", nil, "anything"); got != "" {
		t.Errorf("unclassified must remain empty; got %q", got)
	}
	// Empty-string override (sentinel for "deliberately cleared") should fall
	// through to the default rather than masquerade as a classification.
	overrides := map[string]SideEffect{"tool_x": ""}
	if got := ResolveSideEffect(SideEffectRead, overrides, "tool_x"); got != SideEffectRead {
		t.Errorf("empty override must fall through to default; got %q", got)
	}
}

func TestIsAllowedUnderPolicy_Watch(t *testing.T) {
	cases := map[SideEffect]bool{
		SideEffectRead:     true,
		SideEffectWrite:    false,
		SideEffectExternal: false,
		"":                 false, // unclassified always denied
	}
	for se, want := range cases {
		if got := IsAllowedUnderPolicy(AutonomyWatch, se); got != want {
			t.Errorf("Watch + %q: got %v; want %v", se, got, want)
		}
	}
}

func TestIsAllowedUnderPolicy_Propose(t *testing.T) {
	cases := map[SideEffect]bool{
		SideEffectRead:     true,
		SideEffectWrite:    true,
		SideEffectExternal: false,
		"":                 false,
	}
	for se, want := range cases {
		if got := IsAllowedUnderPolicy(AutonomyPropose, se); got != want {
			t.Errorf("Propose + %q: got %v; want %v", se, got, want)
		}
	}
}

func TestIsAllowedUnderPolicy_UnknownPolicyDenies(t *testing.T) {
	// Future policies (Act-with-approval, Autopilot) are deferred. Until
	// they're implemented, any unknown policy must deny by default so
	// half-built features don't accidentally unlock external actions.
	if IsAllowedUnderPolicy(AutonomyPolicy("act_with_approval"), SideEffectRead) {
		t.Error("unknown policy must deny; got allow")
	}
}

func TestUnclassifiedBindings_FindsEnabledUnclassified(t *testing.T) {
	ws := &Workspace{
		MCPBindings: []WorkspaceMCPBinding{
			{ID: "mcp-classified", Enabled: true, DefaultSideEffect: SideEffectRead},
			{ID: "mcp-unclassified", Enabled: true},
			{ID: "mcp-disabled-unclassified", Enabled: false}, // ignored: disabled
			{ID: "mcp-bad-value", Enabled: true, DefaultSideEffect: SideEffect("bogus")},
		},
		SkillBindings: []WorkspaceSkillBinding{
			{ID: "skill-classified", Enabled: true, DefaultSideEffect: SideEffectRead},
			{ID: "skill-unclassified", Enabled: true},
		},
	}
	mcp, sk := UnclassifiedBindings(ws)

	wantMCP := map[string]bool{"mcp-unclassified": true, "mcp-bad-value": true}
	if len(mcp) != len(wantMCP) {
		t.Fatalf("unclassified MCP = %v; want %v", mcp, wantMCP)
	}
	for _, id := range mcp {
		if !wantMCP[id] {
			t.Errorf("unexpected unclassified MCP binding %q", id)
		}
	}
	if len(sk) != 1 || sk[0] != "skill-unclassified" {
		t.Errorf("unclassified skills = %v; want [skill-unclassified]", sk)
	}
}

func TestMissionBindingsReady(t *testing.T) {
	ready := &Workspace{
		MCPBindings: []WorkspaceMCPBinding{
			{ID: "a", Enabled: true, DefaultSideEffect: SideEffectRead},
		},
	}
	if !MissionBindingsReady(ready) {
		t.Error("workspace with classified bindings should be ready")
	}

	notReady := &Workspace{
		MCPBindings: []WorkspaceMCPBinding{
			{ID: "a", Enabled: true},
		},
	}
	if MissionBindingsReady(notReady) {
		t.Error("workspace with unclassified binding should not be ready")
	}

	emptyOK := &Workspace{}
	if !MissionBindingsReady(emptyOK) {
		t.Error("workspace with no bindings should be ready (nothing to gate)")
	}

	disabledOK := &Workspace{
		MCPBindings: []WorkspaceMCPBinding{
			{ID: "a", Enabled: false}, // disabled, unclassified is fine
		},
	}
	if !MissionBindingsReady(disabledOK) {
		t.Error("workspace where only-disabled-bindings are unclassified should be ready")
	}
}
