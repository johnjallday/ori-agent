package workspaceplan

import (
	"context"
	"errors"
	"testing"
)

func revisableContent() PlanContent {
	return PlanContent{
		Rationale: "Original rationale",
		InScope:   []string{"reporting"},
		NonGoals:  []string{"billing"},
		Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
		Assumptions: []Assumption{
			{ID: "asm-model", Statement: "Staging mirrors production", Author: AuthorModel},
			{ID: "asm-skip", Statement: "No deadline", Author: AuthorApp, ClarificationID: "clr-1"},
		},
		Groups: []TaskGroup{
			{
				ID: "grp-keep", Title: "Prepare", Author: AuthorModel,
				Items: []TaskItem{
					{ID: "itm-keep", Description: "Snapshot staging", Author: AuthorModel},
					{ID: "itm-mine", Description: "Check the vendor contract", Author: AuthorUser},
				},
			},
			{
				ID: "grp-target", Title: "Cut over", Author: AuthorModel,
				Items: []TaskItem{
					{ID: "itm-target", Description: "Switch traffic", Author: AuthorModel},
				},
			},
		},
	}
}

// seedDraft writes content straight through the store.
//
// Seeding via Edit would be wrong here: Edit marks everything a person saved as
// user-authored, which is correct behavior but would erase the model/user
// authorship these tests are about.
func seedDraft(t *testing.T, ctx context.Context, service *Service, plan *Plan, content PlanContent) *Plan {
	t.Helper()
	if _, err := service.Store().UpdatePlanDraft(ctx, plan.WorkspaceID, plan.ID, plan.DraftRevision, DraftUpdate{
		Title:     plan.Title,
		Objective: "Migrate reporting safely",
		Content:   content,
		UpdatedAt: service.Now(),
	}); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	seeded, err := service.Get(ctx, plan.WorkspaceID, plan.ID)
	if err != nil {
		t.Fatalf("read seeded draft: %v", err)
	}
	return seeded
}

func TestDiscloseRevisionReportsWhatWillBeReplaced(t *testing.T) {
	disclosure := DiscloseRevision(revisableContent(), []string{"grp-keep"})

	if disclosure.Whole {
		t.Error("a targeted revision was reported as a whole-plan revision")
	}
	if len(disclosure.GroupIDs) != 1 || disclosure.GroupIDs[0] != "grp-keep" {
		t.Errorf("group ids = %v", disclosure.GroupIDs)
	}
	if len(disclosure.ReplacedItems) != 2 {
		t.Errorf("replaced items = %d, want 2", len(disclosure.ReplacedItems))
	}
	// Losing a person's own writing is called out separately from losing a
	// model's suggestion (FR-55, FR-57).
	if len(disclosure.UserAuthored) != 1 || disclosure.UserAuthored[0].ID != "itm-mine" {
		t.Errorf("user-authored disclosure = %+v", disclosure.UserAuthored)
	}
	if !disclosure.NeedsConfirmation() {
		t.Error("replacing user-authored content did not require confirmation")
	}
}

// Work outside the target that depends on something inside it is exactly what
// "what else will be regenerated" has to answer (FR-56).
func TestDiscloseRevisionReportsCollateralDependencies(t *testing.T) {
	content := revisableContent()
	content.Groups[1].Items[0].DependsOn = []string{"itm-keep"}

	disclosure := DiscloseRevision(content, []string{"grp-keep"})
	if len(disclosure.Collateral) != 1 || disclosure.Collateral[0].ID != "itm-target" {
		t.Fatalf("collateral = %+v, want the dependent item outside the target", disclosure.Collateral)
	}
	if !disclosure.NeedsConfirmation() {
		t.Error("collateral did not require confirmation")
	}
}

func TestDiscloseRevisionForAWholePlan(t *testing.T) {
	disclosure := DiscloseRevision(revisableContent(), nil)
	if !disclosure.Whole {
		t.Error("an untargeted revision was not reported as a whole-plan revision")
	}
	// A whole revision replaces everything; per-item detail would be noise.
	if len(disclosure.ReplacedItems) != 0 {
		t.Errorf("whole revision listed items: %+v", disclosure.ReplacedItems)
	}
}

