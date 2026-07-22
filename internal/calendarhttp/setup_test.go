package calendarhttp

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// --- fakes -------------------------------------------------------------

// fakeFolderStore.mu guards workspaces: several tests (e.g.
// TestPrepare_ConcurrentRequestsDedupeToOneRun) deliberately fire concurrent
// requests at the same Handler, and Prepare's own resolveGateway (a read)
// and Save (a write) race against each other without it -- caught by the
// race detector on some platforms/schedules but not others, so this isn't
// optional even though a sequential test run never shows it.
type fakeFolderStore struct {
	mu         sync.Mutex
	workspaces map[string]*agentworkspace.Workspace
}

func newFakeFolderStore() *fakeFolderStore {
	return &fakeFolderStore{workspaces: make(map[string]*agentworkspace.Workspace)}
}

func (f *fakeFolderStore) GetFolderWorkspace(id string) (*agentworkspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ws, ok := f.workspaces[id]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found", id)
	}
	return ws, nil
}

func (f *fakeFolderStore) Save(ws *agentworkspace.Workspace) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspaces[ws.ID] = ws
	return nil
}

type fakeWorkspaceLister struct {
	workspaces []session.Workspace
}

func (f *fakeWorkspaceLister) ListWorkspaces(context.Context) ([]session.Workspace, error) {
	return f.workspaces, nil
}

func newCalendarOpsWorkspace(id string) *agentworkspace.Workspace {
	ws := &agentworkspace.Workspace{ID: id, Name: "Calendar Ops"}
	ws.SetTemplateProvenance(&agentworkspace.TemplateProvenance{TemplateID: CalendarOpsTemplateID, Builtin: true})
	ws.AgentInstances = []agentworkspace.AgentInstance{
		{ID: "sched-1", Name: "Scheduler", EntryPoint: true},
		{ID: "prep-1", Name: "Meeting Prep"},
	}
	return ws
}

func googleShapedMappingForTest() agentworkspace.CapabilityMapping {
	return agentworkspace.CapabilityMapping{
		Capability: calendar.CapabilityKey,
		Operations: map[string]agentworkspace.OperationMapping{
			calendar.OpListCalendars: {
				Tool:             "calendars_list",
				ResultCollection: "/items",
				Fields:           map[string]string{"id": "/id", "name": "/summary"},
			},
			calendar.OpListEvents: {
				Tool:             "events_list",
				ResultCollection: "/items",
				Fields: map[string]string{
					"id": "/id", "title": "/summary",
					"start_time": "/start/dateTime", "end_time": "/end/dateTime",
				},
			},
		},
	}
}

// --- loadCalendarOpsWorkspace / findCalendarBinding ---------------------

func TestLoadCalendarOpsWorkspace_NonCalendarOpsIsNilOK(t *testing.T) {
	store := newFakeFolderStore()
	other := &agentworkspace.Workspace{ID: "ws-other", Name: "Not Calendar Ops"}
	store.workspaces["ws-other"] = other
	h := NewHandler(store, &fakeWorkspaceLister{}, nil, nil, nil)

	ws, ok := h.loadCalendarOpsWorkspace(nil, "ws-other") //nolint:staticcheck // w unused on this path
	if !ok {
		t.Fatal("expected ok=true for a workspace that loaded successfully")
	}
	if ws != nil {
		t.Fatalf("expected nil workspace for a non-Calendar-Ops workspace, got: %+v", ws)
	}
}

func TestFindCalendarBinding(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-1")
	if _, ok := findCalendarBinding(ws); ok {
		t.Fatal("expected no calendar binding on a fresh workspace")
	}

	if err := ws.UpsertMCPBinding(agentworkspace.MCPBinding{
		ID:         "binding-1",
		ServerName: "google-calendar",
		Enabled:    true,
		Config:     calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
	}); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}

	binding, ok := findCalendarBinding(ws)
	if !ok || binding.ServerName != "google-calendar" {
		t.Fatalf("expected to find the calendar binding, got: %+v ok=%v", binding, ok)
	}
}

