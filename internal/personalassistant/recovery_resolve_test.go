package personalassistant

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
)

// fakeRecoveryRecords applies each fix to the same stubs the coordinator reads,
// so the re-evaluation after a fix sees exactly what a real store would.
type fakeRecoveryRecords struct {
	profiles   *recoveryProfilesStub
	workspaces *recoveryWorkspacesStub
	hq         *recoveryHQStub
	known      map[string]*session.Workspace
	finder     map[string]RecoveryProfile
	briefs     *recoveryBriefStub
	calls      []string
}

func (f *fakeRecoveryRecords) SetPersonalHQMarker(_ context.Context, id, assistantID, requestID string) error {
	f.calls = append(f.calls, "set_hq_marker:"+id)
	marker := map[string]any{"assistant_id": assistantID, "request_id": requestID, "version": 1}
	found := false
	for i := range f.workspaces.workspaces {
		if f.workspaces.workspaces[i].ID == id {
			w := &f.workspaces.workspaces[i]
			w.AssistantID, w.HQRequestID, w.PresentationValid = assistantID, requestID, true
			found = true
		}
	}
	ws := f.known[id]
	if !found && ws != nil {
		evidence := RecoveryWorkspace{ID: id, Name: ws.Name, OwnerUserID: ws.OwnerUserID,
			AssistantID: assistantID, HQRequestID: requestID, PresentationValid: true}
		for _, instance := range ws.AgentInstances {
			if instance.EntryPoint {
				evidence.EntryAgents = append(evidence.EntryAgents, RecoveryEntryAgent{ID: instance.ID, Name: instance.Name})
			}
		}
		f.workspaces.workspaces = append(f.workspaces.workspaces, evidence)
	}
	if ws != nil {
		ws.SharedData["personal_assistant_presentation"] = marker
	}
	return nil
}

func (f *fakeRecoveryRecords) ClearPersonalHQMarker(_ context.Context, id string) error {
	f.calls = append(f.calls, "clear_hq_marker:"+id)
	kept := f.workspaces.workspaces[:0]
	for _, w := range f.workspaces.workspaces {
		if w.ID != id {
			kept = append(kept, w)
		}
	}
	f.workspaces.workspaces = kept
	if ws := f.known[id]; ws != nil {
		delete(ws.SharedData, "personal_assistant_presentation")
	}
	return nil
}

func (f *fakeRecoveryRecords) SetProfileMarkers(name, assistantID, hireRequestID string) error {
	f.calls = append(f.calls, "set_profile_markers:"+name)
	for i := range f.profiles.profiles {
		if f.profiles.profiles[i].Name == name {
			f.profiles.profiles[i].AssistantID, f.profiles.profiles[i].HireRequestID = assistantID, hireRequestID
			return nil
		}
	}
	profile := f.finder[name]
	profile.AssistantID, profile.HireRequestID = assistantID, hireRequestID
	f.profiles.profiles = append(f.profiles.profiles, profile)
	return nil
}

func (f *fakeRecoveryRecords) ClearProfileMarkers(name string) error {
	f.calls = append(f.calls, "clear_profile_markers:"+name)
	kept := f.profiles.profiles[:0]
	for _, p := range f.profiles.profiles {
		if p.Name != name {
			kept = append(kept, p)
		}
	}
	f.profiles.profiles = kept
	return nil
}

func (f *fakeRecoveryRecords) Replace(_ context.Context, userID, workspaceID string) (*personalhq.Status, error) {
	f.calls = append(f.calls, "designate:"+workspaceID)
	ws := f.known[workspaceID]
	status := &personalhq.Status{UserID: userID, WorkspaceID: workspaceID, Workspace: ws, Valid: ws != nil}
	for _, instance := range ws.AgentInstances {
		if instance.EntryPoint {
			status.EntryAgentInstanceID, status.EntryAgentName = instance.ID, instance.Name
		}
	}
	f.hq.status = status
	return status, nil
}

func (f *fakeRecoveryRecords) Clear(_ context.Context, userID string) (*personalhq.Status, error) {
	f.calls = append(f.calls, "clear_designation")
	f.hq.status = &personalhq.Status{UserID: userID}
	return f.hq.status, nil
}