func TestDiscloseRevisionIgnoresUnknownTargets(t *testing.T) {
	disclosure := DiscloseRevision(revisableContent(), []string{"grp-nope", "not_a_section"})
	// An unknown target must not silently widen the revision to everything.
	if disclosure.Whole {
		t.Error("an unknown target widened the revision to the whole plan")
	}
	if len(disclosure.GroupIDs) != 0 || len(disclosure.Sections) != 0 {
		t.Errorf("unknown targets were accepted: %+v", disclosure)
	}
}

// The merge is what makes preservation true, regardless of what the model
// returned (FR-55).
func TestMergeTargetedRevisionKeepsUntouchedSections(t *testing.T) {
	current := revisableContent()

	// A model that ignored the instruction and rewrote everything, changing
	// ids as it went.
	generated := PlanContent{
		Rationale: "Rewritten rationale",
		InScope:   []string{"everything"},
		NonGoals:  []string{"nothing"},
		Execution: ExecutionPolicy{Mode: ExecutionAuto},
		Groups: []TaskGroup{
			{ID: "grp-keep", Title: "REWRITTEN", Items: []TaskItem{{ID: "itm-new", Description: "new"}}},
			{ID: "grp-target", Title: "Cut over, revised",
				Items: []TaskItem{{ID: "itm-target-new", Description: "Switch traffic carefully"}}},
		},
	}

	objective, merged := MergeTargetedRevision(
		current, generated, "Original objective", "Rewritten objective", []string{"grp-target"})

	// Only the targeted group changed.
	if objective != "Original objective" {
		t.Errorf("objective = %q, want the untargeted objective preserved", objective)
	}
	if merged.Groups[0].Title != "Prepare" {
		t.Errorf("untargeted group title = %q, want it preserved", merged.Groups[0].Title)
	}
	if merged.Groups[0].Items[0].ID != "itm-keep" || merged.Groups[0].Items[1].ID != "itm-mine" {
		t.Errorf("untargeted ids changed: %+v", merged.Groups[0].Items)
	}
	if merged.Groups[1].Items[0].Description != "Switch traffic carefully" {
		t.Errorf("targeted group was not replaced: %+v", merged.Groups[1])
	}
	// Untargeted scope, non-goals, and execution policy are untouched.
	if merged.Rationale != "Original rationale" || merged.InScope[0] != "reporting" {
		t.Errorf("untargeted scope changed: %q %v", merged.Rationale, merged.InScope)
	}
	if merged.Execution.Mode != ExecutionStepThrough {
		t.Errorf("untargeted execution policy changed to %q", merged.Execution.Mode)
	}
}

func TestMergeReplacesOnlyTheNamedSection(t *testing.T) {
	current := revisableContent()
	generated := PlanContent{
		NonGoals:  []string{"billing", "payroll"},
		InScope:   []string{"ignored"},
		Execution: ExecutionPolicy{Mode: ExecutionAuto},
		Groups:    []TaskGroup{{ID: "grp-keep", Title: "ignored"}},
	}

	_, merged := MergeTargetedRevision(current, generated, "obj", "obj2", []string{SectionNonGoals})

	if len(merged.NonGoals) != 2 {
		t.Errorf("non-goals were not replaced: %v", merged.NonGoals)
	}
	if merged.InScope[0] != "reporting" {
		t.Errorf("scope changed while only non-goals were targeted: %v", merged.InScope)
	}
	if merged.Groups[0].Title != "Prepare" {
		t.Errorf("groups changed while only non-goals were targeted: %q", merged.Groups[0].Title)
	}
}

// An assumption recording a skipped question is the application's record of a
// user's decision, not a model suggestion, so regeneration must not drop it
// (FR-28).
func TestMergePreservesSkipAssumptions(t *testing.T) {
	current := revisableContent()
	generated := PlanContent{
		Assumptions: []Assumption{{ID: "asm-fresh", Statement: "A new assumption"}},
	}

	_, merged := MergeTargetedRevision(current, generated, "obj", "obj", []string{SectionAssumptions})

	var keptSkip, keptNew bool
	for _, assumption := range merged.Assumptions {
		if assumption.ID == "asm-skip" {
			keptSkip = true
		}
		if assumption.ID == "asm-fresh" {
			keptNew = true
		}
	}
	if !keptSkip {
		t.Error("a skip-derived assumption was dropped by regeneration")
	}
	if !keptNew {
		t.Error("the regenerated assumption was not kept")
	}
	// The model's earlier assumption was replaced.
	for _, assumption := range merged.Assumptions {
		if assumption.ID == "asm-model" {
			t.Error("the targeted section was not replaced")
		}
	}
}