// --- context workspace candidates: ownership enforcement -----------------

func TestContextWorkspaceCandidates_OnlyUserOwnedActiveNonGroup(t *testing.T) {
	lister := &fakeWorkspaceLister{workspaces: []session.Workspace{
		{ID: "ws-mine", Name: "Mine", OwnerUserID: "local", Status: session.WorkspaceStatusActive},
		{ID: "ws-other-user", Name: "Not Mine", OwnerUserID: "someone-else", Status: session.WorkspaceStatusActive},
		{ID: "ws-inactive", Name: "Archived", OwnerUserID: "local", Status: session.WorkspaceStatusTrashed},
		{ID: "ws-group", Name: "A Group", OwnerUserID: "local", Status: session.WorkspaceStatusActive, Kind: session.WorkspaceKindGroup},
		{ID: "ws-calendar-ops", Name: "Calendar Ops", OwnerUserID: "local", Status: session.WorkspaceStatusActive},
		{ID: "ws-blank-owner", Name: "Blank Owner Defaults Local", Status: session.WorkspaceStatusActive},
	}}
	h := NewHandler(newFakeFolderStore(), lister, nil, nil, nil)

	got := h.contextWorkspaceCandidates(context.Background(), "local", "ws-calendar-ops")

	ids := make(map[string]bool, len(got))
	for _, c := range got {
		ids[c.ID] = true
	}
	if !ids["ws-mine"] {
		t.Error("expected the user's own active non-group workspace to be a candidate")
	}
	if !ids["ws-blank-owner"] {
		t.Error("expected a blank OwnerUserID to default to the local user and be a candidate")
	}
	if ids["ws-other-user"] {
		t.Error("expected another user's workspace to be excluded")
	}
	if ids["ws-inactive"] {
		t.Error("expected a non-active workspace to be excluded")
	}
	if ids["ws-group"] {
		t.Error("expected a group workspace to be excluded")
	}
	if ids["ws-calendar-ops"] {
		t.Error("expected the Calendar Ops workspace itself to be excluded")
	}
}

func TestFilterOwnedActiveWorkspaceIDs_DropsUnownedRequestedIDs(t *testing.T) {
	lister := &fakeWorkspaceLister{workspaces: []session.Workspace{
		{ID: "ws-mine", Name: "Mine", OwnerUserID: "local", Status: session.WorkspaceStatusActive},
		{ID: "ws-not-mine", Name: "Not Mine", OwnerUserID: "someone-else", Status: session.WorkspaceStatusActive},
	}}
	h := NewHandler(newFakeFolderStore(), lister, nil, nil, nil)

	got := h.filterOwnedActiveWorkspaceIDs(context.Background(), "local", "ws-calendar-ops", []string{"ws-mine", "ws-not-mine", "ws-does-not-exist"})
	if len(got) != 1 || got[0] != "ws-mine" {
		t.Fatalf("expected only the owned workspace id to survive, got: %v", got)
	}
}

// --- roster access grant --------------------------------------------------

