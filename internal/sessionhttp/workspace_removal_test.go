package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceRemovalChecksBothStoresBeforeAnyMutation(t *testing.T) {
	for _, scenario := range []string{"db-home", "db-link", "disk-link", "db-descendant", "disk-descendant", "db-only-home"} {
		for _, query := range []string{"", "?confirm=true", "?confirm=true&delete_mode=group_only", "?confirm=true&delete_mode=contents", "?confirm=true&delete_sessions=true", "?confirm=true&delete_mode=contents&delete_sessions=true"} {
			t.Run(scenario+query, func(t *testing.T) {
				h, cleanup := createTestHandler(t)
				t.Cleanup(cleanup)
				fs, _ := newTestFileStore(t, h)
				t.Cleanup(func() { _ = fs.Close() })
				ctx := context.Background()
				root := &session.Workspace{ID: "root", Name: "Root", FolderSlug: "root", Kind: session.WorkspaceKindWorkspace}
				descendant := strings.Contains(scenario, "descendant")
				home := strings.Contains(scenario, "home")
				if descendant || home {
					root.Kind = session.WorkspaceKindGroup
				}
				protected := root
				if descendant {
					protected = &session.Workspace{ID: "child", Name: "Child", FolderSlug: "child", ParentID: root.ID}
					if scenario == "disk-descendant" {
						// Disk nesting can disagree with the DB; deleting the root
						// must still refuse before moving an unprotected sibling.
						protected.ParentID = ""
					}
				}
				if strings.HasPrefix(scenario, "db-") {
					protected.AssistantProgramJSON = assistantStateJSON(t, home)
				}
				if err := h.store.CreateWorkspace(ctx, root); err != nil {
					t.Fatal(err)
				}
				if descendant {
					if err := h.store.CreateWorkspace(ctx, protected); err != nil {
						t.Fatal(err)
					}
				}
				var paths []string
				if scenario != "db-only-home" {
					for _, row := range []*session.Workspace{root, protected} {
						if row == root && len(paths) > 0 {
							continue
						}
						folder := &workspace.Workspace{ID: row.ID, Name: row.Name, FolderSlug: row.FolderSlug, Kind: string(row.Kind), ParentID: row.ParentID}
						if row == protected && strings.HasPrefix(scenario, "disk-") {
							state, err := decodeWorkspaceAssistantState(assistantStateJSON(t, false))
							if err != nil {
								t.Fatal(err)
							}
							folder.AssistantProjectLink = state.Link
							if descendant {
								folder.ParentID = root.ID
							}
						}
						if err := fs.Save(folder); err != nil {
							t.Fatal(err)
						}
						path, err := fs.GetFolderPath(row.ID)
						if err != nil {
							t.Fatal(err)
						}
						paths = append(paths, filepath.Join(path, workspace.WorkspaceConfigFile))
					}
				}
				chat := &session.Session{ID: "chat", Title: "Keep", AgentName: "agent", FolderID: root.ID}
				if err := h.store.CreateSession(ctx, chat); err != nil {
					t.Fatal(err)
				}
				before, err := h.store.GetWorkspace(ctx, root.ID)
				if err != nil {
					t.Fatal(err)
				}
				recorder := httptest.NewRecorder()
				h.HandleWorkspaces(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+root.ID+query, nil))
				if recorder.Code != http.StatusConflict {
					t.Fatalf("removal = %d: %s", recorder.Code, recorder.Body.String())
				}
				var failure orihttp.APIError
				if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
					t.Fatal(err)
				}
				if failure.Code != assistantRemovalReviewRequired || !strings.Contains(failure.Message, "Review") {
					t.Fatalf("non-actionable failure: %+v", failure)
				}
				after, err := h.store.GetWorkspace(ctx, root.ID)
				if err != nil || after.Status != before.Status || after.Version != before.Version {
					t.Fatalf("root changed: %+v, %v", after, err)
				}
				retained, err := h.store.GetSession(ctx, chat.ID)
				if err != nil || retained.FolderID != root.ID {
					t.Fatalf("session deleted/unlinked: %+v, %v", retained, err)
				}
				for _, path := range paths {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("folder moved/deleted before review: %v", err)
					}
				}
				if descendant {
					child, err := h.store.GetWorkspace(ctx, protected.ID)
					if err != nil || child.ParentID != protected.ParentID {
						t.Fatalf("child un-nested/deleted: %+v, %v", child, err)
					}
				}
			})
		}
	}
}