// A targeted group the model did not return is left alone. Dropping it would be
// a deletion nobody asked for.
func TestMergeKeepsATargetedGroupTheModelOmitted(t *testing.T) {
	current := revisableContent()
	generated := PlanContent{Groups: []TaskGroup{}}

	_, merged := MergeTargetedRevision(current, generated, "obj", "obj", []string{"grp-target"})

	if len(merged.Groups) != 2 {
		t.Fatalf("groups = %d, want the omitted target preserved", len(merged.Groups))
	}
	if merged.Groups[1].Items[0].ID != "itm-target" {
		t.Errorf("omitted target was replaced: %+v", merged.Groups[1])
	}
}

func TestMergeWholeRevisionReplacesEverything(t *testing.T) {
	generated := PlanContent{
		Execution: ExecutionPolicy{Mode: ExecutionAuto},
		Groups:    []TaskGroup{{ID: "grp-new", Title: "All new"}},
	}

	objective, merged := MergeTargetedRevision(
		revisableContent(), generated, "old", "new objective", nil)

	if objective != "new objective" {
		t.Errorf("objective = %q, want the regenerated one", objective)
	}
	if len(merged.Groups) != 1 || merged.Groups[0].ID != "grp-new" {
		t.Errorf("whole revision did not replace the groups: %+v", merged.Groups)
	}
}

// --- Service integration ---------------------------------------------------

func TestReviseRequiresConfirmationBeforeDiscardingUserWork(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{validDraftResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	seeded := seedDraft(t, ctx, service, plan, revisableContent())

	_, err := service.Revise(ctx, "ws-1", plan.ID, ReviseInput{
		Sections:         []string{"grp-keep"},
		ExpectedRevision: seeded.DraftRevision,
	})

	var required *RevisionRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error = %v, want *RevisionRequiredError", err)
	}
	if len(required.Disclosure.UserAuthored) != 1 {
		t.Errorf("disclosure does not name the user-authored item: %+v", required.Disclosure)
	}
	// Nothing was regenerated: the model was never called.
	if len(model.prompts) != 0 {
		t.Error("the model was called before the user confirmed")
	}
}