func TestGrantCalendarBindingToRoster_RestrictsNonRosterAgent(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-1")
	ws.AgentInstances = append(ws.AgentInstances, agentworkspace.AgentInstance{ID: "extra-1", Name: "General Assistant"})

	otherBinding := agentworkspace.MCPBinding{ID: "other-binding", ServerName: "filesystem", Enabled: true}
	if err := ws.UpsertMCPBinding(otherBinding); err != nil {
		t.Fatalf("UpsertMCPBinding(other): %v", err)
	}
	calBinding := agentworkspace.MCPBinding{
		ID:         "cal-binding",
		ServerName: "google-calendar",
		Enabled:    true,
		Config:     calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
	}
	if err := ws.UpsertMCPBinding(calBinding); err != nil {
		t.Fatalf("UpsertMCPBinding(calendar): %v", err)
	}

	h := NewHandler(newFakeFolderStore(), &fakeWorkspaceLister{}, nil, nil, nil)
	if err := h.grantCalendarBindingToRoster(ws, "cal-binding"); err != nil {
		t.Fatalf("grantCalendarBindingToRoster: %v", err)
	}

	// Scheduler/Meeting Prep had no access entry -- default "all bindings
	// allowed" already covers the new calendar binding, so no entry should
	// have been forced.
	if _, ok := ws.GetAgentMCPAccess("sched-1"); ok {
		t.Error("expected Scheduler to remain on the default all-bindings-allowed path (no explicit entry needed)")
	}
	if _, ok := ws.GetAgentMCPAccess("prep-1"); ok {
		t.Error("expected Meeting Prep to remain on the default all-bindings-allowed path (no explicit entry needed)")
	}

	// The non-roster agent must get an explicit entry that excludes the
	// calendar binding but preserves access to the other one.
	access, ok := ws.GetAgentMCPAccess("extra-1")
	if !ok {
		t.Fatal("expected an explicit access entry for the non-roster agent")
	}
	for _, id := range access.EnabledBindingIDs {
		if id == "cal-binding" {
			t.Fatal("non-roster agent must not receive the calendar binding")
		}
	}
	found := false
	for _, id := range access.EnabledBindingIDs {
		if id == "other-binding" {
			found = true
		}
	}
	if !found {
		t.Fatal("non-roster agent's existing access to an unrelated binding must be preserved")
	}
}

func TestGrantCalendarBindingToRoster_AddsToExistingRestrictiveRosterEntry(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-1")
	calBinding := agentworkspace.MCPBinding{
		ID:         "cal-binding",
		ServerName: "google-calendar",
		Enabled:    true,
		Config:     calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
	}
	if err := ws.UpsertMCPBinding(calBinding); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}
	// Scheduler already has a restrictive access entry (e.g. from prior setup)
	// that does not yet include the calendar binding.
	if err := ws.SetAgentMCPAccess(agentworkspace.AgentMCPAccess{AgentInstanceID: "sched-1", EnabledBindingIDs: []string{"some-other-binding"}}); err != nil {
		t.Fatalf("SetAgentMCPAccess: %v", err)
	}

	h := NewHandler(newFakeFolderStore(), &fakeWorkspaceLister{}, nil, nil, nil)
	if err := h.grantCalendarBindingToRoster(ws, "cal-binding"); err != nil {
		t.Fatalf("grantCalendarBindingToRoster: %v", err)
	}

	access, ok := ws.GetAgentMCPAccess("sched-1")
	if !ok {
		t.Fatal("expected Scheduler's access entry to still exist")
	}
	foundCal, foundOther := false, false
	for _, id := range access.EnabledBindingIDs {
		if id == "cal-binding" {
			foundCal = true
		}
		if id == "some-other-binding" {
			foundOther = true
		}
	}
	if !foundCal {
		t.Fatal("expected the calendar binding to be added to Scheduler's restrictive access entry")
	}
	if !foundOther {
		t.Fatal("expected Scheduler's existing binding access to be preserved")
	}
}

// --- applySave: read-only allowlist + settings persistence ---------------

