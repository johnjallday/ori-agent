package folderagent

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/llm"
)

func TestTurn_EachResolveFailureNamesItsCause(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		reason   string
		sentence string
	}{{
		name:     "no model chosen",
		err:      &folderlook.ModelError{Cause: folderlook.Cause{Kind: folderlook.CauseNoModel}},
		reason:   SkipNoModel,
		sentence: "Ori Music Demo Manager could not look itself: No model is set for Ori Music Demo Manager. Fix it and press Look further. This is what Ori found.",
	}, {
		name:     "the plain no-model error",
		err:      folderlook.ErrNoModel,
		reason:   SkipNoModel,
		sentence: "Ori Music Demo Manager could not look itself: No model is set for Ori Music Demo Manager. Fix it and press Look further. This is what Ori found.",
	}, {
		name: "no API key",
		err: &folderlook.ModelError{Cause: folderlook.Cause{Kind: folderlook.CauseNotConfigured, Provider: "Anthropic", Model: "claude-sonnet-5-5"},
			Err: errors.New("anthropic: no api key")},
		reason:   SkipProviderUnavailable,
		sentence: "Ori Music Demo Manager could not look itself: No API key is set for Anthropic. Fix it and press Look further. This is what Ori found.",
	}, {
		name:     "a CLI provider that is not signed in",
		err:      &folderlook.ModelError{Cause: folderlook.Cause{Kind: folderlook.CauseNotSignedIn, Provider: "Codex", Model: "gpt-6.1-sol"}},
		reason:   SkipProviderUnavailable,
		sentence: "Ori Music Demo Manager could not look itself: Codex is not signed in. Fix it and press Look further. This is what Ori found.",
	}, {
		name:     "a model that cannot call tools",
		err:      &folderlook.ModelError{Cause: folderlook.Cause{Kind: folderlook.CauseNoTools, Provider: "Ollama", Model: "tiny"}},
		reason:   SkipProviderUnavailable,
		sentence: "Ori Music Demo Manager could not look itself: tiny on Ollama cannot use tools, which the look needs. This is what Ori found.",
	}, {
		// A failure the host could not classify keeps the old sentence.
		name:     "an unclassified resolve failure",
		err:      errors.New("registry broke"),
		reason:   SkipProviderUnavailable,
		sentence: "Ori Music Demo Manager's model was unavailable, so it could not look itself. This is what Ori found.",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, demo(t))
			withTurns(h, &scriptedTurns{err: tc.err})
			view := h.start(t, "")
			rec, _ := h.svc.Record("u1", view.WorkspaceID)
			if rec.Turn == nil || rec.Turn.Status != TurnSkipped || rec.Turn.Reason != tc.reason {
				t.Fatalf("receipt = %+v", rec.Turn)
			}
			report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
			if report.ModelSentence != tc.sentence {
				t.Fatalf("sentence = %q", report.ModelSentence)
			}
		})
	}
}

func TestTurn_AFailedCallNamesTheProviderAndModel(t *testing.T) {
	h := newHarness(t, demo(t))
	withTurns(h, &scriptedTurns{chatErr: llm.NewProviderError("Anthropic", llm.CategoryInvalidModel,
		errors.New(`404 {"message":"Workspace wrkspc_1 not found"}`))})
	view := h.start(t, "")
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	if rec.Turn.Reason != SkipProviderUnavailable || rec.Turn.Cause != "Anthropic returned an error for luna-test." || rec.Turn.CauseFixable {
		t.Fatalf("receipt = %+v", rec.Turn)
	}
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if report.ModelSentence != "Ori Music Demo Manager could not look itself: Anthropic returned an error for luna-test. This is what Ori found." {
		t.Fatalf("sentence = %q", report.ModelSentence)
	}
}
