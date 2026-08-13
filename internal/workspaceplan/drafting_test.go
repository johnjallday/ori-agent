package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func newDraftingService(t *testing.T, model PlanModel) (*Service, Store) {
	t.Helper()
	store := NewMemoryStore()
	service := NewService(store, WithGenerator(NewGenerator(model)))
	return service, store
}

func TestDraftWritesGeneratedContentForAClearRequest(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{validDraftResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	drafted, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{Actor: "jj"})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if drafted.Status != StatusDraft {
		t.Errorf("status = %q, want draft", drafted.Status)
	}
	if drafted.Objective != "Migrate reporting safely" {
		t.Errorf("objective = %q", drafted.Objective)
	}
	if len(drafted.Draft.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(drafted.Draft.Groups))
	}
	// Drafting creates no work and authorizes nothing (FR-20).
	if drafted.ApprovedVersion != 0 || len(drafted.TaskLinks) != 0 {
		t.Error("drafting produced approved work")
	}
	// The initiating request is never rewritten by drafting (FR-21).
	if drafted.OriginalRequest != "Plan the migration" {
		t.Errorf("original request = %q", drafted.OriginalRequest)
	}
}

// When information is missing the planner asks instead of guessing, and the
// Plan waits in needs_input (FR-23).
func TestDraftEntersNeedsInputWithStructuredQuestions(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{clarificationResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	waiting, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{
		Actor:              "jj",
		AllowClarification: true,
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if waiting.Status != StatusNeedsInput {
		t.Fatalf("status = %q, want needs_input", waiting.Status)
	}
	if len(waiting.Draft.Clarifications) != 2 {
		t.Fatalf("questions = %d, want 2", len(waiting.Draft.Clarifications))
	}

	question := waiting.Draft.Clarifications[0]
	if question.ID == "" || question.Prompt == "" || question.CreatedAt.IsZero() {
		t.Errorf("question is missing required fields (FR-24): %+v", question)
	}
	if question.Status != ClarificationOpen {
		t.Errorf("status = %q, want open", question.Status)
	}
	if question.Round != 1 {
		t.Errorf("round = %d, want 1", question.Round)
	}
}

// Answering the last required question returns the Plan to draft so it can be
// regenerated or edited (FR-26).
func TestAnsweringAllRequiredQuestionsReturnsToDraft(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{clarificationResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	waiting, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{AllowClarification: true})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}

	var requiredID, optionalID string
	for _, question := range waiting.Draft.Clarifications {
		if question.Required {
			requiredID = question.ID
		} else {
			optionalID = question.ID
		}
	}
	if requiredID == "" || optionalID == "" {
		t.Fatalf("fixture needs one required and one optional question: %+v", waiting.Draft.Clarifications)
	}

	// Answering the optional one first leaves the Plan waiting.
	stillWaiting, err := service.Answer(ctx, "ws-1", plan.ID, optionalID, AnswerInput{
		Answer: "No deadline", Actor: "jj",
	})
	if err != nil {
		t.Fatalf("answer optional: %v", err)
	}
	if stillWaiting.Status != StatusNeedsInput {
		t.Errorf("status = %q, want needs_input while a required question is open", stillWaiting.Status)
	}

	released, err := service.Answer(ctx, "ws-1", plan.ID, requiredID, AnswerInput{
		Answer: "Staging only, never production", Actor: "jj",
	})
	if err != nil {
		t.Fatalf("answer required: %v", err)
	}
	if released.Status != StatusDraft {
		t.Errorf("status = %q, want draft once every required question is answered", released.Status)
	}
}

// Skipping an optional question records the assumption it implies, so the user
// can see what was assumed on their behalf (FR-28).
func TestSkippingAnOptionalQuestionRecordsAnAssumption(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{clarificationResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	waiting, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{AllowClarification: true})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	var optionalID string
	for _, question := range waiting.Draft.Clarifications {
		if !question.Required {
			optionalID = question.ID
		}
	}

	skipped, err := service.Answer(ctx, "ws-1", plan.ID, optionalID, AnswerInput{
		Skip: true, SkipReason: "no deadline pressure", Actor: "jj",
	})
	if err != nil {
		t.Fatalf("skip: %v", err)
	}

	question, _ := skipped.Draft.Clarification(optionalID)
	if question.Status != ClarificationSkipped {
		t.Errorf("status = %q, want skipped", question.Status)
	}
	if len(skipped.Draft.Assumptions) != 1 {
		t.Fatalf("assumptions = %d, want 1 recorded for the skip", len(skipped.Draft.Assumptions))
	}
	assumption := skipped.Draft.Assumptions[0]
	if assumption.ClarificationID != optionalID {
		t.Errorf("assumption is not linked to the skipped question: %+v", assumption)
	}
	if !strings.Contains(assumption.Statement, "no deadline pressure") {
		t.Errorf("assumption does not carry the user's reason: %q", assumption.Statement)
	}

	// Re-skipping must not stack duplicates.
	again, err := service.Answer(ctx, "ws-1", plan.ID, optionalID, AnswerInput{
		Skip: true, SkipReason: "still none", Actor: "jj",
	})
	if err != nil {
		t.Fatalf("re-skip: %v", err)
	}
	if len(again.Draft.Assumptions) != 1 {
		t.Errorf("assumptions = %d after re-skipping, want 1", len(again.Draft.Assumptions))
	}
}

// A required question cannot be skipped: the plan genuinely cannot be drafted
// without it (FR-28).
func TestRequiredQuestionsCannotBeSkipped(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{clarificationResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	waiting, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{AllowClarification: true})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	var requiredID string
	for _, question := range waiting.Draft.Clarifications {
		if question.Required {
			requiredID = question.ID
		}
	}

	_, err = service.Answer(ctx, "ws-1", plan.ID, requiredID, AnswerInput{Skip: true, Actor: "jj"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("skipping a required question error = %v, want ErrValidation", err)
	}
}

func TestEmptyAnswersAreRefused(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{clarificationResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	waiting, _ := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{AllowClarification: true})
	id := waiting.Draft.Clarifications[0].ID

	if _, err := service.Answer(ctx, "ws-1", plan.ID, id, AnswerInput{Answer: "   "}); !errors.Is(err, ErrValidation) {
		t.Errorf("empty answer error = %v, want ErrValidation", err)
	}
}

// Regenerating after answers must carry them to the planner and must not start
// another clarification round (FR-25, FR-26).
func TestRegenerationAfterAnswersDraftsRatherThanReasking(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{clarificationResponse, validDraftResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	waiting, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{AllowClarification: true})
	if err != nil {
		t.Fatalf("first draft: %v", err)
	}
	for _, question := range waiting.Draft.Clarifications {
		if _, err := service.Answer(ctx, "ws-1", plan.ID, question.ID, AnswerInput{
			Answer: "Staging only", Actor: "jj",
		}); err != nil {
			t.Fatalf("answer: %v", err)
		}
	}

	drafted, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{
		AllowClarification: true,
		ExpectedRevision:   0,
	})
	if err != nil {
		t.Fatalf("second draft: %v", err)
	}
	if drafted.Status != StatusDraft {
		t.Errorf("status = %q, want draft", drafted.Status)
	}
	if len(model.responses) != 0 {
		t.Errorf("planner did not consume the draft response; it likely re-asked")
	}

	// The authored answers reached the planner as authoritative context.
	lastPrompt := model.prompts[len(model.prompts)-1]
	if !strings.Contains(lastPrompt, "Staging only") || !strings.Contains(lastPrompt, "do not re-ask") {
		t.Errorf("draft prompt did not carry the authored answers:\n%s", lastPrompt)
	}
}

// An approved or executing Plan is never regenerated in place: revising
// approved work starts a new draft and needs fresh approval (FR-38).
func TestDraftRefusesToRegenerateApprovedWork(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{validDraftResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	for _, to := range []Status{StatusInReview, StatusApproved} {
		if _, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
			To: to, Source: SourceUser, Actor: "jj",
		}); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}

	_, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{Actor: "jj"})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("regenerating approved work error = %v, want ErrInvalidTransition", err)
	}
}

