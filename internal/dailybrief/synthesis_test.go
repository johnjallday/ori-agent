package dailybrief

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
)

type fakeChatCompleter struct {
	response *llm.ChatResponse
	err      error
	calls    int
}

func (f *fakeChatCompleter) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func sampleSnapshot(now time.Time) Snapshot {
	return Snapshot{
		GeneratedAt: now,
		Workspaces: []WorkspaceSnapshot{{
			WorkspaceID: "ws-1", Name: "Personal HQ",
			OpenTasks: []TaskSnapshot{
				{Ref: refAt("task", "t-failed", now), Status: "failed", Description: "Deploy failed"},
			},
		}},
	}
}

func resolverFor(snap Snapshot, previous *Revision, err error) SnapshotResolver {
	return func(ctx context.Context, req GenerationRequest, cfg Config) (Snapshot, *Revision, error) {
		return snap, previous, err
	}
}

func TestSynthesizer_NoChatConfiguredUsesDeterministicFallback(t *testing.T) {
	now := time.Now()
	snap := sampleSnapshot(now)
	s := &Synthesizer{Resolver: resolverFor(snap, nil, nil)}

	result, err := s.Generate(context.Background(), GenerationRequest{}, Config{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Status != GenerationSucceeded {
		t.Fatalf("expected succeeded status for the deterministic fallback, got %q", result.Status)
	}
	var content BriefContent
	if err := json.Unmarshal([]byte(result.ContentJSON), &content); err != nil {
		t.Fatalf("failed to decode content: %v", err)
	}
	if !content.Degraded {
		t.Fatal("expected Degraded=true when no model is configured")
	}
	if len(content.NeedsAttention) != 1 || content.NeedsAttention[0].Ref.EntityID != "t-failed" {
		t.Fatalf("expected the failed task surfaced in needs_attention, got %+v", content.NeedsAttention)
	}
}

func TestSynthesizer_NoModelShowsGroundedFollowUps(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	due := now.Add(time.Hour)
	ref := SourceRef{WorkspaceID: "hq", EntityType: "follow_up", EntityID: "followup-1", Timestamp: now.Add(-8 * 24 * time.Hour)}
	snap := Snapshot{GeneratedAt: now, FollowUps: []FollowUpSnapshot{{
		Ref: ref, Category: "i_owe", Direction: "outbound", Title: "Send Maya the draft",
		Status: "active", DueAt: &due, Stale: true,
	}}}
	s := &Synthesizer{Resolver: resolverFor(snap, nil, nil)}
	result, err := s.Generate(context.Background(), GenerationRequest{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	var content BriefContent
	if err := json.Unmarshal([]byte(result.ContentJSON), &content); err != nil {
		t.Fatal(err)
	}
	if len(content.NeedsAttention) != 1 || content.NeedsAttention[0].Ref != ref || content.NeedsAttention[0].Reason != "follow_up_stale" {
		t.Fatalf("follow-up attention = %#v", content.NeedsAttention)
	}
	if len(content.TodaysPlan) != 1 || content.TodaysPlan[0].Ref != ref || content.TodaysPlan[0].Reason != "follow_up_due" {
		t.Fatalf("follow-up plan = %#v", content.TodaysPlan)
	}
}

func TestSynthesizer_ModelUnavailableFallsBackToPartial(t *testing.T) {
	now := time.Now()
	snap := sampleSnapshot(now)
	s := &Synthesizer{
		Resolver: resolverFor(snap, nil, nil),
		Chat:     &fakeChatCompleter{err: errors.New("model unavailable")},
	}
	result, err := s.Generate(context.Background(), GenerationRequest{}, Config{})
	if err != nil {
		t.Fatalf("Generate should not itself error on model failure (fallback instead): %v", err)
	}
	if result.Status != GenerationPartial {
		t.Fatalf("expected partial status when the model fails and a fallback is used, got %q", result.Status)
	}
	var content BriefContent
	if err := json.Unmarshal([]byte(result.ContentJSON), &content); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !content.Degraded {
		t.Fatal("expected Degraded=true for the fallback content")
	}
}

func TestSynthesizer_ModelSuccessValidatesAndSucceeds(t *testing.T) {
	now := time.Now()
	snap := sampleSnapshot(now)
	modelJSON := `{
		"opening_summary": "One thing needs attention today.",
		"needs_attention": [{"ref": {"workspace_id":"ws-1","entity_type":"task","entity_id":"t-failed","timestamp":"` + now.Format(time.RFC3339) + `"}, "title": "Deploy failed", "workspace_name": "Personal HQ", "reason": "failed"}],
		"since_last_brief": [],
		"todays_plan": [],
		"resume": [],
		"suggested_actions": []
	}`
	s := &Synthesizer{
		Resolver: resolverFor(snap, nil, nil),
		Chat:     &fakeChatCompleter{response: &llm.ChatResponse{Content: modelJSON}},
	}
	result, err := s.Generate(context.Background(), GenerationRequest{}, Config{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Status != GenerationSucceeded {
		t.Fatalf("expected succeeded status, got %q", result.Status)
	}
	var content BriefContent
	if err := json.Unmarshal([]byte(result.ContentJSON), &content); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if content.Degraded {
		t.Fatal("expected Degraded=false when model output was used")
	}
	if content.OpeningSummary != "One thing needs attention today." {
		t.Fatalf("expected the model's opening summary to be used, got %q", content.OpeningSummary)
	}
}

// TestSynthesizer_StripsHallucinatedReferenceAndMarksPartial covers task
// 6.12: a model-fabricated reference (not present in the snapshot) must be
// dropped, not persisted, and the result marked partial rather than fully
// succeeded or fully failed.
func TestSynthesizer_StripsHallucinatedReferenceAndMarksPartial(t *testing.T) {
	now := time.Now()
	snap := sampleSnapshot(now)
	modelJSON := `{
		"opening_summary": "Two things need attention.",
		"needs_attention": [
			{"ref": {"workspace_id":"ws-1","entity_type":"task","entity_id":"t-failed","timestamp":"` + now.Format(time.RFC3339) + `"}, "title": "Deploy failed", "workspace_name": "Personal HQ", "reason": "failed"},
			{"ref": {"workspace_id":"ws-999","entity_type":"task","entity_id":"fabricated","timestamp":"` + now.Format(time.RFC3339) + `"}, "title": "Made up task", "workspace_name": "Nowhere", "reason": "failed"}
		],
		"since_last_brief": [], "todays_plan": [], "resume": [], "suggested_actions": []
	}`
	s := &Synthesizer{
		Resolver: resolverFor(snap, nil, nil),
		Chat:     &fakeChatCompleter{response: &llm.ChatResponse{Content: modelJSON}},
	}
	result, err := s.Generate(context.Background(), GenerationRequest{}, Config{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Status != GenerationPartial {
		t.Fatalf("expected partial status when a reference is stripped, got %q", result.Status)
	}
	var content BriefContent
	if err := json.Unmarshal([]byte(result.ContentJSON), &content); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(content.NeedsAttention) != 1 {
		t.Fatalf("expected exactly the one valid item to survive, got %+v", content.NeedsAttention)
	}
	if content.NeedsAttention[0].Ref.EntityID != "t-failed" {
		t.Fatalf("expected the fabricated item dropped and the real one kept, got %+v", content.NeedsAttention)
	}
}

func TestSynthesizer_UnparsableModelResponseFallsBack(t *testing.T) {
	now := time.Now()
	snap := sampleSnapshot(now)
	s := &Synthesizer{
		Resolver: resolverFor(snap, nil, nil),
		Chat:     &fakeChatCompleter{response: &llm.ChatResponse{Content: "not json at all"}},
	}
	result, err := s.Generate(context.Background(), GenerationRequest{}, Config{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Status != GenerationPartial {
		t.Fatalf("expected partial (fallback) status for unparsable model output, got %q", result.Status)
	}
}

func TestSynthesizer_QuietDayProducesQuietOpeningSummary(t *testing.T) {
	now := time.Now()
	snap := Snapshot{GeneratedAt: now} // no workspaces, nothing happening
	s := &Synthesizer{Resolver: resolverFor(snap, &Revision{GeneratedAt: now.Add(-24 * time.Hour)}, nil)}
	result, err := s.Generate(context.Background(), GenerationRequest{}, Config{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var content BriefContent
	if err := json.Unmarshal([]byte(result.ContentJSON), &content); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if content.OpeningSummary == "" {
		t.Fatal("expected a concise quiet-day summary, not an empty string")
	}
	if len(content.NeedsAttention) != 0 {
		t.Fatalf("expected no attention items on a quiet day, got %+v", content.NeedsAttention)
	}
}

func TestSynthesizer_FailedFollowUpReadNeverClaimsHealthyEmpty(t *testing.T) {
	now := time.Now()
	snap := Snapshot{GeneratedAt: now, Gaps: []string{"Personal HQ follow-ups could not be read"}}
	s := &Synthesizer{Resolver: resolverFor(snap, &Revision{GeneratedAt: now.Add(-time.Hour)}, nil)}
	result, err := s.Generate(context.Background(), GenerationRequest{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	var content BriefContent
	if err := json.Unmarshal([]byte(result.ContentJSON), &content); err != nil {
		t.Fatal(err)
	}
	if content.OpeningSummary == "A quiet day — nothing needs your attention right now." || len(content.DataGaps) != 1 {
		t.Fatalf("failed source rendered healthy empty: %#v", content)
	}
}

func TestSynthesizer_FirstBriefFramingWhenNoPreviousRevision(t *testing.T) {
	now := time.Now()
	snap := sampleSnapshot(now)
	s := &Synthesizer{Resolver: resolverFor(snap, nil, nil)}
	result, err := s.Generate(context.Background(), GenerationRequest{}, Config{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var content BriefContent
	if err := json.Unmarshal([]byte(result.ContentJSON), &content); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !content.IsFirstBrief {
		t.Fatal("expected IsFirstBrief=true when there is no previous revision")
	}
}

func TestSynthesizer_ResolverErrorPropagates(t *testing.T) {
	s := &Synthesizer{Resolver: resolverFor(Snapshot{}, nil, errors.New("boom"))}
	if _, err := s.Generate(context.Background(), GenerationRequest{}, Config{}); err == nil {
		t.Fatal("expected the resolver's error to propagate")
	}
}

func TestSynthesizer_NoResolverConfiguredErrors(t *testing.T) {
	s := &Synthesizer{}
	if _, err := s.Generate(context.Background(), GenerationRequest{}, Config{}); err == nil {
		t.Fatal("expected an error when no resolver is configured")
	}
}

func TestValidateAgainstAllowlist_RejectsFabricatedFollowUpRef(t *testing.T) {
	allowedRef := SourceRef{WorkspaceID: "hq", EntityType: "follow_up", EntityID: "real"}
	content := BriefContent{NeedsAttention: []BriefAttentionItem{
		{Ref: allowedRef, Title: "Real"},
		{Ref: SourceRef{WorkspaceID: "hq", EntityType: "follow_up", EntityID: "fabricated"}, Title: "Fake"},
	}}
	got, dropped := ValidateAgainstAllowlist(content, map[string]SourceRef{allowedRef.Key(): allowedRef})
	if dropped != 1 || len(got.NeedsAttention) != 1 || got.NeedsAttention[0].Ref.EntityID != "real" {
		t.Fatalf("validated=%#v dropped=%d", got, dropped)
	}
}

func TestValidateAgainstAllowlist_DropsOnlyInvalidRefs(t *testing.T) {
	allowed := map[string]SourceRef{
		"task:ws-1:t1": {WorkspaceID: "ws-1", WorkspaceSlug: "marketing-site", EntityType: "task", EntityID: "t1"},
	}
	content := BriefContent{
		NeedsAttention: []BriefAttentionItem{
			{Ref: SourceRef{WorkspaceID: "ws-1", EntityType: "task", EntityID: "t1"}, Title: "valid"},
			{Ref: SourceRef{WorkspaceID: "ws-2", EntityType: "task", EntityID: "fake"}, Title: "invalid"},
		},
	}
	got, dropped := ValidateAgainstAllowlist(content, allowed)
	if dropped != 1 {
		t.Fatalf("expected 1 dropped item, got %d", dropped)
	}
	if len(got.NeedsAttention) != 1 || got.NeedsAttention[0].Title != "valid" {
		t.Fatalf("expected only the valid item to survive, got %+v", got.NeedsAttention)
	}
	if got.NeedsAttention[0].Ref.WorkspaceSlug != "marketing-site" {
		t.Fatalf("expected canonical slug to be restored from allowlist: %+v", got.NeedsAttention[0].Ref)
	}
}
