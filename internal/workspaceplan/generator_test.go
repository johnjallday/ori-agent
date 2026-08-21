package workspaceplan

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// scriptedModel returns canned responses in order and records the prompts it
// was given, so a test can assert both what came back and what was asked.
type scriptedModel struct {
	responses []string
	err       error
	prompts   []string
	systems   []string
	schemas   []string
}

func (m *scriptedModel) GenerateStructured(_ context.Context, req StructuredRequest) (string, error) {
	m.prompts = append(m.prompts, req.Prompt)
	m.systems = append(m.systems, req.System)
	m.schemas = append(m.schemas, req.SchemaName)
	if m.err != nil {
		return "", m.err
	}
	if len(m.responses) == 0 {
		return "", errors.New("scripted model ran out of responses")
	}
	next := m.responses[0]
	m.responses = m.responses[1:]
	return next, nil
}

const validDraftResponse = `{
  "objective": "Migrate reporting safely",
  "groups": [{"id":"grp-1","title":"Prepare","items":[{"id":"itm-1","description":"Snapshot staging"}]}],
  "execution": {"mode":"step_through"}
}`

func TestGenerateDraftReturnsValidatedContent(t *testing.T) {
	model := &scriptedModel{responses: []string{validDraftResponse}}
	outcome, err := NewGenerator(model).GenerateDraft(context.Background(), GenerationInput{
		Request: "Plan the reporting migration",
	})
	if err != nil {
		t.Fatalf("generate draft: %v", err)
	}
	if outcome.Objective != "Migrate reporting safely" {
		t.Errorf("objective = %q", outcome.Objective)
	}
	if len(outcome.Content.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(outcome.Content.Groups))
	}
	if outcome.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 for valid output", outcome.Attempts)
	}
	if len(outcome.Issues) != 0 {
		t.Errorf("valid output reported issues: %v", outcome.Issues)
	}
	if model.schemas[0] != "workspace_plan" {
		t.Errorf("schema name = %q", model.schemas[0])
	}
}

// Invalid output is repaired within a bounded budget, and the repair prompt
// names every issue at once rather than one per round-trip (FR-44).
func TestGenerateDraftRepairsInvalidOutputWithinBudget(t *testing.T) {
	invalid := `{"objective":"","groups":[]}`
	model := &scriptedModel{responses: []string{invalid, validDraftResponse}}

	outcome, err := NewGenerator(model).GenerateDraft(context.Background(), GenerationInput{
		Request: "Plan the reporting migration",
	})
	if err != nil {
		t.Fatalf("generate draft: %v", err)
	}
	if outcome.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one repair)", outcome.Attempts)
	}

	repair := model.prompts[1]
	if !strings.Contains(repair, "rejected") {
		t.Errorf("repair prompt does not say the response was rejected:\n%s", repair)
	}
	if !strings.Contains(repair, string(IssueMissingObjective)) ||
		!strings.Contains(repair, string(IssueNoGroups)) {
		t.Errorf("repair prompt does not name every issue at once:\n%s", repair)
	}
}

// When the budget is spent the content is NOT accepted: the caller gets the
// issues and keeps the Plan a draft rather than falling back to unvalidated
// output (FR-45).
func TestGenerateDraftRefusesInvalidOutputAfterTheBudget(t *testing.T) {
	invalid := `{"objective":"","groups":[]}`
	model := &scriptedModel{responses: []string{invalid, invalid, invalid, invalid}}

	outcome, err := NewGenerator(model, WithRepairAttempts(2)).
		GenerateDraft(context.Background(), GenerationInput{Request: "Plan it"})

	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation", err)
	}
	if outcome.Attempts != 3 {
		t.Errorf("attempts = %d, want 3 (initial plus two repairs)", outcome.Attempts)
	}
	if len(outcome.Issues) == 0 {
		t.Error("exhausted outcome carries no issues to show the user")
	}
	// The rejected content is reported for inspection, never as an accepted
	// draft: the error is what stops the caller using it.
	if err == nil {
		t.Error("invalid content was returned without an error")
	}
}

func TestGenerateDraftRepairsMalformedJSON(t *testing.T) {
	model := &scriptedModel{responses: []string{"not json at all", validDraftResponse}}

	outcome, err := NewGenerator(model).GenerateDraft(context.Background(), GenerationInput{Request: "Plan it"})
	if err != nil {
		t.Fatalf("generate draft: %v", err)
	}
	if outcome.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", outcome.Attempts)
	}
}