// Generation failing after the repair budget leaves the Plan a draft carrying
// the issues, rather than accepting unvalidated content (FR-45).
func TestDraftKeepsThePlanADraftWhenGenerationCannotValidate(t *testing.T) {
	ctx := context.Background()
	invalid := `{"objective":"","groups":[]}`
	model := &scriptedModel{responses: []string{invalid, invalid, invalid}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	_, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{Actor: "jj"})
	if err == nil {
		t.Fatal("invalid generation was accepted")
	}

	var genErr *GenerationError
	if !errors.As(err, &genErr) {
		t.Fatalf("error = %T, want *GenerationError carrying the issues", err)
	}
	if len(genErr.Issues) == 0 {
		t.Error("generation error carries no issues to show the user")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("error does not unwrap to ErrValidation: %v", err)
	}

	after, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.Status != StatusDraft {
		t.Errorf("status = %q, want the plan left as a draft", after.Status)
	}
	if len(after.Draft.Groups) != 0 {
		t.Error("invalid content was written into the draft")
	}
}

// Everything that does not need a model keeps working without one (FR-58).
func TestPlanRemainsEditableWithoutAModel(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	service := NewService(store) // no generator
	plan := mustCreatePlan(t, ctx, service)

	if service.GeneratorAvailable() {
		t.Error("a service with no generator reports generation available")
	}
	if _, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{}); !errors.Is(err, ErrModelUnavailable) {
		t.Errorf("draft error = %v, want ErrModelUnavailable", err)
	}

	// Editing, reviewing, and approving all still work.
	edited, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective: "Hand-written objective",
		Content: PlanContent{
			Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
			Groups: []TaskGroup{{
				ID: "grp-1", Title: "Do it",
				Items: []TaskItem{{ID: "itm-1", Description: "Write it by hand"}},
			}},
		},
		Actor:            "jj",
		ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatalf("edit without a model: %v", err)
	}
	if edited.Objective != "Hand-written objective" {
		t.Errorf("objective = %q", edited.Objective)
	}
	if _, err := service.Transition(ctx, "ws-1", plan.ID, TransitionInput{
		To: StatusInReview, Source: SourceUser, Actor: "jj",
	}); err != nil {
		t.Errorf("review without a model: %v", err)
	}
}