func (f *fakeRecoveryRecords) GetConfig(ctx context.Context, id string) (*dailybrief.Config, error) {
	return f.briefs.GetConfig(ctx, id)
}

func (f *fakeRecoveryRecords) UpdateConfig(_ context.Context, cfg dailybrief.Config) (*dailybrief.Config, error) {
	f.calls = append(f.calls, "create_brief:"+cfg.WorkspaceID)
	stored := cfg
	f.briefs.config, f.briefs.err = &stored, nil
	return &stored, nil
}

func (f *fakeRecoveryRecords) PersonalAssistantRecoveryProfileByName(name string) (RecoveryProfile, bool) {
	profile, ok := f.finder[name]
	return profile, ok
}

// resolverFixture is recoveryFixture with fixes enabled and a second,
// undesignated HQ-shaped workspace ("hq-b") the tests can mark.
func resolverFixture(t *testing.T) (*RecoveryCoordinator, *SQLiteStore, *fakeRecoveryRecords) {
	t.Helper()
	coordinator, store, profiles, workspaces, hq, briefs := recoveryFixture(t)
	hq.status.Workspace.Name = "My HQ"
	workspaces.workspaces[0].Name = "My HQ"
	other := &session.Workspace{
		ID: "hq-b", Name: "Old HQ", OwnerUserID: "local",
		AgentInstances: []session.AgentInstance{{ID: "instance-b", Name: "Assistant", EntryPoint: true}},
		SharedData:     map[string]any{},
	}
	records := &fakeRecoveryRecords{
		profiles: profiles, workspaces: workspaces, hq: hq, briefs: briefs,
		known:  map[string]*session.Workspace{"hq-a": hq.status.Workspace, "hq-b": other},
		finder: map[string]RecoveryProfile{"Assistant": {Name: "Assistant", Role: types.RoleOrchestrator}},
	}
	coordinator.WithResolver(records, records, records, records)
	coordinator.newID = func() string { return "generated-id" }
	return coordinator, store, records
}

func diagnoseAndApply(t *testing.T, coordinator *RecoveryCoordinator, fixID string) *RecoveryResolution {
	t.Helper()
	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	result, err := coordinator.Resolve(context.Background(), "local", fixID, diagnosis.Digest)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", fixID, err)
	}
	return result
}

func fixIDs(fixes []RecoveryFix) []string {
	out := []string{}
	for _, fix := range fixes {
		out = append(out, fix.ID)
	}
	return out
}

// The reported case: the HQ was built for an earlier hire, so its marker names
// a different assistant than the profile that exists now.
func TestRecoveryDiagnoseExplainsAnHQKeptFromAnEarlierHire(t *testing.T) {
	coordinator, store, records := resolverFixture(t)
	records.workspaces.workspaces[0].AssistantID = "assistant-old"
	records.known["hq-a"].SharedData["personal_assistant_presentation"] = map[string]any{
		"assistant_id": "assistant-old", "request_id": "hq-request-a",
	}

	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diagnosis.Issue != RecoveryIssueAssistantMismatch {
		t.Fatalf("issue = %q; want assistant_mismatch", diagnosis.Issue)
	}
	if len(diagnosis.Profiles) != 1 || diagnosis.Profiles[0].Name != "Assistant" || diagnosis.Profiles[0].AssistantID != "assistant-a" {
		t.Fatalf("profiles = %#v", diagnosis.Profiles)
	}
	if len(diagnosis.HQs) != 1 || diagnosis.HQs[0].AssistantID != "assistant-old" || !diagnosis.HQs[0].Designated ||
		!reflect.DeepEqual(diagnosis.HQs[0].EntryAgents, []string{"Assistant"}) {
		t.Fatalf("hqs = %#v", diagnosis.HQs)
	}
	want := []RecoveryFix{{
		ID: "link_hq:hq-a", Kind: RecoveryFixLinkHQ, ProfileName: "Assistant",
		WorkspaceID: "hq-a", WorkspaceName: "My HQ", Recommended: true,
	}}
	if !reflect.DeepEqual(diagnosis.Fixes, want) {
		t.Fatalf("fixes = %#v", diagnosis.Fixes)
	}
	if len(records.calls) != 0 {
		t.Fatalf("diagnosis wrote: %v", records.calls)
	}

	result, err := coordinator.Resolve(context.Background(), "local", "link_hq:hq-a", diagnosis.Digest)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.State == nil || result.Diagnosis != nil || result.Applied != RecoveryFixLinkHQ {
		t.Fatalf("result = %#v", result)
	}
	if result.State.Status != StatusPaused || result.State.AssistantID != "assistant-a" ||
		result.State.HQWorkspaceID != "hq-a" || result.State.LastHQRequestID != "hq-request-a" {
		t.Fatalf("reconnected state = %#v", result.State)
	}
	if !reflect.DeepEqual(records.calls, []string{"set_hq_marker:hq-a"}) {
		t.Fatalf("writes = %v", records.calls)
	}
	if _, err := store.GetState(context.Background(), "local"); err != nil {
		t.Fatalf("relationship not persisted: %v", err)
	}
}

