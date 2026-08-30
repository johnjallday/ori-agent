package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testFolderResolver struct{ root string }

func (resolver testFolderResolver) GetFolderPath(string) (string, error) { return resolver.root, nil }

func learningEvidence() []AssistantEvidenceReference {
	now := time.Now().UTC()
	return []AssistantEvidenceReference{
		{SourceID: "task-a", ProjectID: "project-a", ProjectSlug: "alpha", Route: "/workspaces/alpha", Summary: "A repeated preference appeared.", ObservedAt: now},
		{SourceID: "task-b", ProjectID: "project-b", ProjectSlug: "beta", Route: "/workspaces/beta", Summary: "The same preference appeared again.", ObservedAt: now},
		{SourceID: "task-c", ProjectID: "project-c", ProjectSlug: "gamma", Route: "/workspaces/gamma", Summary: "A third project supports the pattern.", ObservedAt: now},
	}
}

func TestAssistantLearningStore_ApproveEditDeleteTombstone(t *testing.T) {
	root := t.TempDir()
	store := NewAssistantLearningStore(testFolderResolver{root: root})
	document, err := store.AddCandidates("station", 0, []AssistantLearningCandidate{{
		Fingerprint: "pattern-1", Type: "preference", Text: "Projects consistently use a short review checklist.",
		Confidence: "high", Evidence: learningEvidence(), SourceRunID: "run-1",
	}})
	if err != nil || len(document.Candidates) != 1 {
		t.Fatalf("add candidates = (%+v, %v)", document, err)
	}
	candidateID := document.Candidates[0].ID
	learning, err := store.ApproveCandidate("station", candidateID, document.Version)
	if err != nil || learning.ID == "" {
		t.Fatalf("approve = (%+v, %v)", learning, err)
	}
	current, ok := learning.Current()
	if !ok || len(current.Evidence) != 3 {
		t.Fatalf("current revision = (%+v, %v)", current, ok)
	}
	updated, err := store.EditLearning("station", learning.ID, "Projects use a concise review checklist.", "preference", "medium", learning.Version)
	if err != nil || len(updated.Revisions) != 2 || updated.Version != 2 {
		t.Fatalf("edit = (%+v, %v)", updated, err)
	}
	if err := store.DeleteLearning("station", learning.ID, updated.Version); err != nil {
		t.Fatalf("delete: %v", err)
	}
	final, err := store.Read("station")
	if err != nil || len(CurrentAssistantLearnings(final)) != 0 || len(final.Tombstones) != 1 {
		t.Fatalf("final = (%+v, %v)", final, err)
	}
	before := final.Version
	after, err := store.AddCandidates("station", before, []AssistantLearningCandidate{{
		Fingerprint: "pattern-1", Type: "preference", Text: "Projects consistently use a short review checklist.", Confidence: "high", Evidence: learningEvidence(), SourceRunID: "run-2",
	}})
	if err != nil || len(after.Candidates) != 1 {
		t.Fatalf("tombstone suppression = (%+v, %v)", after, err)
	}
	info, err := os.Stat(filepath.Join(root, AssistantLearningSidecarDir, AssistantLearningSidecarName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("sidecar permissions = (%v, %v)", info, err)
	}
}

func TestAssistantLearningStore_EditAndDeletePendingCandidate(t *testing.T) {
	store := NewAssistantLearningStore(testFolderResolver{root: t.TempDir()})
	document, err := store.AddCandidates("station", 0, []AssistantLearningCandidate{{
		Fingerprint: "pending-pattern", Type: "fact", Text: "A safe repeated pattern.", Confidence: "low", Evidence: learningEvidence(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := document.Candidates[0]
	edited, err := store.EditCandidate("station", candidate.ID, "A reviewed repeated preference.", "preference", "high", document.Version)
	if err != nil || edited.Text != "A reviewed repeated preference." || edited.Version != 2 {
		t.Fatalf("edit pending = (%+v, %v)", edited, err)
	}
	document, _ = store.Read("station")
	if err := store.DeleteCandidate("station", candidate.ID, document.Version); err != nil {
		t.Fatal(err)
	}
	document, _ = store.Read("station")
	if len(document.Candidates) != 0 || len(document.Tombstones) != 1 || document.Tombstones[0].Fingerprint != "pending-pattern" {
		t.Fatalf("deleted pending document = %+v", document)
	}
}

func TestAssistantLearningStore_PreservesMemoryAndProjectsOnlyApproved(t *testing.T) {
	root := t.TempDir()
	memory := "# Hand-written memory\n\nThis prose stays byte-identical.\n"
	if err := os.WriteFile(filepath.Join(root, MemoryFileName), []byte(memory), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewAssistantLearningStore(testFolderResolver{root: root})
	document, err := store.AddCandidates("station", 0, []AssistantLearningCandidate{{Fingerprint: "f", Type: "fact", Text: "A reviewed pattern exists.", Confidence: "medium", Evidence: learningEvidence(), SourceRunID: "run"}})
	if err != nil {
		t.Fatal(err)
	}
	if prompt := RenderManagedLearningPromptSection(document); prompt != "" {
		t.Fatalf("pending candidate entered prompt: %q", prompt)
	}
	learning, err := store.ApproveCandidate("station", document.Candidates[0].ID, document.Version)
	if err != nil {
		t.Fatal(err)
	}
	document, _ = store.Read("station")
	prompt := RenderManagedLearningPromptSection(document)
	if !strings.Contains(prompt, `"A reviewed pattern exists."`) || strings.Contains(prompt, "task-a") {
		t.Fatalf("managed prompt = %q", prompt)
	}
	gotMemory, err := os.ReadFile(filepath.Join(root, MemoryFileName))
	if err != nil || string(gotMemory) != memory {
		t.Fatalf("MEMORY.md changed while approving %s: %q, %v", learning.ID, gotMemory, err)
	}
}

func TestAssistantLearningStore_RejectsSecretsMalformedAndStaleWrites(t *testing.T) {
	root := t.TempDir()
	store := NewAssistantLearningStore(testFolderResolver{root: root})
	_, err := store.AddCandidates("station", 0, []AssistantLearningCandidate{{Fingerprint: "secret", Type: "fact", Text: "API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456", Confidence: "high", Evidence: learningEvidence()}})
	if err == nil {
		t.Fatal("secret-like learning was accepted")
	}
	if err := os.MkdirAll(filepath.Join(root, AssistantLearningSidecarDir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, AssistantLearningSidecarDir, AssistantLearningSidecarName), []byte(`{"schema_version":1,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("station"); !errors.Is(err, ErrAssistantLearningCorrupt) {
		t.Fatalf("malformed error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, AssistantLearningSidecarDir, AssistantLearningSidecarName)); err != nil {
		t.Fatal(err)
	}
	document, err := store.AddCandidates("station", 0, []AssistantLearningCandidate{{Fingerprint: "ok", Type: "fact", Text: "A safe repeated pattern.", Confidence: "low", Evidence: learningEvidence()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddCandidates("station", document.Version-1, nil); !errors.Is(err, ErrAssistantLearningConflict) {
		t.Fatalf("stale write error = %v", err)
	}
}

func TestAssistantLearningStore_ConcurrentMutationHasOneWinner(t *testing.T) {
	store := NewAssistantLearningStore(testFolderResolver{root: t.TempDir()})
	document, err := store.AddCandidates("station", 0, []AssistantLearningCandidate{{Fingerprint: "race", Type: "fact", Text: "A safe repeated pattern.", Confidence: "low", Evidence: learningEvidence()}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.ApproveCandidate("station", document.Candidates[0].ID, document.Version)
			errorsSeen <- err
		}()
	}
	wg.Wait()
	close(errorsSeen)
	successes, conflicts := 0, 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAssistantLearningConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}
