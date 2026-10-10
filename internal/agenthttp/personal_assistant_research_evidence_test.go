package agenthttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
)

func TestResearchEvidenceSharedNamespaceRejectsFailedAndInventedReceipts(t *testing.T) {
	ledger := newEvidenceLedger()
	source := ledger.record(assistantcontext.SourceRef{Kind: "note", ID: "note-a", WorkspaceID: "project-a", Version: "v1"})
	result := researchResultFixture()
	ledger.recordResearch(&result)
	if source.Key != "S1" || result.Candidates[0].Receipt.Key != "S2" {
		t.Fatal("citation namespace collision")
	}
	failed := researchResultFixture()
	failed.Candidates[0].Receipt.Availability = assistantdiscovery.Unavailable
	ledger.recordResearch(&failed)
	if failed.Candidates[0].Receipt.Key != "" {
		t.Fatal("failed source issued citation")
	}
	answer, sources := ledger.cite("Workspace [S1]. Listing [S2]. Invention [S3].")
	if strings.Contains(answer, "[S3]") || len(sources) != 1 || len(ledger.researchReferences()) != 1 || !ledger.researchReferences()[0].Cited {
		t.Fatal("invented receipt cited")
	}
}
func TestResearchFolderReviewBindsCanonicalFocusWithoutTranscriptRead(t *testing.T) {
	f, store, _ := newResearchHostFixture(t)
	store.messagesErr = assistantdiscovery.ErrReviewRefused
	store.folder = assistantcontext.ResearchFolderRef{Revision: "event-a", SelectionID: "selection-a", FocusIDs: []string{"topic-a"}}
	ref := &HomeAssistantFolderRef{Revision: "event-a", SelectionID: "selection-a", Historical: true, FocusIDs: []string{"topic-a"}}
	scope, err := f.handler.ResolveResearchScope(context.Background(), &HomeAssistantConversationRef{ID: "canonical"}, f.refs, ref)
	if err != nil || scope.FolderDigest == "" {
		t.Fatal(scope, err)
	}
	review, err := f.handler.ResearchReviews.Prepare(context.Background(), scope, assistantdiscovery.Lookup{Operation: "skills_catalog", Query: "Telegram"})
	if err != nil {
		t.Fatal(err)
	}
	changed := *ref
	changed.FocusIDs = []string{"topic-b"}
	if _, err := f.handler.ResolveResearchScope(context.Background(), &HomeAssistantConversationRef{ID: "canonical"}, f.refs, &changed); err == nil {
		t.Fatal("unsaved edited focus authorized")
	}
	store.folder.FocusIDs = []string{"topic-b"}
	if _, err := f.handler.ResearchReviews.Approve(context.Background(), scope, review); err == nil {
		t.Fatal("changed canonical focus preserved approval")
	}
}
func TestResearchEvidenceOversizedSavedReferencesCannotLeaveUnresolvedMarkers(t *testing.T) {
	result := researchResultFixture()
	ledger := newEvidenceLedger()
	for range assistantcontext.SourceLimit {
		copy := result
		copy.Candidates = append([]assistantdiscovery.Candidate(nil), result.Candidates...)
		copy.Candidates[0].Receipt.URL = "https://example.com/" + strings.Repeat("a", 1800)
		ledger.recordResearch(&copy)
	}
	answer, _ := ledger.cite("Source [S1] and [S12].")
	attr := &assistantcontext.Attribution{Version: 1, Research: ledger.researchReferences()}
	answer, bounded := boundedTurnSources(answer, attr)
	data, err := assistantcontext.EncodeAttribution(bounded)
	if err != nil || len(data) > assistantcontext.AttributionLimit {
		t.Fatal("unbounded attribution", err)
	}
	if strings.Contains(answer, "[S12]") {
		t.Fatal("omitted canonical reference kept marker")
	}
	if !strings.Contains(answer, "[S1]") {
		t.Fatal("first bounded citation dropped")
	}
}
