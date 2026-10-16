package projectlibrary

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestHomeRevisionPlanning_QuestionThenExactProposalUsesCanonicalPriorIntent(t *testing.T) {
	f, _, saved, _ := collectionReviewFixture(t)
	question := true
	calls := 0
	goal := "Keep the selected client references together"
	provider := homePlanningProvider{chat: func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
		calls++
		var prior struct {
			Revision int64                      `json:"revision"`
			Intent   workspacearrangement.Draft `json:"intent"`
			Question *RevisionDiscussion        `json:"pending_question"`
		}
		found := false
		for _, message := range request.Messages {
			if strings.HasPrefix(message.Content, "Revise this canonical prior proposal") {
				_, raw, _ := strings.Cut(message.Content, "\n")
				if err := json.Unmarshal([]byte(raw), &prior); err != nil {
					t.Fatal(err)
				}
				found = true
			}
		}
		if !found || prior.Intent.Operations[0].Members.Handle == "" || len(prior.Intent.Operations[0].Members.IDs) != 0 {
			t.Fatal("prior exact selection missing or dumped", prior)
		}
		reply := &llm.ChatResponse{Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 100, TotalTokens: 200}}
		if question {
			if prior.Revision != 1 {
				t.Fatal(prior)
			}
			reply.Content = "Which client should this represent?"
			return reply, nil
		}
		if prior.Question == nil || !strings.Contains(prior.Question.Answer, "Which client should this represent?") || prior.Revision != 2 {
			t.Fatal("canonical question lost", prior)
		}
		if request.Messages[len(request.Messages)-1].Role != llm.RoleTool {
			raw, _ := json.Marshal(map[string]any{"filter": workspacearrangement.Filter{SelectionHandle: prior.Intent.Operations[0].Members.Handle}, "page_size": 20})
			reply.ToolCalls = []llm.ToolCall{{ID: "inspect-prior", Name: "arrangement_search", Arguments: string(raw)}}
			return reply, nil
		}
		prior.Intent.Goal = goal
		prior.Intent.Operations[0].Name = "Chosen client references"
		raw, _ := json.Marshal(prior.Intent)
		reply.ToolCalls = []llm.ToolCall{{ID: "revised", Name: "arrangement_propose", Arguments: string(raw)}}
		return reply, nil
	}}
	var resolve ArrangementRuntimeResolver = func(context.Context, ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{Provider: provider, Model: saved.Record.Model, ProviderFingerprint: saved.Record.ProviderFingerprint, SkillDigest: saved.Record.SkillDigest, Revalidate: func(ctx context.Context) error { return ctx.Err() }}, nil
	}
	run := func(revision int64) ArrangementPlanningResult {
		result, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: goal, ExpectedRevision: f.doc(t).Revision, ShareCatalogMetadata: true, ReviseArrangementID: saved.Record.ID, ExpectedRecordRevision: revision})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := run(1)
	if first.History.RevisionDiscussion == nil || first.History.Arrangement.Revision != 2 || len(f.doc(t).Arrangements) != 1 {
		t.Fatal(first)
	}
	question = false
	second := run(2)
	after := f.doc(t)
	if second.History.RevisionDiscussion != nil || second.History.Arrangement.Revision != 3 || len(after.Arrangements) != 1 || len(after.Collections) != 0 || calls != 3 {
		t.Fatal(second, calls)
	}
	if !reflect.DeepEqual(after.Arrangements[0].Record.Draft.Operations[0].Members.IDs, saved.Record.Draft.Operations[0].Members.IDs) {
		t.Fatal("model revision lost exact selection")
	}
}

func TestHomeRevisionPlanning_LateModelReplyCannotOverwriteOwnerCorrection(t *testing.T) {
	f, _, saved, originalRuntime := collectionReviewFixture(t)
	service := NewCollectionReviewService(f.library, originalRuntime)
	review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	base := f.doc(t)
	calls := 0
	provider := homePlanningProvider{chat: func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		calls++
		name := "Keep owner's corrected name"
		if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: review.Token, Name: &name}); err != nil {
			t.Fatal(err)
		}
		return &llm.ChatResponse{Content: "An obsolete model question", Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 100, TotalTokens: 200}}, nil
	}}
	resolve := func(context.Context, ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{Provider: provider, Model: saved.Record.Model, ProviderFingerprint: saved.Record.ProviderFingerprint, SkillDigest: saved.Record.SkillDigest, Revalidate: func(ctx context.Context) error { return ctx.Err() }}, nil
	}
	result, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Revise current intent", ExpectedRevision: base.Revision, ShareCatalogMetadata: true, ReviseArrangementID: saved.Record.ID, ExpectedRecordRevision: 1})
	if err == nil || result.History != nil || calls != 1 {
		t.Fatal("stale model completion escaped", result, err)
	}
	current := f.doc(t).Arrangements[0]
	if current.Record.Revision != 2 || current.RevisionDiscussion != nil || current.Record.Draft.Operations[0].Name != "Keep owner's corrected name" {
		t.Fatal(current)
	}
}