// A stale editor loses the race rather than overwriting a newer save (FR-30).
func TestEditRefusesAStaleRevision(t *testing.T) {
	ctx := context.Background()
	service, _ := newDraftingService(t, &scriptedModel{})
	plan := mustCreatePlan(t, ctx, service)

	if _, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective: "First", Actor: "jj", ExpectedRevision: 0,
	}); err != nil {
		t.Fatalf("first edit: %v", err)
	}
	_, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective: "Second", Actor: "other", ExpectedRevision: 0,
	})
	if !errors.Is(err, ErrStaleDraft) {
		t.Fatalf("stale edit error = %v, want ErrStaleDraft", err)
	}
}

// Editing marks changed content as user-authored while leaving untouched
// model-authored content alone (FR-57).
func TestEditMarksUserAuthoredContent(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{responses: []string{validDraftResponse}}
	service, _ := newDraftingService(t, model)
	plan := mustCreatePlan(t, ctx, service)

	drafted, err := service.Draft(ctx, "ws-1", plan.ID, DraftingOptions{Actor: "jj"})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if drafted.Draft.Groups[0].Items[0].Author != AuthorModel {
		t.Fatalf("generated item author = %q, want model", drafted.Draft.Groups[0].Items[0].Author)
	}

	edited := drafted.Draft.Clone()
	edited.Groups[0].Items[0].Description = "Snapshot staging, twice"
	saved, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective:        drafted.Objective,
		Content:          edited,
		Actor:            "jj",
		ExpectedRevision: drafted.DraftRevision,
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got := saved.Draft.Groups[0].Items[0].Author; got != AuthorUser {
		t.Errorf("edited item author = %q, want user", got)
	}
	// The untouched group keeps its model attribution.
	if got := saved.Draft.Groups[0].Author; got != AuthorModel {
		t.Errorf("untouched group author = %q, want model", got)
	}
}