func TestRecoveryResolveRefusesRecordsThatChangedSinceReview(t *testing.T) {
	coordinator, store, records := resolverFixture(t)
	records.workspaces.workspaces[0].AssistantID = "assistant-old"
	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	records.workspaces.workspaces[0].AssistantID = "assistant-other"

	if _, err := coordinator.Resolve(context.Background(), "local", "link_hq:hq-a", diagnosis.Digest); !errors.Is(err, ErrConflict) {
		t.Fatalf("Resolve error = %v; want ErrConflict", err)
	}
	if len(records.calls) != 0 {
		t.Fatalf("stale fix wrote: %v", records.calls)
	}
	if _, err := store.GetState(context.Background(), "local"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale fix created a relationship: %v", err)
	}
}

func TestRecoveryResolveRefusesAFixThatIsNotOffered(t *testing.T) {
	coordinator, _, records := resolverFixture(t)
	records.workspaces.workspaces[0].AssistantID = "assistant-old"
	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	for _, fixID := range []string{"link_hq:hq-b", "clear_designation", "keep_profile:Assistant", ""} {
		if _, err := coordinator.Resolve(context.Background(), "local", fixID, diagnosis.Digest); !errors.Is(err, ErrValidation) {
			t.Fatalf("Resolve(%q) error = %v; want ErrValidation", fixID, err)
		}
	}
	if len(records.calls) != 0 {
		t.Fatalf("refused fixes wrote: %v", records.calls)
	}
}

