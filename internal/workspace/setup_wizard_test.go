package workspace

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidSetupStepKinds_IsTheVersion1Allowlist(t *testing.T) {
	want := []string{
		"directory",
		"automation_review",
		"capability_connect",
		"capability_configure",
		"account_link",
		"plugin_readiness",
		"readiness",
		"runtime_mode",
		"runtime_readiness",
		"summary",
	}
	got := ValidSetupStepKinds()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("version 1 kind allowlist changed:\n got %v\nwant %v", got, want)
	}
	if SetupWizardSchemaVersion != 1 {
		t.Fatalf("SetupWizardSchemaVersion = %d, want 1", SetupWizardSchemaVersion)
	}
}

func TestValidSetupStepKinds_ReturnsACopy(t *testing.T) {
	kinds := ValidSetupStepKinds()
	kinds[0] = "mutated"
	if ValidSetupStepKinds()[0] != SetupStepKindDirectory {
		t.Fatal("ValidSetupStepKinds must not expose the internal allowlist")
	}
}

func TestLookupSetupStepKind_NormalizesAndFailsClosed(t *testing.T) {
	for _, raw := range []string{"directory", "  Directory  ", "DIRECTORY"} {
		spec, ok := LookupSetupStepKind(raw)
		if !ok {
			t.Fatalf("LookupSetupStepKind(%q) should resolve", raw)
		}
		if spec.Kind != SetupStepKindDirectory {
			t.Fatalf("LookupSetupStepKind(%q).Kind = %q", raw, spec.Kind)
		}
	}
	// Anything not on the allowlist resolves to nothing: no prefix matching, no
	// near-miss guessing, no empty-string wildcard.
	for _, raw := range []string{"", "   ", "dir", "directory2", "custom", "shell", "readinesss"} {
		if _, ok := LookupSetupStepKind(raw); ok {
			t.Fatalf("LookupSetupStepKind(%q) must fail closed", raw)
		}
	}
}

func TestSetupStepKindSpecs_ReferenceAndAdapterRules(t *testing.T) {
	cases := []struct {
		kind      string
		scope     SetupStepReferenceScope
		reference bool
		adapter   bool
	}{
		{SetupStepKindDirectory, SetupStepReferenceDirectory, true, false},
		{SetupStepKindAutomationReview, SetupStepReferenceDirectory, true, false},
		{SetupStepKindCapabilityConnect, SetupStepReferenceCapability, true, true},
		{SetupStepKindCapabilityConfigure, SetupStepReferenceCapability, true, true},
		{SetupStepKindAccountLink, SetupStepReferenceCapability, true, true},
		{SetupStepKindPluginReadiness, SetupStepReferencePlugin, true, true},
		{SetupStepKindReadiness, SetupStepReferenceNone, false, true},
		{SetupStepKindRuntimeMode, SetupStepReferenceNone, false, false},
		{SetupStepKindRuntimeReadiness, SetupStepReferenceRuntimeRequirement, true, false},
		{SetupStepKindSummary, SetupStepReferenceNone, false, false},
	}
	if len(cases) != len(ValidSetupStepKinds()) {
		t.Fatalf("every allowlisted kind needs a reference rule: %d rules for %d kinds", len(cases), len(ValidSetupStepKinds()))
	}
	for _, tc := range cases {
		spec, ok := LookupSetupStepKind(tc.kind)
		if !ok {
			t.Fatalf("kind %q is not on the allowlist", tc.kind)
		}
		if spec.ReferenceScope != tc.scope {
			t.Errorf("kind %q scope = %q, want %q", tc.kind, spec.ReferenceScope, tc.scope)
		}
		if spec.RequiresReference != tc.reference {
			t.Errorf("kind %q RequiresReference = %v, want %v", tc.kind, spec.RequiresReference, tc.reference)
		}
		if spec.RequiresAdapter != tc.adapter {
			t.Errorf("kind %q RequiresAdapter = %v, want %v", tc.kind, spec.RequiresAdapter, tc.adapter)
		}
		// A kind that takes no reference must not require one, or no valid
		// declaration of it could exist.
		if spec.ReferenceScope == SetupStepReferenceNone && spec.RequiresReference {
			t.Errorf("kind %q requires a reference but declares no scope to resolve it in", tc.kind)
		}
	}
}