// A broken dependency graph is not a work-in-progress state; it is refused even
// while drafting.
func TestEditRefusesABrokenDependencyGraph(t *testing.T) {
	ctx := context.Background()
	service, _ := newDraftingService(t, &scriptedModel{})
	plan := mustCreatePlan(t, ctx, service)

	_, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Content: PlanContent{
			Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
			Groups: []TaskGroup{{
				ID: "grp-1", Title: "Do it",
				Items: []TaskItem{{ID: "itm-1", Description: "x", DependsOn: []string{"itm-missing"}}},
			}},
		},
		ExpectedRevision: 0,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("broken graph error = %v, want ErrValidation", err)
	}
}

// An incomplete draft is allowed: only moving to review needs a complete plan.
func TestEditAllowsAnIncompleteDraft(t *testing.T) {
	ctx := context.Background()
	service, _ := newDraftingService(t, &scriptedModel{})
	plan := mustCreatePlan(t, ctx, service)

	if _, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective:        "",
		Content:          PlanContent{Execution: ExecutionPolicy{Mode: ExecutionStepThrough}},
		ExpectedRevision: 0,
	}); err != nil {
		t.Fatalf("an empty work-in-progress draft was refused: %v", err)
	}
}

// A partial edit must not silently reset policy the user did not touch. An
// unspecified execution mode means "leave it as it is".
func TestPartialEditPreservesExecutionPolicy(t *testing.T) {
	ctx := context.Background()
	service, _ := newDraftingService(t, &scriptedModel{})
	plan := mustCreatePlan(t, ctx, service)

	withAuto, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective: "Run it automatically",
		Content: PlanContent{
			Execution: ExecutionPolicy{Mode: ExecutionAuto},
			Groups: []TaskGroup{{
				ID: "grp-1", Title: "Do it",
				Items: []TaskItem{{ID: "itm-1", Description: "work"}},
			}},
		},
		ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatalf("first edit: %v", err)
	}
	if withAuto.Draft.Execution.Mode != ExecutionAuto {
		t.Fatalf("execution mode = %q, want auto", withAuto.Draft.Execution.Mode)
	}

	// A later edit that says nothing about execution keeps auto rather than
	// resetting it or failing validation.
	renamed := withAuto.Draft.Clone()
	renamed.Execution.Mode = ""
	updated, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective:        "Run it automatically, renamed",
		Content:          renamed,
		ExpectedRevision: withAuto.DraftRevision,
	})
	if err != nil {
		t.Fatalf("partial edit: %v", err)
	}
	if updated.Draft.Execution.Mode != ExecutionAuto {
		t.Errorf("execution mode = %q after a partial edit, want auto preserved", updated.Draft.Execution.Mode)
	}
}

// --- Autosave recovery (FR-30) --------------------------------------------

