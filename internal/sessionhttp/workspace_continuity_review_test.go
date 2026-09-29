package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func publishSavedWorkReviewFixture(t *testing.T, root, wsID string, withSQLMetadata bool, children ...string) *agentworkspace.Workspace {
	t.Helper()
	ws := exportedWorkspaceFixture(wsID, "Saved work", "")
	ws.OwnerUserID, ws.Version, ws.TicketSequence = "local", 8, 1
	ws.Tasks = []agentworkspace.Task{
		{ID: "finished", WorkspaceID: ws.ID, Status: agentworkspace.TaskStatusCompleted, CreatedAt: ws.CreatedAt, Result: "saved result", TicketNumber: 1},
		{ID: "interrupted", WorkspaceID: ws.ID, Status: agentworkspace.TaskStatusInProgress, CreatedAt: ws.CreatedAt},
	}
	ws.Messages = []agentworkspace.AgentMessage{{ID: "collaboration", Timestamp: ws.CreatedAt, Content: "Private conversation"}}
	ws.Attachments = []agentworkspace.Attachment{{ID: "note", WorkspaceID: ws.ID, Type: agentworkspace.AttachmentTypeDoc,
		CreatedAt: ws.CreatedAt, UpdatedAt: ws.UpdatedAt, Body: "Private note"}}
	ws.DirectoryReferences = []agentworkspace.DirectoryReference{{ID: "external", WorkspaceID: ws.ID, Path: "/old-machine/private", Name: "Reference"}}
	writeExportedWorkspaceFixture(t, root, ws)
	canonical, err := os.ReadFile(filepath.Join(root, agentworkspace.WorkspaceConfigFile)) // #nosec G304 -- test-owned workspace fixture
	if err != nil {
		t.Fatal(err)
	}
	record, err := agentworkspace.SnapshotContinuityWorkspace(canonical)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor agentworkspace.ContinuityWorkspace
	if err := workspacecontinuity.DecodeRecord(record, &descriptor); err != nil {
		t.Fatal(err)
	}
	if withSQLMetadata {
		descriptor.SQLMetadata = &agentworkspace.ContinuityWorkspaceSQLMetadata{Color: ""}
	}
	record, err = workspacecontinuity.EncodeRecord(ws.ID, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	chunk, ref, err := workspacecontinuity.EncodeChunk(workspacecontinuity.Chunk{
		Version: workspacecontinuity.Version, WorkspaceID: ws.ID, Domain: "workspace", Family: "workspaces",
		Records: []workspacecontinuity.Record{record},
	})
	if err != nil {
		t.Fatal(err)
	}
	files := []workspacecontinuity.Fingerprint{{Path: agentworkspace.WorkspaceConfigFile, Digest: workspacecontinuity.Digest(canonical), Bytes: int64(len(canonical))}}
	manifest := workspacecontinuity.Manifest{
		Version: workspacecontinuity.Version, Generation: uuid.NewString(), WorkspaceID: ws.ID,
		CheckpointAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), SourceRevision: 8,
		Files: files, Children: children, SourceFingerprint: workspacecontinuity.FilesDigest(files),
	}
	for _, domain := range workspacecontinuity.DomainNames() {
		component := workspacecontinuity.Component{Domain: domain, Version: workspacecontinuity.Version, Availability: workspacecontinuity.Empty}
		if domain == "workspace" {
			component.Availability = workspacecontinuity.Present
			component.Counts = map[string]int64{"workspaces": 1}
			component.Chunks = []workspacecontinuity.ChunkRef{ref}
		}
		manifest.Components = append(manifest.Components, component)
	}
	if err := workspacecontinuity.Publish(t.Context(), root, manifest,
		func(_ context.Context, digest string) (io.ReadCloser, error) {
			if digest != ref.Digest {
				return nil, errors.New("unknown synthetic object")
			}
			return io.NopCloser(bytes.NewReader(chunk)), nil
		}, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestContinuityImportCheckRejectsUnclaimedMemoryAndNotesBeforeLegacyWrites(t *testing.T) {
	for _, variant := range []string{"memory", "note", "linked_note"} {
		t.Run(variant, func(t *testing.T) {
			if variant == "linked_note" && runtime.GOOS == "windows" {
				t.Skip("symlinks require platform permissions")
			}
			h, cleanup := createTestHandler(t)
			defer cleanup()
			root := filepath.Join(t.TempDir(), "prepared")
			ws := publishSavedWorkReviewFixture(t, root, "unclaimed-private-work", true)
			if variant != "memory" {
				if err := os.Mkdir(filepath.Join(root, agentworkspace.NotesDir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			switch variant {
			case "memory":
				if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("unclaimed memory"), 0600); err != nil {
					t.Fatal(err)
				}
			case "note":
				if err := os.WriteFile(filepath.Join(root, agentworkspace.NotesDir, "private.md"), []byte("unclaimed note"), 0600); err != nil {
					t.Fatal(err)
				}
			case "linked_note":
				if err := os.Symlink(root, filepath.Join(root, agentworkspace.NotesDir, "linked.md")); err != nil {
					t.Fatal(err)
				}
			}
			// A valid pointer does not prove physical files outside its list.
			if _, err := workspacecontinuity.Inspect(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			code, response := requestImportReview(t, h, root, true, false)
			if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "unavailable" {
				t.Fatal("unclaimed bytes looked complete", code, response)
			}
			code, _ = requestImportReview(t, h, root, false, true)
			if code != http.StatusConflict {
				t.Fatal("legacy override imported unclaimed bytes", code)
			}
			if _, err := h.store.GetWorkspace(t.Context(), ws.ID); err == nil {
				t.Fatal("review wrote a canonical workspace")
			}
		})
	}
}

func TestContinuityImportCheckRequiresExactOwnedAssetTree(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	root := filepath.Join(t.TempDir(), "prepared")
	publishSavedWorkReviewFixture(t, root, "reviewed-owned-assets", true)
	asset := []byte("private synthetic attached asset")
	if err := os.MkdirAll(filepath.Join(root, agentworkspace.FilesDir, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, agentworkspace.FilesDir, "reports", "result.txt"), asset, 0600); err != nil {
		t.Fatal(err)
	}
	inspected, err := workspacecontinuity.Inspect(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	} // a pointer alone does not enumerate physical assets
	manifest := inspected.Manifest
	manifest.Generation = uuid.NewString()
	manifest.Files = append(manifest.Files, workspacecontinuity.Fingerprint{Path: "files/reports/result.txt", Digest: workspacecontinuity.Digest(asset), Bytes: int64(len(asset))})
	manifest.SourceFingerprint = workspacecontinuity.FilesDigest(manifest.Files)
	chunk := inspected.Manifest.Components[0].Chunks[0]
	payload, err := os.ReadFile(filepath.Join(root, ".ori", "continuity", "objects", chunk.Digest)) // #nosec G304 -- fixed test-owned checkpoint root and digest from the inspected synthetic manifest
	if err != nil {
		t.Fatal(err)
	}
	if err := workspacecontinuity.Publish(t.Context(), root, manifest, func(_ context.Context, digest string) (io.ReadCloser, error) {
		if digest != chunk.Digest {
			return nil, workspacecontinuity.ErrIncomplete
		}
		return io.NopCloser(bytes.NewReader(payload)), nil
	}, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	inspected, err = workspacecontinuity.Inspect(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	code, response := requestImportReview(t, h, root, true, false)
	if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "review_required" {
		t.Fatal("declared nested asset rejected", code, response)
	}
	if saved := response["continuity"].(map[string]any)["saved_work"].(map[string]any); saved["owned_files"] != float64(1) {
		t.Fatal("verified owned-file count was omitted", saved)
	}
	// An extra empty directory holds no data and is not part of a copy's
	// reviewed files; an extra file or link is.
	for _, variant := range []string{"extra_file", "linked_asset"} {
		if variant == "linked_asset" && runtime.GOOS == "windows" {
			continue // symlinks require platform permissions
		}
		location := filepath.Join(root, agentworkspace.FilesDir, "reports", variant)
		switch variant {
		case "extra_file":
			if err := os.WriteFile(location, []byte("unclaimed"), 0600); err != nil {
				t.Fatal(err)
			}
		case "extra_directory":
			if err := os.Mkdir(location, 0700); err != nil {
				t.Fatal(err)
			}
		case "linked_asset":
			if err := os.Symlink(root, location); err != nil {
				t.Fatal(err)
			}
		}
		code, response = requestImportReview(t, h, root, true, false)
		if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "unavailable" {
			t.Fatal("unclaimed asset was advertised as complete", variant, code, response)
		}
		code, _ = requestImportReview(t, h, root, false, true)
		if code != http.StatusConflict {
			t.Fatal("legacy override imported unclaimed asset", variant, code)
		}
		inert := t.TempDir()
		if err := os.Chmod(inert, 0700); err != nil {
			t.Fatal(err)
		}
		if n, err := agentworkspace.StageContinuityOwnedFiles(t.Context(), root, inert, inspected); err == nil || n != 0 {
			t.Fatal("incomplete assets staged", variant, n, err)
		}
		if err := os.Remove(location); err != nil {
			t.Fatal(err)
		}
	}
	stage := t.TempDir()
	if err := os.Chmod(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if n, err := agentworkspace.StageContinuityOwnedFiles(t.Context(), root, stage, inspected); err != nil || n != 1 {
		t.Fatal("declared owned asset failed private staging", n, err)
	}
	staged, err := workspacecontinuity.ReadCanonicalFile(t.Context(), stage, "files/reports/result.txt", workspacecontinuity.MaxChunkBytes)
	if err != nil || !bytes.Equal(staged, asset) {
		t.Fatal("staged asset changed", err)
	}
	if _, err := agentworkspace.StageContinuityOwnedFiles(t.Context(), root, stage, inspected); !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatal("staging overwrote existing asset", err)
	}
	if err := os.WriteFile(filepath.Join(root, "files", "reports", "result.txt"), []byte("changed synthetic attached asset"), 0600); err != nil {
		t.Fatal(err)
	}
	changedStage := t.TempDir()
	if err := os.Chmod(changedStage, 0700); err != nil {
		t.Fatal(err)
	}
	if n, err := agentworkspace.StageContinuityOwnedFiles(t.Context(), root, changedStage, inspected); !errors.Is(err, workspacecontinuity.ErrChanged) || n != 0 {
		t.Fatal("stale reviewed asset was installed", n, err)
	}
}

func TestContinuityImportCheckReadsTypedSavedWorkWithoutExposingContent(t *testing.T) {
	for _, withSQL := range []bool{true, false} {
		t.Run(fmt.Sprintf("sql_metadata_%t", withSQL), func(t *testing.T) {
			h, cleanup := createTestHandler(t)
			defer cleanup()
			root := filepath.Join(t.TempDir(), "prepared")
			ws := publishSavedWorkReviewFixture(t, root, "reviewed-work", withSQL)
			code, response := requestImportReview(t, h, root, true, false)
			if code != http.StatusOK {
				t.Fatal(code, response)
			}
			review := response["continuity"].(map[string]any)
			if !withSQL {
				if review["status"] != "unavailable" || review["saved_work"] != nil {
					t.Fatal("file-only descriptor appeared restorable", review)
				}
			} else {
				if review["status"] != "review_required" || review["workspace_id"] != ws.ID {
					t.Fatal(review)
				}
				work := review["saved_work"].(map[string]any)
				for key, want := range map[string]float64{"tasks": 2, "completed_tasks": 1, "interrupted_tasks": 1, "collaboration_messages": 1, "attachments": 1, "external_references": 1} {
					if work[key] != want {
						t.Fatal("inaccurate saved work", key, work)
					}
				}
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte("Private conversation")) || bytes.Contains(encoded, []byte("Private note")) || bytes.Contains(encoded, []byte("/old-machine/private")) {
				t.Fatal("read-only review leaked private work content")
			}
			code, _ = requestImportReview(t, h, root, false, true)
			if code != http.StatusConflict {
				t.Fatal("review accepted legacy import", code)
			}
		})
	}
}

func requestImportReview(t *testing.T, h *Handler, path string, check, override bool) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"path": path, "allow_duplicate": override})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/workspaces/import"
	if check {
		endpoint += "/check"
	}
	r := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleWorkspaces(w, r)
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("%s response %d: %v: %s", endpoint, w.Code, err, w.Body.String())
	}
	return w.Code, response
}

func TestContinuityImportCheckShowsVerifiedMetadataWithoutAuthorizingLegacyImport(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	root := filepath.Join(t.TempDir(), "prepared")
	ws := exportedWorkspaceFixture("prepared-review", "Prepared", "")
	ws = publishSavedWorkReviewFixture(t, root, ws.ID, true)
	before, err := os.ReadFile(filepath.Join(root, agentworkspace.WorkspaceConfigFile)) // #nosec G304 -- test-owned prepared workspace
	if err != nil {
		t.Fatal(err)
	}
	code, response := requestImportReview(t, h, root, true, false)
	if code != http.StatusOK {
		t.Fatal(code, response)
	}
	review, ok := response["continuity"].(map[string]any)
	if !ok || review["status"] != "review_required" || review["import_supported"] != false || review["workspace_id"] != ws.ID || review["checkpoint_at"] != "2026-09-01T12:00:00Z" || review["modern_directories"] != float64(1) {
		t.Fatalf("read-only review omitted checkpoint evidence or promised an importer: %+v", review)
	}
	components, ok := review["components"].([]any)
	if !ok || len(components) != len(workspacecontinuity.DomainNames()) {
		t.Fatal("component availability missing", review)
	}
	get := httptest.NewRequest(http.MethodGet, "/api/workspaces/import/check?path="+url.QueryEscape(root), nil)
	getResponse := httptest.NewRecorder()
	h.HandleWorkspaces(getResponse, get)
	if getResponse.Code != http.StatusOK || !bytes.Contains(getResponse.Body.Bytes(), []byte(`"status":"review_required"`)) {
		t.Fatal("GET check did not use the read-only review path", getResponse.Code, getResponse.Body.String())
	}
	for _, override := range []bool{false, true} {
		code, response = requestImportReview(t, h, root, false, override)
		if code != http.StatusConflict || !strings.Contains(response["message"].(string), "reviewed restoration") {
			t.Fatal("legacy import accepted a modern checkpoint", code, response)
		}
	}
	assertReviewImportUntouched(t, h, root, ws.ID, before)
}

func assertReviewImportUntouched(t *testing.T, h *Handler, dir, id string, before []byte) {
	t.Helper()
	rows, err := h.store.ListWorkspaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id {
			t.Fatal("refused import registered workspace", id)
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, agentworkspace.WorkspaceConfigFile)) // #nosec G304 -- test-owned prepared workspace
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("refused import modified canonical work", err)
	}
}

func TestContinuityImportCheckDoesNotDowngradeDamagedOrChildCheckpoint(t *testing.T) {
	for _, mode := range []string{"missing_format", "corrupt_pointer", "child_prepared"} {
		t.Run(mode, func(t *testing.T) {
			h, cleanup := createTestHandler(t)
			defer cleanup()
			root := filepath.Join(t.TempDir(), "legacy-parent")
			ws := exportedWorkspaceFixture("legacy-review", "Legacy Parent", "")
			writeExportedWorkspaceFixture(t, root, ws)
			before, err := os.ReadFile(filepath.Join(root, agentworkspace.WorkspaceConfigFile)) // #nosec G304 -- test-owned workspace
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing_format", "corrupt_pointer":
				managed := filepath.Join(root, workspacecontinuity.Directory)
				if err := os.MkdirAll(managed, 0o750); err != nil {
					t.Fatal(err)
				}
				if mode == "corrupt_pointer" {
					if err := os.WriteFile(filepath.Join(managed, "format.json"), []byte(`{"format":"ori.workspace-continuity","version":1}`), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(managed, "current.json"), []byte(`{invalid`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "child_prepared":
				child := filepath.Join(root, agentworkspace.SubWorkspacesDir, "child")
				publishSavedWorkReviewFixture(t, child, "child-review", true)
			}
			code, response := requestImportReview(t, h, root, true, false)
			if code != http.StatusOK {
				t.Fatal(code, response)
			}
			review := response["continuity"].(map[string]any)
			want := "unavailable"
			if mode == "child_prepared" {
				want = "review_required"
			}
			if review["status"] != want || review["import_supported"] != false {
				t.Fatal("modern child/damage treated as legacy", review)
			}
			if mode == "child_prepared" && (review["contains_modern_child"] != true || review["workspace_id"] != nil) {
				t.Fatal("child identity was presented as the selected root", review)
			}
			code, response = requestImportReview(t, h, root, false, true)
			if code != http.StatusConflict {
				t.Fatal("duplicate override imported a modern child/damaged root", code, response)
			}
			assertReviewImportUntouched(t, h, root, ws.ID, before)
		})
	}
}

func TestContinuityImportTreeDigestBindsVerifiedPhysicalMembership(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	root := filepath.Join(t.TempDir(), "selected")
	firstChild := filepath.Join(root, agentworkspace.SubWorkspacesDir, "one")
	publishSavedWorkReviewFixture(t, firstChild, "child-bound", true)
	publishSavedWorkReviewFixture(t, root, "parent-bound", true, "child-bound")
	check := func() map[string]any {
		t.Helper()
		code, response := requestImportReview(t, h, root, true, false)
		if code != http.StatusOK {
			t.Fatal(code, response)
		}
		return response["continuity"].(map[string]any)
	}
	before := check()
	firstDigest, ok := before["tree_digest"].(string)
	if !ok || len(firstDigest) != 64 || before["status"] != "review_required" || before["modern_directories"] != float64(2) {
		t.Fatal("tree commitment missing verified members", before)
	}
	for key, want := range map[string]float64{"tasks": 4, "completed_tasks": 2, "interrupted_tasks": 2, "collaboration_messages": 2, "attachments": 2} {
		if before["saved_work"].(map[string]any)[key] != want {
			t.Fatal("selected tree omitted a verified child's saved work", key, before)
		}
	}
	if check()["tree_digest"] != firstDigest {
		t.Fatal("unchanged source produced a different tree commitment")
	}
	firstDestination, ok := before["destination_digest"].(string)
	if !ok || len(firstDestination) != 64 || check()["destination_digest"] != firstDestination {
		t.Fatal("read-only review did not bind the stable SQL destination", before)
	}
	if _, err := h.store.DB().ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES ('unrelated-incumbent','Incumbent',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	changedDestination := check()
	if changedDestination["tree_digest"] != firstDigest || changedDestination["destination_digest"] == firstDestination {
		t.Fatal("destination SQL change did not invalidate the preview version", changedDestination)
	}
	if err := os.Rename(firstChild, filepath.Join(root, agentworkspace.SubWorkspacesDir, "two")); err != nil {
		t.Fatal(err)
	}
	moved := check()
	if moved["status"] != "review_required" || moved["tree_digest"] == firstDigest {
		t.Fatal("moving an otherwise identical child retained its prior commitment", moved)
	}
	publishSavedWorkReviewFixture(t, filepath.Join(root, agentworkspace.SubWorkspacesDir, "extra"), "unexpected", true)
	damaged := check()
	if damaged["status"] != "unavailable" || damaged["tree_digest"] != nil || damaged["saved_work"] != nil {
		t.Fatal("mismatched physical members produced a usable saved-work count", damaged)
	}
}

func TestContinuityImportCheckRequiresExactDeclaredPhysicalChildren(t *testing.T) {
	t.Run("child_missing_sql_evidence", func(t *testing.T) {
		h, cleanup := createTestHandler(t)
		defer cleanup()
		root := filepath.Join(t.TempDir(), "selected")
		publishSavedWorkReviewFixture(t, filepath.Join(root, agentworkspace.SubWorkspacesDir, "child"), "child-1", false)
		publishSavedWorkReviewFixture(t, root, "review-parent", true, "child-1")
		code, response := requestImportReview(t, h, root, true, false)
		if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "unavailable" {
			t.Fatal("unverified child appeared reviewable", code, response)
		}
	})
	for _, test := range []struct {
		name     string
		declared []string
		actual   []string
		want     string
	}{
		{"exact", []string{"child-1"}, []string{"child-1"}, "review_required"},
		{"foreign_file", []string{"child-1"}, []string{"child-1"}, "unavailable"},
		{"missing", []string{"child-1"}, nil, "unavailable"},
		{"extra", nil, []string{"child-1"}, "unavailable"},
		{"wrong", []string{"child-1"}, []string{"other-child"}, "unavailable"},
		{"duplicate", []string{"child-1", "child-2"}, []string{"child-1", "child-1"}, "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, cleanup := createTestHandler(t)
			defer cleanup()
			root := filepath.Join(t.TempDir(), "selected")
			parent := exportedWorkspaceFixture("review-parent", "Parent", "")
			for i, id := range test.actual {
				childPath := filepath.Join(root, agentworkspace.SubWorkspacesDir, fmt.Sprintf("child-%d", i))
				publishSavedWorkReviewFixture(t, childPath, id, true)
			}
			publishSavedWorkReviewFixture(t, root, parent.ID, true, test.declared...)
			if test.name == "foreign_file" {
				if err := os.WriteFile(filepath.Join(root, agentworkspace.SubWorkspacesDir, "foreign.txt"), []byte("unclaimed child content"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			code, response := requestImportReview(t, h, root, true, false)
			if code != http.StatusOK {
				t.Fatal(code, response)
			}
			review := response["continuity"].(map[string]any)
			if review["status"] != test.want || review["import_supported"] != false {
				t.Fatal("declared and physical child membership were not reconciled", review)
			}
			if test.want == "unavailable" && review["workspace_id"] != nil {
				t.Fatal("incomplete tree was presented as reviewed identity", review)
			}
			code, _ = requestImportReview(t, h, root, false, true)
			if code != http.StatusConflict {
				t.Fatal("legacy import accepted modern tree", code)
			}
			rows, err := h.store.ListWorkspaces(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.ID == parent.ID {
					t.Fatal("refused tree registered its parent")
				}
			}
		})
	}
}

func TestContinuityImportCheckRejectsLinkedChildTreeWithoutRegistration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating directory symlinks requires additional Windows privileges")
	}
	h, cleanup := createTestHandler(t)
	defer cleanup()
	root := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "selected"), exportedWorkspaceFixture("linked-child-owner", "Selected", ""))
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, agentworkspace.SubWorkspacesDir)); err != nil {
		t.Fatal(err)
	}
	code, _ := requestImportReview(t, h, root, true, false)
	if code != http.StatusBadRequest {
		t.Fatal("read-only review followed an external child link", code)
	}
	code, _ = requestImportReview(t, h, root, false, true)
	if code != http.StatusBadRequest {
		t.Fatal("import bypassed an unsafe child link", code)
	}
	rows, err := h.store.ListWorkspaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "linked-child-owner" {
			t.Fatal("unsafe child tree was registered")
		}
	}
}

func TestContinuityImportCheckPreservesLegacyStatus(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	root := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "legacy"), exportedWorkspaceFixture("legacy-unchanged", "Legacy", ""))
	code, response := requestImportReview(t, h, root, true, false)
	if code != http.StatusOK {
		t.Fatal(code, response)
	}
	review := response["continuity"].(map[string]any)
	if review["status"] != "legacy" || review["import_supported"] != true {
		t.Fatal(review)
	}
}