func TestRecoveryResolveKeepsTheChosenProfileAndUnmarksTheOthers(t *testing.T) {
	coordinator, _, records := resolverFixture(t)
	records.profiles.profiles = append(records.profiles.profiles, RecoveryProfile{
		Name: "Atlas", AssistantID: "assistant-z", HireRequestID: "hire-z", Role: types.RoleOrchestrator,
	})

	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diagnosis.Issue != RecoveryIssueProfileDuplicate {
		t.Fatalf("issue = %q", diagnosis.Issue)
	}
	if got := fixIDs(diagnosis.Fixes); !reflect.DeepEqual(got, []string{"keep_profile:Assistant", "keep_profile:Atlas"}) {
		t.Fatalf("fixes = %v", got)
	}
	if !diagnosis.Fixes[0].Recommended || diagnosis.Fixes[1].Recommended {
		t.Fatalf("the profile the HQ names should be recommended: %#v", diagnosis.Fixes)
	}

	result := diagnoseAndApply(t, coordinator, "keep_profile:Assistant")
	if result.State == nil || result.State.GlobalAgentProfileName != "Assistant" {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(records.calls, []string{"clear_profile_markers:Atlas"}) {
		t.Fatalf("writes = %v", records.calls)
	}
}

func TestRecoveryResolveKeepsTheDesignatedHQAndUnmarksTheOther(t *testing.T) {
	coordinator, _, records := resolverFixture(t)
	records.workspaces.workspaces = append(records.workspaces.workspaces, RecoveryWorkspace{
		ID: "hq-b", Name: "Old HQ", OwnerUserID: "local", AssistantID: "assistant-a", HQRequestID: "hq-request-b",
		PresentationValid: true, EntryAgents: []RecoveryEntryAgent{{ID: "instance-b", Name: "Assistant"}},
	})

	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diagnosis.Issue != RecoveryIssueHQDuplicate {
		t.Fatalf("issue = %q", diagnosis.Issue)
	}
	if got := fixIDs(diagnosis.Fixes); !reflect.DeepEqual(got, []string{"keep_hq:hq-a", "keep_hq:hq-b"}) {
		t.Fatalf("fixes = %v", got)
	}
	if !diagnosis.Fixes[0].Recommended || diagnosis.Fixes[1].Recommended {
		t.Fatalf("the designated HQ should be recommended: %#v", diagnosis.Fixes)
	}

	result := diagnoseAndApply(t, coordinator, "keep_hq:hq-a")
	if result.State == nil || result.State.HQWorkspaceID != "hq-a" {
		t.Fatalf("result = %#v", result)
	}
	// Already designated: only the other marker is removed.
	if !reflect.DeepEqual(records.calls, []string{"clear_hq_marker:hq-b"}) {
		t.Fatalf("writes = %v", records.calls)
	}
}

func TestRecoveryResolveDesignatesTheMarkedHQ(t *testing.T) {
	coordinator, _, records := resolverFixture(t)
	records.hq.status = &personalhq.Status{UserID: "local"}

	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diagnosis.Issue != RecoveryIssueDesignationMismatch || !reflect.DeepEqual(fixIDs(diagnosis.Fixes), []string{"designate_hq:hq-a"}) {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}
	result := diagnoseAndApply(t, coordinator, "designate_hq:hq-a")
	if result.State == nil || result.State.HQWorkspaceID != "hq-a" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRecoveryResolveCreatesMissingBriefSettingsWithTheScheduleOff(t *testing.T) {
	coordinator, _, records := resolverFixture(t)
	records.briefs.config, records.briefs.err = nil, dailybrief.ErrConfigNotFound

	result := diagnoseAndApply(t, coordinator, "create_brief:hq-a")
	if result.State == nil {
		t.Fatalf("result = %#v", result)
	}
	config := records.briefs.config
	if config == nil || config.WorkspaceID != "hq-a" || config.UserID != "local" || config.ScheduleEnabled {
		t.Fatalf("brief settings = %#v", config)
	}
}

func TestRecoveryResolveAdoptsTheHQLeadWhenNoProfileIsMarked(t *testing.T) {
	coordinator, _, records := resolverFixture(t)
	records.profiles.profiles = nil

	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diagnosis.Issue != RecoveryIssueProfileMissing ||
		!reflect.DeepEqual(fixIDs(diagnosis.Fixes), []string{"adopt_entry_profile:hq-a"}) ||
		diagnosis.Fixes[0].ProfileName != "Assistant" {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}
	result := diagnoseAndApply(t, coordinator, "adopt_entry_profile:hq-a")
	if result.State == nil || result.State.AssistantID != "assistant-a" || result.State.LastHireRequestID != "generated-id" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRecoveryResolveOffersNoLeadThatIsNotAnOrchestrator(t *testing.T) {
	coordinator, _, records := resolverFixture(t)
	records.profiles.profiles = nil
	records.finder["Assistant"] = RecoveryProfile{Name: "Assistant", Role: types.RoleResearcher}

	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diagnosis.Issue != RecoveryIssueProfileMissing || len(diagnosis.Fixes) != 0 {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}
}

// One fix at a time: after the first, the next disagreement is explained and
// nothing is reconnected until the records agree.
func TestRecoveryResolveStepsThroughSuccessiveIssues(t *testing.T) {
	coordinator, store, records := resolverFixture(t)
	records.workspaces.workspaces[0].AssistantID = "assistant-old"
	records.known["hq-a"].SharedData["personal_assistant_presentation"] = map[string]any{
		"assistant_id": "assistant-old", "request_id": "hq-request-a",
	}
	records.briefs.config, records.briefs.err = nil, dailybrief.ErrConfigNotFound

	first := diagnoseAndApply(t, coordinator, "link_hq:hq-a")
	if first.State != nil || first.Diagnosis == nil || first.Diagnosis.Issue != RecoveryIssueBriefMissing {
		t.Fatalf("first fix = %#v", first)
	}
	if _, err := store.GetState(context.Background(), "local"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reconnected before the records agreed: %v", err)
	}
	second, err := coordinator.Resolve(context.Background(), "local", "create_brief:hq-a", first.Diagnosis.Digest)
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if second.State == nil || second.State.AssistantID != "assistant-a" {
		t.Fatalf("second fix = %#v", second)
	}
}

func TestRecoveryDiagnoseLinksOrClearsADesignationWithoutAnHQMarker(t *testing.T) {
	coordinator, _, records := resolverFixture(t)
	records.workspaces.workspaces = nil
	delete(records.known["hq-a"].SharedData, "personal_assistant_presentation")

	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diagnosis.Issue != RecoveryIssueDesignationWithoutHQ {
		t.Fatalf("issue = %q", diagnosis.Issue)
	}
	if got := fixIDs(diagnosis.Fixes); !reflect.DeepEqual(got, []string{"link_hq:hq-a", "clear_designation"}) {
		t.Fatalf("fixes = %v", got)
	}
	result := diagnoseAndApply(t, coordinator, "link_hq:hq-a")
	if result.State == nil || result.State.HQWorkspaceID != "hq-a" || result.State.LastHQRequestID != "generated-id" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRecoveryDiagnoseOffersNoFixWhereNoneIsSafe(t *testing.T) {
	tests := []struct {
		name   string
		issue  RecoveryIssue
		mutate func(*fakeRecoveryRecords)
	}{
		{"hq owned by someone else", RecoveryIssueHQForeignOwner, func(r *fakeRecoveryRecords) {
			r.workspaces.workspaces[0].OwnerUserID = "other-user"
		}},
		{"hq led by another agent", RecoveryIssueEntryMismatch, func(r *fakeRecoveryRecords) {
			r.workspaces.workspaces[0].EntryAgents[0].Name = "Someone else"
		}},
		{"assistant is not an orchestrator", RecoveryIssueProfileRole, func(r *fakeRecoveryRecords) {
			r.profiles.profiles[0].Role = types.RoleResearcher
		}},
		{"brief settings belong to someone else", RecoveryIssueBriefMismatch, func(r *fakeRecoveryRecords) {
			r.briefs.config.UserID = "other-user"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator, _, records := resolverFixture(t)
			test.mutate(records)
			diagnosis, err := coordinator.Diagnose(context.Background(), "local")
			if err != nil {
				t.Fatalf("Diagnose: %v", err)
			}
			if diagnosis.Issue != test.issue || len(diagnosis.Fixes) != 0 {
				t.Fatalf("diagnosis = %#v", diagnosis)
			}
		})
	}
}

func TestRecoveryDiagnoseNeedsAMissingRelationshipAndSomeEvidence(t *testing.T) {
	coordinator, _, records := resolverFixture(t)
	if _, err := coordinator.Repair(context.Background(), "local", 0); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if _, err := coordinator.Diagnose(context.Background(), "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Diagnose with a relationship = %v; want ErrConflict", err)
	}

	fresh, _, freshRecords := resolverFixture(t)
	freshRecords.profiles.profiles = nil
	freshRecords.workspaces.workspaces = nil
	if _, err := fresh.Diagnose(context.Background(), "local"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Diagnose with no evidence = %v; want ErrNotFound", err)
	}
	_ = records
}

func TestRecoveryDiagnosisWithoutAResolverExplainsButOffersNothing(t *testing.T) {
	coordinator, _, _, workspaces, _, _ := recoveryFixture(t)
	workspaces.workspaces[0].AssistantID = "assistant-old"
	diagnosis, err := coordinator.Diagnose(context.Background(), "local")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diagnosis.Issue != RecoveryIssueAssistantMismatch || len(diagnosis.Fixes) != 0 {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}
	if _, err := coordinator.Resolve(context.Background(), "local", "link_hq:hq-a", diagnosis.Digest); err == nil {
		t.Fatal("Resolve without a resolver succeeded")
	}
}

func TestWithoutProfileMarkersKeepsTheUsersOwnTags(t *testing.T) {
	got := WithoutProfileMarkers([]string{
		"music", ProfileAssistantMarker("a"), "team", ProfileHireMarker("h"), " " + ProfileAssistantMarker("b"),
	})
	if !reflect.DeepEqual(got, []string{"music", "team"}) {
		t.Fatalf("tags = %v", got)
	}
}