func TestWorkspaceRemovalReviewDestinationAndTrashedHome(t *testing.T) {
	for _, status := range []session.WorkspaceStatus{session.WorkspaceStatusActive, session.WorkspaceStatusTrashed, session.WorkspaceStatusMissing} {
		h, cleanup := createTestHandler(t)
		t.Cleanup(cleanup)
		ctx := context.Background()
		for _, row := range []*session.Workspace{
			{ID: "home", Name: "Music Home", Kind: session.WorkspaceKindGroup, FolderSlug: "music-home", Status: status, AssistantProgramJSON: assistantStateJSON(t, true)},
			{ID: "song", Name: "Song", FolderSlug: "song", AssistantProgramJSON: assistantStateJSON(t, false)},
		} {
			if err := h.store.CreateWorkspace(ctx, row); err != nil {
				t.Fatal(err)
			}
		}
		recorder := httptest.NewRecorder()
		h.HandleWorkspaces(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/song?confirm=true", nil))
		var failure struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != http.StatusConflict || failure.Details["station_workspace_id"] != "home" {
			t.Fatalf("review failure = %+v, status %d", failure, recorder.Code)
		}
		if status == session.WorkspaceStatusActive {
			if failure.Details["review_home_slug"] != "music-home" {
				t.Fatal("missing review destination")
			}
		} else if failure.Details["review_home_slug"] != "" || !strings.Contains(strings.ToLower(failure.Message), "restore") {
			t.Fatalf("unavailable Home should offer recovery, not a broken page: %+v", failure)
		}
	}
}

func TestReviewedDisconnectAllowsGenericRemovalWithProductionStores(t *testing.T) {
	h, fixture, home, project := assistantPortfolioHTTPFixture(t)
	fs, _ := newTestFileStore(t, h)
	t.Cleanup(func() { _ = fs.Close() })
	store := workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(h.store), fs)
	h.SetWorkspaceTaskStore(store)
	for _, id := range []string{home.ID, project.ID} {
		current, err := fixture.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(current); err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	h.HandleWorkspaces(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+project.ID+"?confirm=true&delete_sessions=true", nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected protected project, got %d: %s", recorder.Code, recorder.Body.String())
	}

	programs := workspace.NewAssistantProgramStore(store)
	review, err := programs.ReviewDisconnect(home.ID, project.ID, home.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := programs.CommitDisconnect(home.ID, review.Token, "reviewed-delete"); err != nil {
		t.Fatal(err)
	}
	retained, err := store.Get(project.ID)
	if err != nil || retained.GetAssistantProjectLink() != nil || retained.ParentID != "" {
		t.Fatalf("reviewed disconnect did not detach project: %+v, %v", retained, err)
	}
	// An unrelated metadata sync after disconnect must not resurrect the link.
	row, err := h.store.GetWorkspace(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.syncWorkspacePortableStateToFileStore(row); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	h.HandleWorkspaces(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+project.ID+"?confirm=true&delete_sessions=true", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("reviewed project still blocked: %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, err := store.Get(project.ID); err == nil {
		t.Fatal("deleted project remains")
	}
	if _, err := store.Get(home.ID); err != nil {
		t.Fatalf("project removal deleted the Home: %v", err)
	}
}

func TestWorkspaceRemovalFailsClosedOnMalformedAssistantState(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	ctx := context.Background()
	row := &session.Workspace{ID: "bad", Name: "Bad", AssistantProgramJSON: json.RawMessage(`{"state":false}`)}
	if err := h.store.CreateWorkspace(ctx, row); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	h.HandleWorkspaces(recorder, httptest.NewRequest(http.MethodDelete, "/api/workspaces/bad?confirm=true&delete_sessions=true", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("malformed state permitted removal: %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, err := h.store.GetWorkspace(ctx, row.ID); err != nil {
		t.Fatal("malformed workspace was deleted")
	}
}