func TestApplySave_PersistsReadOnlyAllowlistAndSettings(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	if err := ws.UpsertMCPBinding(agentworkspace.MCPBinding{
		ID:         "cal-binding",
		ServerName: "google-calendar",
		Enabled:    true,
		Config:     calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
	}); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}

	store := newFakeFolderStore()
	store.workspaces["ws-cal"] = ws
	lister := &fakeWorkspaceLister{workspaces: []session.Workspace{
		{ID: "ws-notes", Name: "My Notes", OwnerUserID: "local", Status: session.WorkspaceStatusActive},
		{ID: "ws-not-mine", Name: "Not Mine", OwnerUserID: "someone-else", Status: session.WorkspaceStatusActive},
	}}
	h := NewHandler(store, lister, nil, nil, nil)

	req := saveRequest{
		WorkspaceID:         "ws-cal",
		Mapping:             googleShapedMappingForTest(),
		SelectedCalendarIDs: []string{"primary"},
		DisplayTimeZone:     "America/New_York",
		ContextWorkspaceIDs: []string{"ws-notes", "ws-not-mine"},
	}
	if err := h.applySave(context.Background(), ws, "local", req); err != nil {
		t.Fatalf("applySave: %v", err)
	}

	binding, ok := findCalendarBinding(ws)
	if !ok {
		t.Fatal("expected the calendar binding to still exist")
	}
	wantTools := []string{"calendars_list", "events_list"}
	if len(binding.AllowedTools) != len(wantTools) {
		t.Fatalf("AllowedTools = %v, want %v", binding.AllowedTools, wantTools)
	}
	for _, tool := range binding.AllowedTools {
		if tool != "calendars_list" && tool != "events_list" {
			t.Fatalf("unexpected tool in read-only allowlist: %q (full: %v)", tool, binding.AllowedTools)
		}
	}

	settings := calendar.ReadBindingSettings(binding.Config)
	if !settings.Validated {
		t.Fatal("expected settings.Validated to be true after save")
	}
	if len(settings.SelectedCalendarIDs) != 1 || settings.SelectedCalendarIDs[0] != "primary" {
		t.Fatalf("SelectedCalendarIDs = %v", settings.SelectedCalendarIDs)
	}
	if settings.DisplayTimeZone != "America/New_York" {
		t.Fatalf("DisplayTimeZone = %q", settings.DisplayTimeZone)
	}
	if len(settings.ContextWorkspaceIDs) != 1 || settings.ContextWorkspaceIDs[0] != "ws-notes" {
		t.Fatalf("expected only the owned context workspace to persist, got: %v", settings.ContextWorkspaceIDs)
	}
}

func TestApplySave_ClassifiesToolSideEffects(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	if err := ws.UpsertMCPBinding(agentworkspace.MCPBinding{
		ID:         "cal-binding",
		ServerName: "google-calendar",
		Enabled:    true,
		Config:     calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
	}); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}

	mapping := googleShapedMappingForTest()
	mapping.Operations[calendar.OpCreateEvent] = agentworkspace.OperationMapping{
		Tool:      "events_insert",
		Arguments: map[string]string{"calendar_id": "/calendarId", "title": "/summary", "start_time": "/start/dateTime", "end_time": "/end/dateTime"},
	}

	h := NewHandler(newFakeFolderStore(), &fakeWorkspaceLister{}, nil, nil, nil)
	if err := h.applySave(context.Background(), ws, "local", saveRequest{WorkspaceID: "ws-cal", Mapping: mapping}); err != nil {
		t.Fatalf("applySave: %v", err)
	}

	binding, ok := findCalendarBinding(ws)
	if !ok {
		t.Fatal("expected the calendar binding to still exist")
	}
	if binding.DefaultSideEffect != agentworkspace.SideEffectRead {
		t.Fatalf("DefaultSideEffect = %q, want %q", binding.DefaultSideEffect, agentworkspace.SideEffectRead)
	}
	if got := binding.ToolOverrides["calendars_list"]; got != agentworkspace.SideEffectRead {
		t.Errorf("ToolOverrides[calendars_list] = %q, want read", got)
	}
	if got := binding.ToolOverrides["events_list"]; got != agentworkspace.SideEffectRead {
		t.Errorf("ToolOverrides[events_list] = %q, want read", got)
	}
	if got := binding.ToolOverrides["events_insert"]; got != agentworkspace.SideEffectExternal {
		t.Errorf("ToolOverrides[events_insert] = %q, want external", got)
	}
	// The write tool must never appear in AllowedTools, even though it's
	// classified -- classification and exposure are separate defenses.
	for _, tool := range binding.AllowedTools {
		if tool == "events_insert" {
			t.Fatal("create_event's tool must not be in AllowedTools")
		}
	}
}

