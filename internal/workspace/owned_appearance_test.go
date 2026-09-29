package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

type ownedAppearanceFunc func(context.Context, string, string) ([]byte, error)

func (f ownedAppearanceFunc) ReadOwnedAgentAppearance(ctx context.Context, name, filename string) ([]byte, error) {
	return f(ctx, name, filename)
}

func TestOwnedAppearanceNativeSnapshotIsSelfContained(t *testing.T) {
	local, old, ws, folder := localConfigFixture(t)
	files, err := NewFileStoreWithLocalConfig(old.BasePath(), local)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	globalRoot := t.TempDir()
	globals, err := store.NewFileStore(filepath.Join(globalRoot, "agents.json"), types.Settings{})
	localConfigMust(t, err)
	source := syntheticPrivateAgent()
	source.Appearance.SetUpload("guide.png")
	localConfigMust(t, globals.SetAgent("Guide", source))
	image := tinyContinuityImage(t)
	localConfigMust(t, os.WriteFile(filepath.Join(globalRoot, "agents", "Guide", "guide.png"), image, 0600))
	wrapped := NewAgentSnapshotStore(files, globals)
	wrapped.SnapshotReferencedAgents(ws)
	got, exists, err := files.GetWorkspaceAgent(ws.ID, "Guide")
	localConfigMust(t, err)
	if !exists || got.WorkspaceLocalConfigID == "" || got.Settings.APIKey != source.Settings.APIKey || source.WorkspaceLocalConfigID != "" {
		t.Fatal("snapshot did not preserve private native settings without changing the global definition")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/agents", nil)
	request.SetPathValue("workspaceID", ws.ID)
	response := httptest.NewRecorder()
	NewHTTPHandler(wrapped, nil, nil).ListWorkspaceAgentProfiles(response, request)
	var profiles struct {
		Agents []WorkspaceAgentProfile `json:"agents"`
	}
	localConfigMust(t, json.Unmarshal(response.Body.Bytes(), &profiles))
	if response.Code != http.StatusOK || len(profiles.Agents) != 1 || profiles.Agents[0].AppearanceWorkspaceID != ws.ID || bytes.Contains(response.Body.Bytes(), []byte(source.Settings.APIKey)) {
		t.Fatal("profile API did not provide a private-field-free scoped appearance")
	}
	data, err := wrapped.ReadWorkspaceAppearance(t.Context(), ws.ID, "Guide", "guide.png")
	localConfigMust(t, err)
	if !bytes.Equal(data, image) {
		t.Fatal("scoped image bytes differ")
	}
	for _, name := range []string{"agents/guide/config.json", "agents/guide/appearance/guide.png"} {
		info, err := os.Stat(filepath.Join(folder, name))
		localConfigMust(t, err)
		if info.Mode().Perm() != 0600 {
			t.Fatal("snapshot file is not private", name)
		}
	}
	// No runtime dependence on the old library, no refresh by matching name.
	localConfigMust(t, os.Remove(filepath.Join(globalRoot, "agents", "Guide", "guide.png")))
	wrapped.SnapshotReferencedAgents(ws)
	data, err = wrapped.ReadWorkspaceAppearance(t.Context(), ws.ID, "Guide", "guide.png")
	localConfigMust(t, err)
	if !bytes.Equal(data, image) {
		t.Fatal("snapshot followed the global image")
	}
	local.secrets = unavailableLocalSecrets{local.secrets}
	if _, err := wrapped.ReadWorkspaceAppearance(t.Context(), ws.ID, "Guide", "guide.png"); err != nil {
		t.Fatal("read-only appearance required decrypting a provider key", err)
	}
	barriers, err := workspacecontinuity.NewLocalStore(local.db).FileMutationBarriers(t.Context(), ws.ID)
	localConfigMust(t, err)
	if len(barriers) != 0 {
		t.Fatal("successful seed left pending work")
	}
}

func TestOwnedAppearanceMissingCollisionAndAdmissionAreExplicit(t *testing.T) {
	local, old, ws, folder := localConfigFixture(t)
	files, err := NewFileStoreWithLocalConfig(old.BasePath(), local)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	source := syntheticPrivateAgent()
	source.Appearance.SetUpload("guide.png")
	missing := ownedAppearanceFunc(func(context.Context, string, string) ([]byte, error) { return nil, workspacecontinuity.ErrIncomplete })
	localConfigMust(t, files.seedNativeAgent(t.Context(), ws.ID, "Guide", source, missing))
	if _, err := files.ReadWorkspaceAppearance(t.Context(), ws.ID, "Guide", "guide.png"); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("missing image was replaced or concealed", err)
	}
	if err := files.seedNativeAgent(t.Context(), ws.ID, "Guide", source, missing); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("existing definition was reseeded", err)
	}
	// A retained inactive upload is still addressable, but only under its exact
	// profile. Another filename or profile cannot select arbitrary owned bytes.
	localConfigMust(t, workspacecontinuity.ReplaceCanonicalFile(t.Context(), folder, "agents/guide/appearance/guide.png", "", tinyContinuityImage(t)))
	for _, pair := range [][2]string{{"Other", "guide.png"}, {"Guide", "other.png"}, {"../Guide", "guide.png"}, {"Guide", "../guide.png"}} {
		if _, err := files.ReadWorkspaceAppearance(t.Context(), ws.ID, pair[0], pair[1]); err == nil {
			t.Fatal("unowned path was readable", pair)
		}
	}
	_, err = local.db.ExecContext(t.Context(), `UPDATE continuity_attachments SET state='detached' WHERE workspace_id=?`, ws.ID)
	localConfigMust(t, err)
	if _, err := files.ReadWorkspaceAppearance(t.Context(), ws.ID, "Guide", "guide.png"); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatal("detached profile remained readable through admission", err)
	}
	called := false
	reader := ownedAppearanceFunc(func(context.Context, string, string) ([]byte, error) {
		called = true
		return tinyContinuityImage(t), nil
	})
	if err := files.seedNativeAgent(t.Context(), ws.ID, "Other", source, reader); err == nil || called {
		t.Fatal("detachment did not precede source lookup")
	}
}

func TestOwnedAppearanceRefusesOrphansAndKeepsFailedBarrier(t *testing.T) {
	local, old, ws, folder := localConfigFixture(t)
	files, err := NewFileStoreWithLocalConfig(old.BasePath(), local)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	source := syntheticPrivateAgent()
	source.Appearance.SetUpload("guide.png")
	image := tinyContinuityImage(t)
	location := "agents/guide/appearance/guide.png"
	localConfigMust(t, workspacecontinuity.ReplaceCanonicalFile(t.Context(), folder, location, "", image))
	reader := ownedAppearanceFunc(func(context.Context, string, string) ([]byte, error) { return image, nil })
	if err := files.seedNativeAgent(t.Context(), ws.ID, "Guide", source, reader); !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatal("orphan image was silently claimed", err)
	}
	if _, err := os.Stat(filepath.Join(folder, "agents", "guide", "config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed seed installed a profile")
	}
	barriers, err := workspacecontinuity.NewLocalStore(local.db).FileMutationBarriers(t.Context(), ws.ID)
	localConfigMust(t, err)
	if len(barriers) != 1 || !barriers[0].Failed {
		t.Fatal("failed seed lost its durable barrier", barriers)
	}
	data, err := workspacecontinuity.ReadCanonicalFile(t.Context(), folder, location, maxContinuityAppearanceBytes)
	localConfigMust(t, err)
	if !bytes.Equal(data, image) {
		t.Fatal("collision changed the existing file")
	}
}