func TestSetupWizardStep_ReferenceScopeComesFromKind(t *testing.T) {
	step := SetupWizardStep{ID: "folder", Kind: "  Directory  ", RequirementKey: "  Downloads-Root  "}
	ref, ok := step.Reference()
	if !ok {
		t.Fatal("a directory step with a key should resolve a reference")
	}
	if ref.Scope != SetupStepReferenceDirectory || ref.Key != "downloads-root" {
		t.Fatalf("reference = %+v, want {directory downloads-root}", ref)
	}

	// automation_review resolves in the directory namespace: its recipe is keyed
	// by the directory it automates.
	ref, ok = SetupWizardStep{Kind: SetupStepKindAutomationReview, RequirementKey: "downloads-root"}.Reference()
	if !ok || ref.Scope != SetupStepReferenceDirectory {
		t.Fatalf("automation_review reference = %+v, ok=%v; want directory scope", ref, ok)
	}

	ref, ok = SetupWizardStep{Kind: SetupStepKindAccountLink, RequirementKey: "email"}.Reference()
	if !ok || ref.Scope != SetupStepReferenceCapability || ref.Key != "email" {
		t.Fatalf("account_link reference = %+v, ok=%v; want capability/email", ref, ok)
	}

	ref, ok = SetupWizardStep{Kind: SetupStepKindPluginReadiness, RequirementKey: "reaper-plugin"}.Reference()
	if !ok || ref.Scope != SetupStepReferencePlugin {
		t.Fatalf("plugin_readiness reference = %+v, ok=%v; want plugin scope", ref, ok)
	}

	ref, ok = SetupWizardStep{Kind: SetupStepKindRuntimeReadiness, RequirementKey: " REAPER_LIVE_CONTROL "}.Reference()
	if !ok || ref.Scope != SetupStepReferenceRuntimeRequirement || ref.Key != "reaper_live_control" {
		t.Fatalf("runtime_readiness reference = %+v, ok=%v; want runtime requirement scope", ref, ok)
	}
}

func TestSetupWizardStep_ReferenceRefusesToGuess(t *testing.T) {
	// An unknown kind carrying a key resolves to nothing rather than defaulting
	// to a namespace — a hand-edited kind must not reach any requirement.
	if _, ok := (SetupWizardStep{Kind: "shell", RequirementKey: "downloads-root"}).Reference(); ok {
		t.Fatal("an unknown kind must resolve no reference")
	}
	// Kinds that take no reference ignore a stray key.
	for _, kind := range []string{SetupStepKindReadiness, SetupStepKindRuntimeMode, SetupStepKindSummary} {
		if _, ok := (SetupWizardStep{Kind: kind, RequirementKey: "downloads-root"}).Reference(); ok {
			t.Fatalf("kind %q must resolve no reference", kind)
		}
	}
	// A blank key is no reference, not an empty-key reference.
	if _, ok := (SetupWizardStep{Kind: SetupStepKindDirectory, RequirementKey: "   "}).Reference(); ok {
		t.Fatal("a blank requirement_key must resolve no reference")
	}
}