func TestApplySave_RejectsInvalidMapping(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	if err := ws.UpsertMCPBinding(agentworkspace.MCPBinding{
		ID:         "cal-binding",
		ServerName: "google-calendar",
		Enabled:    true,
		Config:     calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
	}); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}
	h := NewHandler(newFakeFolderStore(), &fakeWorkspaceLister{}, nil, nil, nil)

	// Missing list_events -- a required operation.
	incomplete := agentworkspace.CapabilityMapping{
		Capability: calendar.CapabilityKey,
		Operations: map[string]agentworkspace.OperationMapping{
			calendar.OpListCalendars: {Tool: "calendars_list", Fields: map[string]string{"id": "/id", "name": "/summary"}},
		},
	}
	err := h.applySave(context.Background(), ws, "local", saveRequest{WorkspaceID: "ws-cal", Mapping: incomplete})
	if err == nil {
		t.Fatal("expected an error for a mapping missing a required operation")
	}
}

func TestApplySave_NoBindingIsError(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	h := NewHandler(newFakeFolderStore(), &fakeWorkspaceLister{}, nil, nil, nil)
	err := h.applySave(context.Background(), ws, "local", saveRequest{WorkspaceID: "ws-cal", Mapping: googleShapedMappingForTest()})
	if err == nil {
		t.Fatal("expected an error when no calendar connector has been selected yet")
	}
}

// --- setup state derivation through buildStateResponse --------------------

func TestBuildStateResponse_NoBindingIsConnectorMissing(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	h := NewHandler(newFakeFolderStore(), &fakeWorkspaceLister{}, nil, nil, nil)
	resp := h.buildStateResponse(context.Background(), ws, "local")
	if resp.State != calendar.SetupConnectorMissing {
		t.Fatalf("state = %q, want connector_missing", resp.State)
	}
	if resp.Binding != nil {
		t.Fatalf("expected no binding view, got: %+v", resp.Binding)
	}
}

func TestBuildStateResponse_BindingWithoutConnectorStatusFnIsConnectorMissing(t *testing.T) {
	// A nil registry (h.connectorStatusFn falls back to the zero-value
	// connectorStatus{}, i.e. Present=false) must surface as connector_missing
	// even though a binding row exists -- a stale/removed server should not
	// look "ready".
	ws := newCalendarOpsWorkspace("ws-cal")
	if err := ws.UpsertMCPBinding(agentworkspace.MCPBinding{
		ID:         "cal-binding",
		ServerName: "google-calendar",
		Enabled:    true,
		Config:     calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
	}); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}
	h := NewHandler(newFakeFolderStore(), &fakeWorkspaceLister{}, nil, nil, nil)
	resp := h.buildStateResponse(context.Background(), ws, "local")
	if resp.State != calendar.SetupConnectorMissing {
		t.Fatalf("state = %q, want connector_missing", resp.State)
	}
}

func TestBuildStateResponse_ReadyAfterSave(t *testing.T) {
	ws := newCalendarOpsWorkspace("ws-cal")
	if err := ws.UpsertMCPBinding(agentworkspace.MCPBinding{
		ID:         "cal-binding",
		ServerName: "google-calendar",
		Enabled:    true,
		Config:     calendar.WriteBindingSettings(nil, calendar.BindingSettings{}),
	}); err != nil {
		t.Fatalf("UpsertMCPBinding: %v", err)
	}
	h := NewHandler(newFakeFolderStore(), &fakeWorkspaceLister{}, nil, nil, nil)
	h.WithConnectorStatusFn(func(serverName string) connectorStatus {
		return connectorStatus{Present: true, Connected: true}
	})

	if err := h.applySave(context.Background(), ws, "local", saveRequest{
		WorkspaceID: "ws-cal",
		Mapping:     googleShapedMappingForTest(),
	}); err != nil {
		t.Fatalf("applySave: %v", err)
	}

	resp := h.buildStateResponse(context.Background(), ws, "local")
	if resp.State != calendar.SetupReady {
		t.Fatalf("state = %q, want ready", resp.State)
	}
	if resp.Binding == nil || !resp.Binding.MappingValid {
		t.Fatalf("expected a valid binding view, got: %+v", resp.Binding)
	}
}
