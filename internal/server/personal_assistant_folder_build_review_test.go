package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// A confirmed build can stop and then be abandoned while its durable offer
// remains awaiting_outcome. It is not a chat conversation to reopen, and must
// neither poison unrelated folder reviews nor be changed by them.
func TestFolderPlacement_ConfirmedBuildDoesNotBlockAnotherFoldersReview(t *testing.T) {
	for _, abandoned := range []bool{false, true} {
		name := "open"
		if abandoned {
			name = "abandoned"
		}
		t.Run(name, func(t *testing.T) {
			f := newDraftServerFixture(t)
			ctx := context.Background()
			request := stageReviewFolder(t, f)
			projection, err := f.builder.personalAssistantService.Get(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			const buildID = "8f3c9a3e-1d2b-4c5d-9e6f-0a1b2c3d4e5f"
			draft := foldercontext.Target{UserID: "local", WorkspaceID: projection.HQWorkspaceID,
				AgentName: strings.ToLower(projection.GlobalAgentProfile), DraftID: personalassistant.BuildReviewPrefix + buildID}
			observations := f.builder.server.Storage.PersonalAssistantFolderObservations
			if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), "Desktop"), 0750); err != nil {
				t.Fatal(err)
			}
			observation, err := observations.Observe(ctx, draft, "chip", "desktop")
			if err != nil {
				t.Fatal(err)
			}
			offer, err := observations.ReviewBuild(ctx, draft, buildID, observation.ID, observation.Projects[0].ID, "Earlier build")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if _, err := f.builder.workspaceBuildStore.Mutate(ctx, "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
				doc.Add(personalassistant.WorkspaceBuildSession{
					ID: buildID, Status: personalassistant.WorkspaceBuildOpen, Version: 1, CreatedAt: now, UpdatedAt: now,
					EntryPoint: "home_cockpit_create", Transcript: []personalassistant.BuildTranscriptEntry{},
					Folder: &personalassistant.BuildFolder{Observation: *observation},
					Setup: &personalassistant.BuildSetupClaim{OfferID: offer.ID, PlanDigest: strings.Repeat("a", 64),
						RequestID: "earlier-setup", StartedAt: now, StopReason: personalassistant.FolderStopNeedsChoice},
				})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			store := personalassistant.NewFolderDigestStore(personalassistant.NewKnowledgeStore(
				personalassistant.NewKnowledgeResolver(f.builder.personalAssistantStore, f.builder.personalHQService,
					personalassistant.NewAgentStoreProfileReader(f.builder.st)), f.builder.workspaceFileStore))
			// Seed the persisted state of a confirmed, unfinished setup; no task
			// or provider is run by this regression fixture.
			if _, err := store.Mutate(ctx, "local", func(doc *personalassistant.FolderDigestDocument) error {
				stored := doc.Offer(offer.ID)
				stored.Status, stored.Decision, stored.DecidedAt = personalassistant.FolderOfferAwaitingOutcome, "yes", &now
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if abandoned {
				if status, body := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+buildID+"/abandon", map[string]any{}); status != http.StatusOK {
					t.Fatalf("abandon: %d %v", status, body)
				}
			}
			before, err := store.Read(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			buildsBefore, err := f.builder.workspaceBuildStore.Read(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			expectedStatus := personalassistant.WorkspaceBuildOpen
			if abandoned {
				expectedStatus = personalassistant.WorkspaceBuildAbandoned
			}
			if build := buildsBefore.Session(buildID); build == nil || build.Status != expectedStatus {
				t.Fatalf("build lifecycle: %+v, want %s", build, expectedStatus)
			}
			workspaceCount := len(f.builder.workspaceFileStore.CachedWorkspaces())
			request["context"] = map[string]any{"context_version": 1, "origin": "personal_assistant_panel", "surface": "home", "page_path": "/"}
			id, _, newOffer := reviewFolder(t, f, request)
			after, err := store.Read(ctx, "local")
			if err != nil || !reflect.DeepEqual(before.Offer(offer.ID), after.Offer(offer.ID)) {
				t.Fatalf("review changed the build's confirmed offer: %v", err)
			}
			buildsAfter, err := f.builder.workspaceBuildStore.Read(ctx, "local")
			if err != nil || !reflect.DeepEqual(buildsBefore, buildsAfter) {
				t.Fatalf("review changed the build: %v", err)
			}
			if newOffer == offer.ID || after.Pending() == nil || after.Pending().ID != newOffer ||
				len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspaceCount {
				t.Fatal("review must prepare only its own offer, without executing setup")
			}
			// A build earlier in the sidecar must not hide a real pending chat.
			another := stageReviewFolder(t, f)
			another["context"] = request["context"]
			status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", another)
			pending, _ := body["folder_pending_elsewhere"].(map[string]any)
			if status != http.StatusOK || pending["conversation_id"] != id {
				t.Fatalf("pending conversation handoff: %d %v", status, body)
			}
		})
	}
}