func TestSetupWizard_JSONRoundTrip(t *testing.T) {
	const raw = `{
	  "version": 1,
	  "title": "Set up Downloads Janitor",
	  "steps": [
	    {"id": "folder", "kind": "directory", "requirement_key": "downloads-root", "required": true},
	    {"id": "automation", "kind": "automation_review", "requirement_key": "downloads-root", "required": true,
	     "title": "Review automation", "description": "What runs after setup", "disclosure": "Watches for new files."},
	    {"id": "readiness", "kind": "readiness", "adapter": "downloads_janitor", "required": true},
	    {"id": "summary", "kind": "summary", "required": false}
	  ]
	}`
	var wizard SetupWizard
	if err := json.Unmarshal([]byte(raw), &wizard); err != nil {
		t.Fatal(err)
	}
	if wizard.Version != SetupWizardSchemaVersion || wizard.Title != "Set up Downloads Janitor" {
		t.Fatalf("wizard header did not parse: %+v", wizard)
	}
	if len(wizard.Steps) != 4 {
		t.Fatalf("got %d steps, want 4", len(wizard.Steps))
	}
	if wizard.Steps[1].Disclosure != "Watches for new files." {
		t.Fatalf("disclosure did not parse: %+v", wizard.Steps[1])
	}
	if wizard.Steps[2].Adapter != "downloads_janitor" {
		t.Fatalf("adapter did not parse: %+v", wizard.Steps[2])
	}

	data, err := json.Marshal(wizard)
	if err != nil {
		t.Fatal(err)
	}
	var back SetupWizard
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wizard, back) {
		t.Fatalf("round-trip changed the wizard:\n got %+v\nwant %+v", back, wizard)
	}
	// `required` is never omitted: an optional step must serialize as an
	// explicit false, so a persisted snapshot cannot be read as "unstated".
	if !strings.Contains(string(data), `"id":"summary","kind":"summary","required":false`) {
		t.Fatalf("required must always serialize explicitly, got %s", data)
	}
}

func TestSetupWizard_UnknownManifestFieldsAreDropped(t *testing.T) {
	// A hand-edited manifest can carry anything; none of it reaches the typed
	// snapshot, because the struct has nowhere to put it.
	const hostile = `{
	  "version": 1,
	  "title": "Hostile",
	  "steps": [{
	    "id": "folder", "kind": "directory", "requirement_key": "downloads-root", "required": true,
	    "component_url": "https://example.test/step.js",
	    "render_html": "<script>alert(1)</script>",
	    "command": "rm -rf /",
	    "api_endpoint": "/api/workspaces/1/delete",
	    "handler": "internal/evil"
	  }]
	}`
	var wizard SetupWizard
	if err := json.Unmarshal([]byte(hostile), &wizard); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(wizard)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"component_url", "render_html", "command", "api_endpoint", "handler", "example.test", "rm -rf", "<script>"} {
		if strings.Contains(string(data), leaked) {
			t.Fatalf("hostile manifest field %q survived into the snapshot: %s", leaked, data)
		}
	}
}

// TestSetupWizardTypes_CarryNoExecutableFields freezes the wizard's field
// surface. It fails on *any* added field, which is the point: a new field is a
// deliberate, reviewed widening of what a blueprint author can put in a
// manifest, and the one class that must never be added is anything naming code,
// markup, or a network destination.
func TestSetupWizardTypes_CarryNoExecutableFields(t *testing.T) {
	cases := []struct {
		typ  reflect.Type
		tags []string
	}{
		{reflect.TypeFor[SetupWizard](), []string{"version", "title", "steps"}},
		{reflect.TypeFor[SetupWizardStep](), []string{
			"id", "kind", "required", "title", "description", "disclosure", "requirement_key", "adapter",
		}},
	}
	// Matched per underscore-separated word, not as substrings, so a plain
	// "description" is not mistaken for a "script".
	banned := map[string]bool{
		"html": true, "markup": true, "script": true, "command": true, "cmd": true,
		"shell": true, "exec": true, "url": true, "uri": true, "endpoint": true,
		"module": true, "handler": true, "component": true, "path": true,
		"payload": true, "header": true, "method": true,
	}

	for _, tc := range cases {
		var got []string
		for i := range tc.typ.NumField() {
			field := tc.typ.Field(i)
			tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if tag == "-" || tag == "" {
				t.Fatalf("%s.%s has no JSON tag; every persisted field must name itself", tc.typ.Name(), field.Name)
			}
			got = append(got, tag)

			for word := range strings.SplitSeq(strings.ToLower(tag), "_") {
				if banned[word] {
					t.Errorf("%s.%s (%q) names %q: a manifest must never select code, markup, or a network destination", tc.typ.Name(), field.Name, tag, word)
				}
			}
			switch field.Type.Kind() {
			case reflect.String, reflect.Int, reflect.Bool:
			case reflect.Slice:
				if field.Type.Elem() != reflect.TypeFor[SetupWizardStep]() {
					t.Errorf("%s.%s is a slice of %s; only []SetupWizardStep is allowed", tc.typ.Name(), field.Name, field.Type.Elem())
				}
			default:
				t.Errorf("%s.%s is a %s; wizard fields must be plain scalars so nothing arbitrary can be smuggled through JSON", tc.typ.Name(), field.Name, field.Type.Kind())
			}
		}
		if !reflect.DeepEqual(got, tc.tags) {
			t.Errorf("%s field set changed:\n got %v\nwant %v", tc.typ.Name(), got, tc.tags)
		}
	}
}

