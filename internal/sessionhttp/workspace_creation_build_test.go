package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/linkcontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
)

func attachedCreationFixture(t *testing.T, folder bool) (*buildFixture, map[string]any, *fakeBuildFolders) {
	t.Helper()
	h, _, _, cleanup := templateTestEnv(t)
	t.Cleanup(cleanup)
	f := newBuildFixtureOn(t, h, false)
	build := f.start(t)
	folders := &fakeBuildFolders{root: t.TempDir()}
	if err := os.WriteFile(filepath.Join(folders.root, "source.txt"), []byte("source remains exact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.workspaceBuild.Folders = folders
	_, err := f.store.Mutate(t.Context(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		current := doc.Session(build.ID)
		if folder {
			observation := reaperObservation()
			observation.ID, observation.Folder = "ordinary-folder", "Synthetic notes"
			current.Folder = &personalassistant.BuildFolder{Observation: *observation}
		} else {
			current.Link = &personalassistant.BuildLink{Observation: linkcontext.Observation{
				Version: linkcontext.Version, ID: "ordinary-link", URL: "https://example.org/notes", Host: "example.org", Kind: linkcontext.KindPage, Title: "Synthetic notes", FetchedAt: f.store.Now(),
			}}
		}
		current.Touch(f.store.Now())
		build = *current
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, map[string]any{"name": "Reviewed source", "blank": true, "create_template_agents": false, "build_session_id": build.ID, "build_review_version": build.Version}, folders
}

func TestWorkspaceCreationBuild_AttachedSourceIsInertUntilExactConsentAndObservedWithoutReplay(t *testing.T) {
	for _, folder := range []bool{false, true} {
		t.Run(map[bool]string{false: "link", true: "folder"}[folder], func(t *testing.T) {
			f, material, folders := attachedCreationFixture(t, folder)
			h := f.handler
			id, digest := reviewCreationOperation(t, h, material)
			before, _ := h.store.ListWorkspaces(t.Context())
			if len(before) != 0 || f.provider.calls != 0 {
				t.Fatal("review created resources or inferred")
			}
			w, body := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
			if w.Code != http.StatusCreated {
				t.Fatalf("confirm %d: %s", w.Code, w.Body)
			}
			workspaceID := body["folder"].(map[string]any)["id"].(string)
			doc, _ := f.store.Read(t.Context(), "local")
			build := doc.Session(material["build_session_id"].(string))
			if build.Status != personalassistant.WorkspaceBuildCreated || build.CreatedWorkspaceID != workspaceID {
				t.Fatalf("wrong build outcome: %+v", build)
			}
			for range 3 {
				assertCreationOperationState(t, h, id, "complete")
				w, _ = creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
				if w.Code != http.StatusCreated {
					t.Fatalf("replay: %d %s", w.Code, w.Body)
				}
			}
			after, _ := h.store.ListWorkspaces(t.Context())
			if len(after) != 1 || f.provider.calls != 0 {
				t.Fatal("observation replayed domain work")
			}
			if folder {
				bytes, err := os.ReadFile(filepath.Join(folders.root, "source.txt"))
				if err != nil || string(bytes) != "source remains exact\n" {
					t.Fatal("source files changed")
				}
				resource, err := h.workspaceStore.Get(workspaceID)
				if err != nil || len(resource.DirectoryReferences) != 2 || resource.DirectoryReferences[1].Path != folders.root ||
					resource.SharedData["primary_directory_id"] != resource.DirectoryReferences[1].ID {
					t.Fatalf("wrong linked directory: %+v %v", resource, err)
				}
			} else {
				notes, _ := h.store.ListNotesByWorkspace(t.Context(), workspaceID)
				if len(notes) != 1 || notes[0].Name != "Sources" {
					t.Fatalf("link completion missing/repeated: %+v", notes)
				}
			}
		})
	}
}

func TestWorkspaceCreationBuild_ChangedExpiredBusyOrRecognizedSourcesRefuseBeforeEffects(t *testing.T) {
	for _, change := range []string{"version", "link_without_version", "detach", "expired", "recognized", "pending_attachment", "pending_inference"} {
		t.Run(change, func(t *testing.T) {
			f, material, folders := attachedCreationFixture(t, change == "expired" || change == "recognized")
			id, digest := reviewCreationOperation(t, f.handler, material)
			_, err := f.store.Mutate(t.Context(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
				build := doc.Session(material["build_session_id"].(string))
				switch change {
				case "version":
					build.Touch(f.store.Now())
				case "link_without_version":
					build.Link.Observation.URL = "https://example.org/replaced"
				case "detach":
					build.Link = nil
				case "recognized":
					build.Folder.Recognition = reaperRecognition()
				case "pending_attachment":
					build.LinkRequestID = "pending"
				case "pending_inference":
					build.InferenceID = "pending"
				case "expired":
					folders.rootErr = errors.New("held selection expired")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			w, _ := creationOperationRequest(t, f.handler, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
			if w.Code != http.StatusConflict {
				t.Fatalf("stale source accepted: %d %s", w.Code, w.Body)
			}
			assertCreationOperationState(t, f.handler, id, "refused")
			resources, _ := f.handler.store.ListWorkspaces(t.Context())
			if len(resources) != 0 || f.provider.calls != 0 {
				t.Fatal("stale confirmation had effects")
			}
		})
	}
}

func TestWorkspaceCreationBuild_TwoOperationsAndLegacyCreateCannotClaimOneBuildTwice(t *testing.T) {
	f, material, _ := attachedCreationFixture(t, false)
	first, digest := reviewCreationOperation(t, f.handler, material)
	material["name"] = "Competing source"
	second, secondDigest := reviewCreationOperation(t, f.handler, material)
	var wg sync.WaitGroup
	for _, operation := range [][2]string{{first, digest}, {second, secondDigest}} {
		wg.Go(func() {
			w, _ := creationOperationRequest(t, f.handler, http.MethodPost, "/"+operation[0]+"/confirm", map[string]any{"review_digest": operation[1]})
			if w.Code != http.StatusCreated && w.Code != http.StatusConflict {
				t.Errorf("unexpected outcome: %d %s", w.Code, w.Body)
			}
		})
	}
	wg.Wait()
	resources, _ := f.handler.store.ListWorkspaces(t.Context())
	if len(resources) != 1 {
		t.Fatalf("one build created %d resources", len(resources))
	}
	for _, action := range []string{"abandon", "pause", "link/detach"} {
		status, body := f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+material["build_session_id"].(string)+"/"+action, map[string]any{"version": material["build_review_version"]})
		if status != http.StatusConflict || body["code"] != "creation_build_accepted" {
			t.Fatalf("accepted source mutable: %d %+v", status, body)
		}
	}
	status, _ := f.do(t, http.MethodPost, "/api/workspaces", material)
	if status != http.StatusConflict {
		t.Fatalf("legacy create bypassed accepted source: %d", status)
	}
}

func TestWorkspaceCreationBuild_DurableClaimSurvivesIndependentControllerSnapshots(t *testing.T) {
	f, material, _ := attachedCreationFixture(t, false)
	first, _ := reviewCreationOperation(t, f.handler, material)
	material["name"] = "Second controller"
	second, _ := reviewCreationOperation(t, f.handler, material)
	doc, _ := f.store.Read(t.Context(), "local")
	accepted := 0
	for _, id := range []string{first, second} {
		// Independent store snapshots/mutexes: only the common SQLite journal
		// can prevent two accepted operations after concurrent process reads.
		h := New(f.handler.store)
		h.workspaceBuild = &WorkspaceBuildDeps{Store: &memoryBuildStore{doc: cloneBuildDocument(doc), now: f.store.Now()}}
		op, err := h.readWorkspaceCreationOperation(t.Context(), "local", id)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), creationOperationContextKey{}, op))
		if h.acceptWorkspaceCreationOperation(httptest.NewRecorder(), r, &session.Workspace{}, projecttemplates.Template{}, false) {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("independent snapshots accepted %d operations", accepted)
	}
}

func TestWorkspaceCreationBuild_UnreviewedAndForeignSourceCannotFallBackToPlainCreation(t *testing.T) {
	f, material, _ := attachedCreationFixture(t, false)
	body, _ := json.Marshal(material)
	response, _ := postCreateWorkspace(t, f.handler, string(body))
	if response.Code != http.StatusConflict {
		t.Fatalf("plain source create: %d %s", response.Code, response.Body)
	}
	f.handler.workspaceBuild.CurrentUserID = func(context.Context) (string, error) { return "foreign", nil }
	w, _ := creationOperationRequest(t, f.handler, http.MethodPost, "", map[string]any{"id": "00000000-0000-4000-8000-000000000001", "request": material})
	if w.Code != http.StatusConflict {
		t.Fatalf("foreign source review: %d %s", w.Code, w.Body)
	}
	resources, _ := f.handler.store.ListWorkspaces(t.Context())
	if len(resources) != 0 {
		t.Fatal("refusal created a plain fallback")
	}
}

// A failure saving the build invalidation after SQL acceptance must not release
// that durable claim, including when a controller is reconstructed.
type failedCreationBuildStore struct{ *memoryBuildStore }

func (s failedCreationBuildStore) Mutate(ctx context.Context, owner string, mutate func(*personalassistant.WorkspaceBuildDocument) error) (personalassistant.WorkspaceBuildDocument, error) {
	return s.memoryBuildStore.Mutate(ctx, owner, func(doc *personalassistant.WorkspaceBuildDocument) error {
		if err := mutate(doc); err != nil {
			return err
		}
		return errors.New("injected sidecar write failure")
	})
}

func TestWorkspaceCreationBuild_AcceptedWriteFailureNeverReleasesTheSource(t *testing.T) {
	f, material, _ := attachedCreationFixture(t, false)
	id, digest := reviewCreationOperation(t, f.handler, material)
	f.handler.workspaceBuild.Store = failedCreationBuildStore{f.store}
	w, _ := creationOperationRequest(t, f.handler, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected uncertainty: %d %s", w.Code, w.Body)
	}
	observer := New(f.handler.store)
	assertCreationOperationState(t, observer, id, "reconciliation_required")
	body, _ := json.Marshal(material)
	legacy, _ := postCreateWorkspace(t, observer, string(body))
	if legacy.Code != http.StatusConflict {
		t.Fatalf("reconstructed controller bypass: %d %s", legacy.Code, legacy.Body)
	}
	for range 2 {
		w, _ = creationOperationRequest(t, f.handler, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusConflict {
			t.Fatalf("accepted retry: %d", w.Code)
		}
	}
	resources, _ := f.handler.store.ListWorkspaces(t.Context())
	if len(resources) != 0 || f.provider.calls != 0 {
		t.Fatal("uncertainty ran creation")
	}
}
