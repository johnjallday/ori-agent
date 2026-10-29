package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestWorkspaceBuildSoftware_ExplicitReviewUsesExistingEvidenceOwnerAndCreatesNothing(t *testing.T) {
	f := newDraftServerFixture(t)
	ctx := context.Background()
	root := filepath.Join(os.Getenv("HOME"), "Documents")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	bytes := []byte("module example.org/synthetic\n\ngo 1.24\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	observations := f.builder.server.Storage.PersonalAssistantFolderObservations
	projection, err := f.builder.personalAssistantService.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	id := "4fe2dcaa-3b31-4eae-9e53-4600fbab107c"
	draft := foldercontext.Target{UserID: "local", WorkspaceID: projection.HQWorkspaceID, AgentName: strings.ToLower(projection.GlobalAgentProfile), DraftID: personalassistant.BuildReviewPrefix + id}
	observation, err := observations.Observe(ctx, draft, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	recognition, err := observations.Recognize(ctx, draft, observation.ID)
	if err != nil || !recognition.Recognized() {
		t.Fatalf("recognition=%+v %v", recognition, err)
	}
	now := time.Now().UTC()
	_, err = f.builder.workspaceBuildStore.Mutate(ctx, "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		doc.Add(personalassistant.WorkspaceBuildSession{ID: id, Version: 1, Status: personalassistant.WorkspaceBuildOpen, CreatedAt: now, UpdatedAt: now, EntryPoint: "home_cockpit_create", Transcript: []personalassistant.BuildTranscriptEntry{}, Folder: &personalassistant.BuildFolder{Observation: *observation, Recognition: recognition}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	metadata, err := observations.BuildPlan(ctx, draft, observation.ID, recognition.CandidateID, "Software proof")
	if err != nil || metadata.Ready {
		t.Fatalf("metadata alone authorized a software plan: %+v %v", metadata, err)
	}
	view, err := observations.ReviewBuildSoftware(ctx, draft, id, observation.ID, recognition.CandidateID, "Software proof", "explicit-software-review", true)
	if err != nil {
		t.Fatal(err)
	}
	if view.Software == nil || view.Software.Review == nil || view.Software.Review.EvidenceRevision == "" {
		t.Fatalf("missing canonical evidence review: %+v", view.Software)
	}
	plan := personalassistant.BuildFolderPlanFromReview(&view)
	if plan == nil || !plan.Ready || !plan.SoftwareReview || !plan.FirstTaskDeferred || plan.Digest == "" {
		t.Fatalf("plan=%+v", plan)
	}
	if view.Setup != nil || view.Outcome != nil || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("review executed domain effects")
	}
	saved, err := f.builder.workspaceBuildStore.Read(ctx, "local")
	if err != nil || saved.Session(id).Setup != nil || saved.Session(id).Status != personalassistant.WorkspaceBuildOpen {
		t.Fatalf("review accepted setup: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || string(got) != string(bytes) {
		t.Fatal("review changed source bytes")
	}
	// Names-only edits keep the original reviewed evidence, even if the
	// project has since changed. Final setup must recheck that change, not
	// make Adjust an undisclosed inspection operation.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.org/changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adjusted, err := observations.ReviewBuildSoftware(ctx, draft, id, observation.ID, recognition.CandidateID, "Edited software proof", "names-only", false)
	if err != nil || adjusted.Software == nil || adjusted.Software.Review == nil || adjusted.Software.Review.EvidenceRevision != view.Software.Review.EvidenceRevision || adjusted.Plan == nil || adjusted.Plan.Digest == plan.Digest {
		t.Fatalf("names-only edit lost source ownership/evidence: %+v %v", adjusted, err)
	}
	plan = personalassistant.BuildFolderPlanFromReview(&adjusted)
	observed, err := observations.ReadBuildSetup(ctx, personalassistant.BuildReviewTarget(draft, id), view.ID)
	if err != nil || observed == nil || observed.Plan.Digest != plan.Digest {
		t.Fatalf("read=%+v %v", observed, err)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("observation created a workspace")
	}
}