func TestNormalizeSetupWizardState_UnknownIsNeverReady(t *testing.T) {
	for raw, want := range map[string]string{
		"":                  SetupWizardStateNotStarted,
		"  not_started  ":   SetupWizardStateNotStarted,
		"NOT_APPLICABLE":    SetupWizardStateNotApplicable,
		"ready":             SetupWizardStateReady,
		"needs_attention":   SetupWizardStateNeedsAttention,
		"in_progress":       SetupWizardStateInProgress,
		"Ready!":            SetupWizardStateInProgress,
		"complete":          SetupWizardStateInProgress,
		"totally_finished":  SetupWizardStateInProgress,
		"ready_for_reals":   SetupWizardStateInProgress,
		"  READY  ":         SetupWizardStateReady,
		"needs-attention":   SetupWizardStateInProgress,
		"\tnot_started\r\n": SetupWizardStateNotStarted,
	} {
		if got := NormalizeSetupWizardState(raw); got != want {
			t.Errorf("NormalizeSetupWizardState(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizeSetupStepStatus_UnknownIsNeverComplete(t *testing.T) {
	for raw, want := range map[string]string{
		"":                 SetupStepStatusPending,
		"pending":          SetupStepStatusPending,
		" Active ":         SetupStepStatusActive,
		"COMPLETE":         SetupStepStatusComplete,
		"blocked":          SetupStepStatusBlocked,
		"optional_skipped": SetupStepStatusOptionalSkipped,
		"done":             SetupStepStatusPending,
		"completed":        SetupStepStatusPending,
		"skipped":          SetupStepStatusPending,
	} {
		if got := NormalizeSetupStepStatus(raw); got != want {
			t.Errorf("NormalizeSetupStepStatus(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestWorkspace_SetupWizardProgressPersistsAndIsCopied(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Downloads"})
	if ws.GetSetupWizardProgress() != nil {
		t.Fatal("a new workspace has recorded no setup progress")
	}

	opened := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	completedStep := opened.Add(time.Minute)
	progress := &SetupWizardProgress{
		WizardVersion: 1,
		State:         SetupWizardStateInProgress,
		CurrentStepID: "automation",
		Steps: []SetupStepProgress{
			{StepID: "folder", Status: SetupStepStatusComplete, CompletedAt: &completedStep},
			{StepID: "automation", Status: SetupStepStatusActive},
		},
		FirstOpenedAt: &opened,
	}
	ws.SetSetupWizardProgress(progress)

	// The caller's record is not the stored one — including the time values its
	// pointers reach.
	progress.State = SetupWizardStateReady
	progress.Steps[1].Status = SetupStepStatusComplete
	*progress.FirstOpenedAt = opened.Add(time.Hour)
	wantOpened := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)

	stored := ws.GetSetupWizardProgress()
	if stored.State != SetupWizardStateInProgress || stored.StepStatus("automation") != SetupStepStatusActive {
		t.Fatalf("mutating the caller's record changed stored progress: %+v", stored)
	}
	if !stored.FirstOpenedAt.Equal(wantOpened) {
		t.Fatalf("timestamps were shared, not copied: %v", stored.FirstOpenedAt)
	}
	if stored.CreatedAt.IsZero() || stored.UpdatedAt.IsZero() {
		t.Fatalf("created/updated timestamps must be stamped: %+v", stored)
	}

	// Reads are copies too.
	stored.Steps[0].Status = SetupStepStatusPending
	if ws.GetSetupWizardProgress().StepStatus("folder") != SetupStepStatusComplete {
		t.Fatal("GetSetupWizardProgress handed out the stored record")
	}

	if got := ws.GetSetupWizardProgress().StepStatus("never-declared"); got != SetupStepStatusPending {
		t.Fatalf("an unrecorded step is pending, got %q", got)
	}
	ws.SetSetupWizardProgress(nil)
	if ws.GetSetupWizardProgress() != nil {
		t.Fatal("nil should clear setup progress")
	}
}

func TestWorkspace_SetupWizardStateNeedsBothSnapshotAndProgress(t *testing.T) {
	// Without a wizard snapshot the workspace is not_applicable — even if a
	// stray progress record claims otherwise, which is what a copied or
	// hand-edited workspace.json looks like.
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Plain"})
	ws.SetSetupWizardProgress(&SetupWizardProgress{WizardVersion: 1, State: SetupWizardStateReady})
	if got := ws.SetupWizardState(); got != SetupWizardStateNotApplicable {
		t.Fatalf("no wizard means not_applicable, got %q", got)
	}
	if ws.IsSetupWizardReady() {
		t.Fatal("a workspace with no wizard is not 'ready'")
	}

	ws.SetTemplateProvenance(&TemplateProvenance{
		TemplateID: "downloads-janitor",
		SetupWizard: &SetupWizard{Version: 1, Title: "Setup", Steps: []SetupWizardStep{
			{ID: "folder", Kind: SetupStepKindDirectory, RequirementKey: "downloads-root", Required: true},
		}},
	})
	if got := ws.SetupWizardState(); got != SetupWizardStateReady {
		t.Fatalf("with a snapshot the recorded state applies, got %q", got)
	}

	ws.SetSetupWizardProgress(nil)
	if got := ws.SetupWizardState(); got != SetupWizardStateNotStarted {
		t.Fatalf("a wizard with no progress is not_started, got %q", got)
	}

	// A persisted state this build cannot read must not present as ready.
	ws.SetSetupWizardProgress(&SetupWizardProgress{WizardVersion: 1, State: "all-good"})
	if got := ws.SetupWizardState(); got != SetupWizardStateInProgress {
		t.Fatalf("an unreadable state must degrade to in_progress, got %q", got)
	}
}

func TestWorkspace_DismissalIsRecordedSeparatelyFromReadiness(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Downloads"})
	ws.SetTemplateProvenance(&TemplateProvenance{
		TemplateID: "downloads-janitor",
		SetupWizard: &SetupWizard{Version: 1, Title: "Setup", Steps: []SetupWizardStep{
			{ID: "folder", Kind: SetupStepKindDirectory, RequirementKey: "downloads-root", Required: true},
		}},
	})

	dismissed := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	opened := dismissed.Add(-time.Minute)
	ws.SetSetupWizardProgress(&SetupWizardProgress{
		WizardVersion: 1,
		State:         SetupWizardStateInProgress,
		CurrentStepID: "folder",
		Steps:         []SetupStepProgress{{StepID: "folder", Status: SetupStepStatusActive}},
		FirstOpenedAt: &opened,
		DismissedAt:   &dismissed,
	})

	progress := ws.GetSetupWizardProgress()
	if !progress.IsDismissed() || !progress.HasBeenOpened() {
		t.Fatalf("dismissal/open must be recorded: %+v", progress)
	}
	// The whole point: closing the dialog did not make the workspace ready.
	if ws.IsSetupWizardReady() || ws.SetupWizardState() != SetupWizardStateInProgress {
		t.Fatalf("dismissal must not affect readiness, state = %q", ws.SetupWizardState())
	}
	if progress.CompletedAt != nil {
		t.Fatal("dismissal must not record completion")
	}
}

func TestWorkspace_SetupProgressSurvivesJSONRoundTrip(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Downloads"})
	completed := time.Date(2026, 7, 28, 11, 0, 0, 0, time.UTC)
	ws.SetSetupWizardProgress(&SetupWizardProgress{
		WizardVersion: 1,
		State:         SetupWizardStateNeedsAttention,
		CurrentStepID: "readiness",
		Steps: []SetupStepProgress{
			{StepID: "folder", Status: SetupStepStatusComplete, CompletedAt: &completed},
			{StepID: "content", Status: SetupStepStatusOptionalSkipped},
			{StepID: "readiness", Status: SetupStepStatusBlocked},
		},
		FirstOpenedAt: &completed,
		CompletedAt:   &completed,
	})

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	var back Workspace
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	got := back.GetSetupWizardProgress()
	if got == nil || got.State != SetupWizardStateNeedsAttention || got.CurrentStepID != "readiness" {
		t.Fatalf("progress lost in workspace.json: %+v", got)
	}
	if got.StepStatus("folder") != SetupStepStatusComplete || got.StepStatus("content") != SetupStepStatusOptionalSkipped || got.StepStatus("readiness") != SetupStepStatusBlocked {
		t.Fatalf("per-step status lost in the round-trip: %+v", got.Steps)
	}
	if got.CompletedAt == nil || !got.CompletedAt.Equal(completed) {
		t.Fatalf("completion timestamp lost in the round-trip: %v", got.CompletedAt)
	}
	// Completion survives a regression to needs_attention: repair must not look
	// like a first-time completion later.
	if got.State == SetupWizardStateReady {
		t.Fatal("a regressed workspace must not read as ready")
	}
}

func TestCloneSetupWizard_DeepCopiesSteps(t *testing.T) {
	if CloneSetupWizard(nil) != nil {
		t.Fatal("cloning nil must yield nil")
	}
	original := &SetupWizard{
		Version: 1,
		Title:   "Set up Downloads Janitor",
		Steps: []SetupWizardStep{
			{ID: "folder", Kind: SetupStepKindDirectory, RequirementKey: "downloads-root", Required: true},
			{ID: "readiness", Kind: SetupStepKindReadiness, Adapter: "downloads_janitor", Required: true},
		},
	}
	clone := CloneSetupWizard(original)
	if !reflect.DeepEqual(original, clone) {
		t.Fatalf("clone differs from original:\n got %+v\nwant %+v", clone, original)
	}
	clone.Title = "mutated"
	clone.Steps[0].RequirementKey = "/etc"
	clone.Steps[1].Adapter = "evil"
	if original.Title != "Set up Downloads Janitor" || original.Steps[0].RequirementKey != "downloads-root" || original.Steps[1].Adapter != "downloads_janitor" {
		t.Fatalf("mutating a clone changed the original: %+v", original)
	}

	empty := CloneSetupWizard(&SetupWizard{Version: 1, Title: "Empty"})
	if empty.Steps != nil {
		t.Fatal("an empty step list should clone to nil, not an empty slice")
	}
}

func TestSetupWizard_StepLookupAndRequiredIDs(t *testing.T) {
	var missing *SetupWizard
	if _, ok := missing.Step("folder"); ok {
		t.Fatal("a nil wizard declares no steps")
	}
	if missing.RequiredStepIDs() != nil || !missing.IsEmpty() {
		t.Fatal("a nil wizard is empty and requires nothing")
	}

	wizard := &SetupWizard{Version: 1, Title: "Setup", Steps: []SetupWizardStep{
		{ID: "folder", Kind: SetupStepKindDirectory, RequirementKey: "downloads-root", Required: true},
		{ID: "content", Kind: SetupStepKindSummary},
		{ID: "readiness", Kind: SetupStepKindReadiness, Adapter: "downloads_janitor", Required: true},
	}}
	if wizard.IsEmpty() {
		t.Fatal("a wizard with steps is not empty")
	}
	step, ok := wizard.Step("readiness")
	if !ok || step.Adapter != "downloads_janitor" {
		t.Fatalf("Step(readiness) = %+v, ok=%v", step, ok)
	}
	if _, ok := wizard.Step("nope"); ok {
		t.Fatal("Step must not resolve an undeclared ID")
	}
	if _, ok := wizard.Step("  "); ok {
		t.Fatal("Step must not resolve a blank ID")
	}
	if got := wizard.RequiredStepIDs(); !reflect.DeepEqual(got, []string{"folder", "readiness"}) {
		t.Fatalf("RequiredStepIDs = %v, want [folder readiness]", got)
	}
	if !(&SetupWizard{Version: 1, Title: "Setup"}).IsEmpty() {
		t.Fatal("a wizard declaring no steps is empty")
	}
}