func TestAutosaveSnapshotsAreRetainedAndRecoverable(t *testing.T) {
	ctx := context.Background()
	service, store := newDraftingService(t, &scriptedModel{})
	plan := mustCreatePlan(t, ctx, service)

	current, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective: "First objective", ExpectedRevision: 0, Snapshot: true,
	})
	if err != nil {
		t.Fatalf("first edit: %v", err)
	}
	if _, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
		Objective: "Second objective", ExpectedRevision: current.DraftRevision, Snapshot: true,
	}); err != nil {
		t.Fatalf("second edit: %v", err)
	}

	snapshots, err := service.Snapshots(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snapshots))
	}

	// Recovering restores the captured content and is itself undoable.
	recovered, err := service.RecoverSnapshot(ctx, "ws-1", plan.ID, snapshots[0].ID, "jj")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered.Objective != "First objective" {
		t.Errorf("recovered objective = %q, want the snapshot's content", recovered.Objective)
	}

	after, err := service.Snapshots(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("snapshots after recovery: %v", err)
	}
	if len(after) != 3 {
		t.Errorf("snapshots after recovery = %d, want 3 (recovery is undoable)", len(after))
	}

	// Recovery is recorded in the Plan's history.
	activity, err := store.ListActivity(ctx, "ws-1", plan.ID, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	found := false
	for _, entry := range activity {
		if entry.Kind == ActivityDraftRecovered {
			found = true
		}
	}
	if !found {
		t.Error("recovery was not recorded in the plan's history")
	}
}

func TestAutosaveSnapshotsArePrunedToTheRetainedCount(t *testing.T) {
	ctx := context.Background()
	service, _ := newDraftingService(t, &scriptedModel{})
	plan := mustCreatePlan(t, ctx, service)

	revision := int64(0)
	for i := range MaxDraftSnapshots + 4 {
		updated, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
			Objective:        strings.Repeat("edit ", i+1),
			ExpectedRevision: revision,
			Snapshot:         true,
		})
		if err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
		revision = updated.DraftRevision
	}

	snapshots, err := service.Snapshots(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	if len(snapshots) != MaxDraftSnapshots {
		t.Errorf("snapshots = %d, want the retained %d", len(snapshots), MaxDraftSnapshots)
	}
}

// Two sessions autosaving the same draft: exactly one write wins per revision,
// and the losers are told their view is stale rather than silently overwriting
// each other (FR-30).
func TestConcurrentAutosavesResolveToOneWinnerPerRevision(t *testing.T) {
	ctx := context.Background()
	service, _ := newDraftingService(t, &scriptedModel{})
	plan := mustCreatePlan(t, ctx, service)

	const editors = 8
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		won    int
		stale  int
		others []error
	)

	wg.Add(editors)
	for i := range editors {
		go func(i int) {
			defer wg.Done()
			_, err := service.Edit(ctx, "ws-1", plan.ID, EditInput{
				Objective:        fmt.Sprintf("Objective from editor %d", i),
				ExpectedRevision: 0, // every session loaded revision 0
				Snapshot:         true,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, ErrStaleDraft):
				stale++
			default:
				others = append(others, err)
			}
		}(i)
	}
	wg.Wait()

	if len(others) > 0 {
		t.Fatalf("unexpected errors: %v", others)
	}
	if won != 1 {
		t.Errorf("successful writes = %d, want exactly 1", won)
	}
	if stale != editors-1 {
		t.Errorf("stale rejections = %d, want %d", stale, editors-1)
	}

	// The surviving draft is one editor's, whole — not a blend of several.
	final, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.HasPrefix(final.Objective, "Objective from editor ") {
		t.Errorf("final objective = %q, want one editor's write intact", final.Objective)
	}
	if final.DraftRevision != 1 {
		t.Errorf("draft revision = %d, want 1 after a single accepted write", final.DraftRevision)
	}
}

func TestRecoverRejectsAnUnknownSnapshot(t *testing.T) {
	ctx := context.Background()
	service, _ := newDraftingService(t, &scriptedModel{})
	plan := mustCreatePlan(t, ctx, service)

	if _, err := service.RecoverSnapshot(ctx, "ws-1", plan.ID, "snap-missing", "jj"); !errors.Is(err, ErrPlanNotFound) {
		t.Errorf("unknown snapshot error = %v, want ErrPlanNotFound", err)
	}
}
