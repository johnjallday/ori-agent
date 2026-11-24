//go:build darwin

package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filesearch"
)

// An anchor lives in managed storage; its external directory does not become
// managed storage. Neither an accepted workspace nor a search grant proves
// permission for the other store.
func TestExternalAnchorAndSearchHaveIndependentWriteBoundaries(t *testing.T) {
	h, managed, _, cleanup := templateTestEnv(t)
	defer cleanup()
	// Keep the managed store inside the synthetic home: otherwise even a
	// missing policy guard would reject the sidecar as outside the home.
	base, err := filepath.EvalSymlinks(managed)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Dir(base)
	root := filepath.Join(home, "External")
	data := filepath.Join(home, "Ori Data")
	for _, p := range []string{root, data} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	pick := root
	service, err := filesearch.New(t.Context(), filesearch.Options{DB: h.store.DB().DB, Policy: func() filesearch.Policy { return filesearch.Policy{Home: home, Protected: []string{managed, data}} }, Pick: func(context.Context) (string, bool, error) { return pick, true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	choose := func(path string) (*filesearch.Source, error) {
		pick = path
		id, e := service.Begin("local", "custom")
		if e != nil {
			return nil, e
		}
		return service.Select(t.Context(), "local", id)
	}
	counts := func(workspaces, sources int) {
		t.Helper()
		ws, e := h.store.ListWorkspaces(t.Context())
		if e != nil || len(ws) != workspaces {
			t.Fatalf("workspaces = %d, err = %v", len(ws), e)
		}
		ss, e := service.Sources(t.Context(), "local")
		if e != nil || len(ss) != sources {
			t.Fatalf("sources = %d, err = %v", len(ss), e)
		}
	}
	counts(0, 0)
	op, digest := reviewCreationOperation(t, h, map[string]any{"name": "Different name", "anchor_path": root, "blank": true, "create_template_agents": false})
	counts(0, 0)
	before, e := choose(root)
	if e != nil {
		t.Fatal(e)
	}
	counts(0, 1)
	w, body := creationOperationRequest(t, h, http.MethodPost, "/"+op+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != 201 {
		t.Fatalf("confirm %d: %s", w.Code, w.Body)
	}
	counts(1, 1)
	id := body["folder"].(map[string]any)["id"].(string)
	ws, e := h.workspaceStore.Get(id)
	if e != nil {
		t.Fatal(e)
	}
	ref, e := ws.GetDirectoryReference(ws.AnchorDirectoryReferenceID())
	if e != nil || ref.Path != root || ref.WorkspaceID != id || ws.OwnerUserID != "local" {
		t.Fatalf("missing owned anchor: %v", e)
	}
	after, e := choose(root)
	if e != nil || after.ID != before.ID || after.Generation != before.Generation {
		t.Fatalf("linked root changed search authority: %v", e)
	}
	sidecar, e := h.workspaceStore.GetFolderPath(id)
	if e != nil {
		t.Fatal(e)
	}
	realSidecar, e := filepath.EvalSymlinks(sidecar)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{realSidecar, data} {
		if _, e := choose(p); !errors.Is(e, filesearch.ErrRoot) {
			t.Fatalf("managed root accepted: %v", e)
		}
	}
	if err := service.Remove(t.Context(), "local", before.ID); err != nil {
		t.Fatal(err)
	}
	counts(1, 0)
}

func TestSearchableAnchorUsesOneReviewedSelection(t *testing.T) {
	for _, consent := range []bool{true, false} {
		h, managed, _, cleanup := templateTestEnv(t)
		t.Cleanup(cleanup)
		home := canonicalAnchorTestDir(t)
		root := filepath.Join(home, "External")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		s, e := filesearch.New(t.Context(), filesearch.Options{DB: h.store.DB().DB, Policy: func() filesearch.Policy { return filesearch.Policy{Home: home, Protected: []string{managed}} }, Pick: func(context.Context) (string, bool, error) { return root, true, nil }})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(s.Close)
		h.SetFolderSearch(s, func(_ *http.Request, owner string) bool { return owner == "local" })
		intent, e := s.Begin("local", "custom")
		if e != nil {
			t.Fatal(e)
		}
		review, e := s.Review(t.Context(), "local", intent)
		if e != nil {
			t.Fatal(e)
		}
		// Even with a live token, legacy unreviewed create cannot opt in.
		direct, _ := postCreateWorkspace(t, h, `{"name":"No review","allow_filename_search":true}`)
		if direct.Code != http.StatusConflict {
			t.Fatal("unreviewed search effect was accepted")
		}
		op, digest := reviewCreationOperation(t, h, map[string]any{"name": "My archive", "folder_selection": review.ID, "allow_filename_search": consent, "blank": true, "create_template_agents": false, "existing_agent_names": []string{}})
		if ss, e := s.Sources(t.Context(), "local"); e != nil || len(ss) != 0 {
			t.Fatal("review granted search", e)
		}
		if ws, e := h.store.ListWorkspaces(t.Context()); e != nil || len(ws) != 0 {
			t.Fatal("review created workspace", e)
		}
		w, body := creationOperationRequest(t, h, http.MethodPost, "/"+op+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusCreated {
			t.Fatalf("confirm %d %s", w.Code, w.Body)
		}
		id := body["folder"].(map[string]any)["id"].(string)
		ws, e := h.workspaceStore.Get(id)
		if e != nil {
			t.Fatal(e)
		}
		ref, e := ws.GetDirectoryReference(ws.AnchorDirectoryReferenceID())
		if e != nil || ref.WorkspaceID != id || ref.Path != root {
			t.Fatal("wrong anchor", e)
		}
		ss, e := s.Sources(t.Context(), "local")
		if e != nil {
			t.Fatal(e)
		}
		if consent {
			if len(ss) != 1 || ss[0].ID != body["filename_source_id"] || ref.ID != body["directory_reference_id"] {
				t.Fatal("receipt did not identify persisted anchor and grant")
			}
		} else if len(ss) != 0 {
			t.Fatal("created grant without consent")
		}
		h.SetFolderSearch(s, func(*http.Request, string) bool { return false })
		denied, body := creationOperationRequest(t, h, http.MethodGet, "/"+op, nil)
		if denied.Code != http.StatusForbidden || body["result"] != nil {
			t.Fatal("local-only review was disclosed")
		}
	}
}