// Generation is the only thing a missing model stops. Everything else about a
// Plan keeps working (FR-58, FR-177).
func TestGenerateReportsModelUnavailability(t *testing.T) {
	offline := NewGenerator(nil)
	if offline.Available() {
		t.Error("a generator with no model reports itself available")
	}
	if _, err := offline.GenerateDraft(context.Background(), GenerationInput{Request: "x"}); !errors.Is(err, ErrModelUnavailable) {
		t.Errorf("draft error = %v, want ErrModelUnavailable", err)
	}
	if _, err := offline.GenerateClarifications(context.Background(), GenerationInput{Request: "x"}); !errors.Is(err, ErrModelUnavailable) {
		t.Errorf("clarification error = %v, want ErrModelUnavailable", err)
	}

	failing := NewGenerator(&scriptedModel{err: ErrModelUnavailable})
	if _, err := failing.GenerateDraft(context.Background(), GenerationInput{Request: "x"}); !errors.Is(err, ErrModelUnavailable) {
		t.Errorf("provider failure error = %v, want ErrModelUnavailable", err)
	}
}

// The planner is told what exists so it cannot claim an agent or capability
// that does not (FR-46, FR-47).
func TestDraftPromptStatesWhatIsAvailable(t *testing.T) {
	model := &scriptedModel{responses: []string{validDraftResponse}}
	_, err := NewGenerator(model).GenerateDraft(context.Background(), GenerationInput{
		Request: "Plan it",
		Validation: ValidationContext{
			AvailableAgents:       []string{"builder", "reviewer"},
			AvailableCapabilities: []string{"email"},
		},
		Guidance: GuidanceInput{Style: "investigation", DetailLevel: "concise"},
	})
	if err != nil {
		t.Fatalf("generate draft: %v", err)
	}

	system := model.systems[0]
	for _, want := range []string{"builder", "reviewer", "email", "investigation", "concise"} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt omits %q:\n%s", want, system)
		}
	}
	// The prompt must state that the model cannot approve or start work.
	if !strings.Contains(system, "cannot approve") {
		t.Errorf("system prompt does not disclaim approval authority:\n%s", system)
	}
	if !strings.Contains(system, "Do not propose `prd` or `task_list`") {
		t.Errorf("system prompt still delegates app-owned planning files to the model:\n%s", system)
	}
	// The bounds are stated up front rather than discovered by rejection.
	if !strings.Contains(system, "20 task groups") || !strings.Contains(system, "200 task items") {
		t.Errorf("system prompt omits the hard limits:\n%s", system)
	}
}

// A workspace with no agents must be told so explicitly, or the model will
// invent one (FR-47).
func TestDraftPromptSaysWhenNothingIsAvailable(t *testing.T) {
	model := &scriptedModel{responses: []string{validDraftResponse}}
	_, err := NewGenerator(model).GenerateDraft(context.Background(), GenerationInput{
		Request:    "Plan it",
		Validation: ValidationContext{AvailableAgents: []string{}, AvailableCapabilities: []string{}},
	})
	if err != nil {
		t.Fatalf("generate draft: %v", err)
	}
	system := model.systems[0]
	if !strings.Contains(system, "No agents are available") {
		t.Errorf("system prompt does not say there are no agents:\n%s", system)
	}
}

