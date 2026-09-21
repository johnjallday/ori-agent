package sessionhttp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

func reviewedFileJanitorHandler(t *testing.T) (*Handler, *agentstore.CompositeStore, agentworkspace.Store, func()) {
	t.Helper()
	handler, cleanup := createTestHandler(t)
	base := t.TempDir()
	root := filepath.Join(base, "Ori Workspaces")
	data := filepath.Join(base, "data")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	system, err := agentstore.NewFileStore(filepath.Join(data, "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	agents, err := agentstore.NewCompositeStore(system, root, filepath.Join(data, "agent_state"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	handler.SetAgentStore(agents)

	folders, err := agentworkspace.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	handler.SetWorkspaceStore(folders)
	workspaces := agentworkspace.NewSyncStore(session.NewWorkspaceStoreAdapter(handler.store), folders)
	handler.SetWorkspaceTaskStore(workspaces)
	registry, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	handler.SetTemplateCapabilityService(workspacecapability.NewService(registry, workspaces))
	templates := filepath.Join("..", "projecttemplates", "starter")
	handler.SetTemplatesRootResolver(func() string { return templates })
	return handler, agents, workspaces, cleanup
}

func TestReviewedFileJanitorCreationStampsExactOperationAndReplays(t *testing.T) {
	handler, agents, workspaces, cleanup := reviewedFileJanitorHandler(t)
	defer cleanup()
	plan, err := handler.ReviewFileJanitorCreation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Roles) != 1 || plan.Roles[0].Name != "File Curator" || plan.Roles[0].Action != "create" {
		t.Fatalf("plan = %+v", plan)
	}
	descriptor := AssistantSetupCreationDescriptor{
		OwnerUserID: "local", RunID: uuid.NewString(), OperationID: uuid.NewString(),
		ReviewDigest: "review-digest", WorkspaceID: uuid.NewString(),
		ProfileProvenanceID: uuid.NewString(), ConfigDigest: plan.Roles[0].ConfigDigest,
	}
	request := ReviewedTemplateCreationRequest{Name: "File Janitor", Plan: plan, Descriptor: descriptor}
	result, err := handler.CreateReviewedFileJanitor(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspaceID != descriptor.WorkspaceID || result.AgentInstanceID == "" ||
		!result.ProfileCreated || result.ProfileProvenanceID != descriptor.ProfileProvenanceID {
		t.Fatalf("result = %+v", result)
	}
	folders := workspaces.(interface {
		GetFolderWorkspace(string) (*agentworkspace.Workspace, error)
	})
	ws, err := folders.GetFolderWorkspace(descriptor.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	provenance := ws.GetTemplateProvenance()
	if provenance == nil || provenance.AssistantSetup == nil || provenance.AssistantSetup.OperationID != descriptor.OperationID ||
		!ws.HasInstalledCapability(agentworkspace.CapabilityFileJanitor) {
		t.Fatalf("workspace provenance = %+v", provenance)
	}
	profile, found := agents.GetAgent("File Curator")
	if !found || profile.AssistantSetup == nil || profile.AssistantSetup.ID != descriptor.ProfileProvenanceID {
		t.Fatalf("profile provenance = %+v", profile)
	}

	replay, err := handler.CreateReviewedFileJanitor(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replay != result {
		t.Fatalf("replay = %+v, want %+v", replay, result)
	}
	ids, err := workspaces.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("workspaces = %v", ids)
	}
}

func TestReviewedFileJanitorCreationRejectsPlanDriftBeforeWrites(t *testing.T) {
	handler, agents, workspaces, cleanup := reviewedFileJanitorHandler(t)
	defer cleanup()
	plan, err := handler.ReviewFileJanitorCreation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan.Roles[0].ConfigDigest = "changed"
	descriptor := AssistantSetupCreationDescriptor{
		OwnerUserID: "local", RunID: uuid.NewString(), OperationID: uuid.NewString(),
		ReviewDigest: "review", WorkspaceID: uuid.NewString(), ProfileProvenanceID: uuid.NewString(),
		ConfigDigest: "changed",
	}
	_, err = handler.CreateReviewedFileJanitor(context.Background(), ReviewedTemplateCreationRequest{Name: "File Janitor", Plan: plan, Descriptor: descriptor})
	if !errors.Is(err, ErrReviewedPlanChanged) {
		t.Fatalf("changed plan err = %v, want the typed pre-write refusal", err)
	}
	if _, found := agents.GetAgent("File Curator"); found {
		t.Fatal("drift created a profile")
	}
	ids, listErr := workspaces.List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(ids) != 0 {
		t.Fatalf("drift created workspaces %v", ids)
	}
}

func TestReviewedFileJanitorCreationFailsClosedWithoutRootAgentStorage(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	base := t.TempDir()
	system, err := agentstore.NewFileStore(filepath.Join(base, "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	missingRoot := filepath.Join(base, "missing-root")
	agents, err := agentstore.NewCompositeStore(system, missingRoot, filepath.Join(base, "state"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	handler.SetAgentStore(agents)
	handler.SetTemplatesRootResolver(func() string { return filepath.Join("..", "projecttemplates", "starter") })
	if _, err := handler.ReviewFileJanitorCreation(context.Background()); !errors.Is(err, agentstore.ErrAgentRootUnavailable) {
		t.Fatalf("missing root agent storage err = %v, want the typed pre-write refusal", err)
	}
}

func reviewedCreationFixture(t *testing.T, handler *Handler) ReviewedTemplateCreationRequest {
	t.Helper()
	plan, err := handler.ReviewFileJanitorCreation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ReviewedTemplateCreationRequest{Name: "File Janitor", Plan: plan, Descriptor: AssistantSetupCreationDescriptor{
		OwnerUserID: "local", RunID: uuid.NewString(), OperationID: uuid.NewString(),
		ReviewDigest: "review-digest", WorkspaceID: uuid.NewString(),
		ProfileProvenanceID: uuid.NewString(), ConfigDigest: plan.Roles[0].ConfigDigest,
	}}
}

func TestObserveReviewedFileJanitorReportsExactlyWhatTheClaimLeftAndWritesNothing(t *testing.T) {
	handler, agents, workspaces, cleanup := reviewedFileJanitorHandler(t)
	defer cleanup()
	request := reviewedCreationFixture(t, handler)

	before, err := handler.ObserveReviewedFileJanitor(request)
	if err != nil {
		t.Fatal(err)
	}
	if before != (ReviewedFileJanitorObservation{}) {
		t.Fatalf("nothing exists yet, observation = %+v", before)
	}

	result, err := handler.CreateReviewedFileJanitor(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	idsBefore, _ := workspaces.List()
	agentsBefore := len(agents.ListAgents())
	observed, err := handler.ObserveReviewedFileJanitor(request)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.WorkspacePresent || !observed.WorkspaceProven || observed.AgentInstanceID != result.AgentInstanceID ||
		!observed.ProfileCreated || observed.ProfileProvenanceID != request.Descriptor.ProfileProvenanceID || observed.ProfileStoreOrigin == "" {
		t.Fatalf("observation = %+v, result = %+v", observed, result)
	}
	idsAfter, _ := workspaces.List()
	if len(idsAfter) != len(idsBefore) || len(agents.ListAgents()) != agentsBefore {
		t.Fatalf("observing wrote state: workspaces %d→%d agents %d→%d", len(idsBefore), len(idsAfter), agentsBefore, len(agents.ListAgents()))
	}

	// Another claim at the same reserved workspace ID finds something there but
	// cannot prove it; the same-name profile without its markers is unrelated.
	other := request
	other.Descriptor.OperationID = uuid.NewString()
	other.Descriptor.ProfileProvenanceID = uuid.NewString()
	foreign, err := handler.ObserveReviewedFileJanitor(other)
	if err != nil {
		t.Fatal(err)
	}
	if !foreign.WorkspacePresent || foreign.WorkspaceProven || foreign.AgentInstanceID != "" || foreign.ProfileCreated {
		t.Fatalf("foreign observation = %+v", foreign)
	}
}