func TestReviseProceedsOnceConfirmed(t *testing.T) {
	ctx := context.Background()
	revised := `{
	  "objective": "Migrate reporting safely",
	  "groups": [
	    {"id":"grp-keep","title":"Prepare, revised","items":[{"id":"itm-fresh","description":"Snapshot twice"}]},
	    {"id":"grp-target","title":"Cut over","items":[{"id":"itm-target","description":"Switch traffic"}]}
	  ],
	  "execution": {"mode":"step_through"}
	}`
	model := &scriptedModel{responses: []string{revised}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	seeded := seedDraft(t, ctx, service, plan, revisableContent())

	updated, err := service.Revise(ctx, "ws-1", plan.ID, ReviseInput{
		Sections:         []string{"grp-keep"},
		Confirmed:        true,
		ExpectedRevision: seeded.DraftRevision,
	})
	if err != nil {
		t.Fatalf("revise: %v", err)
	}

	if updated.Draft.Groups[0].Title != "Prepare, revised" {
		t.Errorf("targeted group was not revised: %q", updated.Draft.Groups[0].Title)
	}
	// The untargeted group kept its id and content.
	if updated.Draft.Groups[1].ID != "grp-target" ||
		updated.Draft.Groups[1].Items[0].ID != "itm-target" {
		t.Errorf("untargeted group changed: %+v", updated.Draft.Groups[1])
	}
}

// A revision that replaces only model content with no collateral is
// unremarkable and proceeds without a confirmation round-trip.
func TestReviseNeedsNoConfirmationForModelOnlyContent(t *testing.T) {
	ctx := context.Background()
	revised := `{
	  "objective": "Migrate reporting safely",
	  "groups": [
	    {"id":"grp-keep","title":"Prepare","items":[{"id":"itm-keep","description":"Snapshot staging"},{"id":"itm-mine","description":"Check the vendor contract"}]},
	    {"id":"grp-target","title":"Cut over, revised","items":[{"id":"itm-target","description":"Switch traffic slowly"}]}
	  ],
	  "execution": {"mode":"step_through"}
	}`
	model := &scriptedModel{responses: []string{revised}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	seeded := seedDraft(t, ctx, service, plan, revisableContent())

	updated, err := service.Revise(ctx, "ws-1", plan.ID, ReviseInput{
		Sections:         []string{"grp-target"},
		ExpectedRevision: seeded.DraftRevision,
	})
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if updated.Draft.Groups[1].Items[0].Description != "Switch traffic slowly" {
		t.Errorf("targeted group was not revised: %+v", updated.Draft.Groups[1])
	}
}

// A merge that leaves a dependency pointing at something the regenerated
// section no longer contains is refused, so a broken graph never reaches the
// draft (FR-56).
func TestReviseRefusesAMergeThatBreaksADependency(t *testing.T) {
	ctx := context.Background()
	// The revision drops itm-keep, which the other group depends on.
	revised := `{
	  "objective": "Migrate reporting safely",
	  "groups": [
	    {"id":"grp-keep","title":"Prepare","items":[{"id":"itm-fresh","description":"Something else"}]},
	    {"id":"grp-target","title":"Cut over","items":[{"id":"itm-target","description":"Switch traffic"}]}
	  ],
	  "execution": {"mode":"step_through"}
	}`
	model := &scriptedModel{responses: []string{revised, revised, revised}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	content := revisableContent()
	content.Groups[1].Items[0].DependsOn = []string{"itm-keep"}
	seeded, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective:        "Migrate reporting safely",
		Content:          content,
		ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	_, err = service.Revise(ctx, "ws-1", plan.ID, ReviseInput{
		Sections:         []string{"grp-keep"},
		Confirmed:        true,
		ExpectedRevision: seeded.DraftRevision,
	})
	if err == nil {
		t.Fatal("a revision that broke a dependency was accepted")
	}

	var genErr *GenerationError
	if !errors.As(err, &genErr) {
		t.Fatalf("error = %T, want *GenerationError", err)
	}
	found := false
	for _, issue := range genErr.Issues {
		if issue.Code == IssueDanglingDependency {
			found = true
		}
	}
	if !found {
		t.Errorf("issues do not name the broken dependency: %+v", genErr.Issues)
	}

	// The draft is unchanged.
	after, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.Draft.Groups[0].Items[0].ID != "itm-keep" {
		t.Error("the refused revision was written to the draft anyway")
	}
}

// A mistyped section name must never become a full rewrite of work the user
// meant to keep.
func TestReviseRefusesTargetsThatMatchNothing(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{validDraftResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)
	seeded := seedDraft(t, ctx, service, plan, revisableContent())

	_, err := service.Revise(ctx, "ws-1", plan.ID, ReviseInput{
		Sections:         []string{"grpo-keep", "non_gaols"},
		Confirmed:        true,
		ExpectedRevision: seeded.DraftRevision,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation for targets that match nothing", err)
	}
	if len(model.prompts) != 0 {
		t.Error("a typo'd target reached the model")
	}

	after, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(after.Draft.Groups) != 2 || after.Draft.Groups[0].Title != "Prepare" {
		t.Error("a typo'd target rewrote the plan")
	}
}

func TestReviseRefusesApprovedPlans(t *testing.T) {
	ctx := context.Background()
	service, _ := newDraftingService(t, &scriptedModel{responses: []string{validDraftResponse}})
	plan := mustCreatePlan(t, ctx, service)

	for _, to := range []Status{StatusInReview, StatusApproved} {
		if _, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
			To: to, Source: SourceUser, Actor: "jj",
		}); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}

	_, err := service.Revise(ctx, "ws-1", plan.ID, ReviseInput{Confirmed: true})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("revising an approved plan error = %v, want ErrInvalidTransition", err)
	}
}