// User answers are authoritative context and must never be re-asked (FR-25).
func TestDraftPromptCarriesAuthoredAnswersAndSkips(t *testing.T) {
	model := &scriptedModel{responses: []string{validDraftResponse}}
	_, err := NewGenerator(model).GenerateDraft(context.Background(), GenerationInput{
		Request: "Plan it",
		Answers: []Clarification{
			{ID: "c1", Prompt: "Which environment?", Status: ClarificationAnswered, Answer: "Staging only"},
			{ID: "c2", Prompt: "Any deadline?", Status: ClarificationSkipped},
			{ID: "c3", Prompt: "Still open?", Status: ClarificationOpen},
		},
	})
	if err != nil {
		t.Fatalf("generate draft: %v", err)
	}

	prompt := model.prompts[0]
	if !strings.Contains(prompt, "Staging only") {
		t.Errorf("prompt omits the authored answer:\n%s", prompt)
	}
	if !strings.Contains(prompt, "do not re-ask") {
		t.Errorf("prompt does not mark answers authoritative:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Any deadline?") || !strings.Contains(prompt, "record your assumption") {
		t.Errorf("prompt does not turn a skip into an assumption instruction:\n%s", prompt)
	}
	// An unanswered question is not presented as if it had been answered.
	if strings.Contains(prompt, "Still open?") {
		t.Errorf("prompt included an unanswered question as context:\n%s", prompt)
	}
}

// A targeted revision must keep untouched sections identified, which only works
// if ids come back unchanged (FR-55, FR-56).
func TestTargetedRevisionAsksForStableIDsAndTheWholePlan(t *testing.T) {
	model := &scriptedModel{responses: []string{validDraftResponse}}
	_, err := NewGenerator(model).GenerateDraft(context.Background(), GenerationInput{
		Request:   "Tighten the rollout steps",
		Objective: "Migrate reporting safely",
		Content: PlanContent{
			Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
			Groups: []TaskGroup{{
				ID: "grp-existing", Title: "Prepare",
				Items: []TaskItem{{ID: "itm-existing", Description: "Snapshot"}},
			}},
		},
		Sections: []string{"groups"},
	})
	if err != nil {
		t.Fatalf("generate draft: %v", err)
	}

	prompt := model.prompts[0]
	if !strings.Contains(prompt, "grp-existing") || !strings.Contains(prompt, "itm-existing") {
		t.Errorf("revision prompt omits the current plan's ids:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Revise only these sections: groups") {
		t.Errorf("revision prompt does not scope the change:\n%s", prompt)
	}
	if !strings.Contains(prompt, "leaving every other element and every id exactly as it is") {
		t.Errorf("revision prompt does not require id stability:\n%s", prompt)
	}
}

// --- Clarifications --------------------------------------------------------

const clarificationResponse = `{"questions":[
  {"id":"c-keep","prompt":"Which environment?","required":true,"options":["Staging","Production"]},
  {"prompt":"Any deadline?"}
]}`

func TestGenerateClarificationsDecodesStableQuestions(t *testing.T) {
	model := &scriptedModel{responses: []string{clarificationResponse}}
	questions, err := NewGenerator(model).GenerateClarifications(context.Background(), GenerationInput{
		Request: "Plan it",
	})
	if err != nil {
		t.Fatalf("generate clarifications: %v", err)
	}
	if len(questions) != 2 {
		t.Fatalf("questions = %d, want 2", len(questions))
	}
	if questions[0].ID != "c-keep" {
		t.Errorf("supplied question id was not preserved: %q", questions[0].ID)
	}
	if questions[1].ID == "" || !strings.HasPrefix(questions[1].ID, "clr_") {
		t.Errorf("question without an id did not receive one: %q", questions[1].ID)
	}
	if !questions[0].Required || questions[1].Required {
		t.Errorf("required flags did not decode: %+v", questions)
	}
	if questions[0].Status != ClarificationOpen {
		t.Errorf("status = %q, want open", questions[0].Status)
	}
	if len(questions[0].Options) != 2 {
		t.Errorf("options did not decode: %v", questions[0].Options)
	}
}

// The per-round cap is enforced by the application, so a model that ignores the
// instruction still cannot flood the user (FR-27).
func TestClarificationsAreCappedByTheApplication(t *testing.T) {
	many := `{"questions":[
	  {"prompt":"q1"},{"prompt":"q2"},{"prompt":"q3"},
	  {"prompt":"q4"},{"prompt":"q5"},{"prompt":"q6"},{"prompt":"q7"}
	]}`
	model := &scriptedModel{responses: []string{many}}

	questions, err := NewGenerator(model).GenerateClarifications(context.Background(), GenerationInput{
		Request:  "Plan it",
		Guidance: GuidanceInput{MaxQuestionsPerRound: 3},
	})
	if err != nil {
		t.Fatalf("generate clarifications: %v", err)
	}
	if len(questions) != 3 {
		t.Errorf("questions = %d, want the configured cap of 3", len(questions))
	}
	if !strings.Contains(model.systems[0], "at most 3 questions") {
		t.Errorf("system prompt does not state the cap:\n%s", model.systems[0])
	}
}

// Trimming must never drop a required question in favor of a nice-to-have.
func TestCapQuestionsKeepsRequiredOnesFirst(t *testing.T) {
	questions := []Clarification{
		{ID: "c1", Prompt: "optional a"},
		{ID: "c2", Prompt: "required a", Required: true},
		{ID: "c3", Prompt: "optional b"},
		{ID: "c4", Prompt: "required b", Required: true},
	}

	capped := CapQuestions(questions, 2)
	if len(capped) != 2 {
		t.Fatalf("capped = %d, want 2", len(capped))
	}
	for _, question := range capped {
		if !question.Required {
			t.Errorf("an optional question displaced a required one: %+v", capped)
		}
	}

	// No cap configured falls back to the default rather than to unlimited.
	if got := len(CapQuestions(make([]Clarification, 20), 0)); got != DefaultMaxQuestions {
		t.Errorf("uncapped length = %d, want the default of %d", got, DefaultMaxQuestions)
	}
}

func TestDecodeClarificationsRejectsUnusableResponses(t *testing.T) {
	for _, response := range []string{
		"",
		"   ",
		"not json",
		`{"questions":[]}`,
		`{"questions":[{"prompt":"   "}]}`,
		`{"questions":[{"prompt":"q","auto_answer":true}]}`,
	} {
		if _, err := DecodeClarifications([]byte(response)); !errors.Is(err, ErrValidation) {
			t.Errorf("response %q error = %v, want ErrValidation", response, err)
		}
	}
}
