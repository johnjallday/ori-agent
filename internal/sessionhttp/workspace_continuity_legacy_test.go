package sessionhttp

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/continuityprep"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// legacyHQCopy is what an older Ori left behind: the HQ folder with its
// workspace.json and entry profile, but no continuity checkpoint.
func legacyHQCopy(t *testing.T) (string, string) {
	t.Helper()
	at := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	source := newContinuityInstallation(t, "source")
	ws := sourceHQ(t, source, at)
	if status, err := source.worker.PrepareNow(t.Context(), ws.ID); err != nil || status.State != continuityprep.StateReady {
		t.Fatalf("source not ready: %+v %v", status, err)
	}
	folder, _ := source.files.GetFolderPath(ws.ID)
	copied := copyContinuityFolder(t, folder)
	if err := os.RemoveAll(filepath.Join(copied, ".ori", "continuity")); err != nil {
		t.Fatal(err)
	}
	return copied, ws.ID
}

func legacyImport(t *testing.T, dest *continuityInstallation, path string, adopt bool) (int, map[string]any) {
	t.Helper()
	return dest.request(t, http.MethodPost, "/api/workspaces/import", map[string]any{"path": path, "adopt_assistant": adopt})
}

// FR-28/FR-29: an older HQ copy says what it lacks and, confirmed, brings its
// assistant back from the folder's own evidence — paused, with the agreement's
// choices unknown, the HQ designated, routines off — and nothing else.
func TestLegacyHQImportAdoptsProvenAssistantOnlyWhenConfirmed(t *testing.T) {
	ctx := t.Context()
	copied, hq := legacyHQCopy(t)

	dest := newContinuityInstallation(t, "destination")
	review := reviewContinuityFolder(t, dest, copied)
	if review["status"] != "legacy" || review["legacy_workspace"] != true {
		t.Fatalf("an older Ori workspace was not recognized: %v", review)
	}
	assistant, _ := review["legacy_assistant"].(map[string]any)
	if assistant["display_name"] != "Ada" || review["legacy_adoption_blocked"] != nil {
		t.Fatalf("legacy assistant not offered: %v", review)
	}
	code, payload := legacyImport(t, dest, copied, true)
	if code != http.StatusCreated {
		t.Fatalf("legacy import: %d %v", code, payload)
	}
	adoption, _ := payload["assistant_adoption"].(map[string]any)
	if adoption["adopted"] != true || adoption["display_name"] != "Ada" {
		t.Fatalf("assistant not adopted: %v", payload)
	}
	state, err := personalassistant.NewSQLiteStore(dest.db).GetState(ctx, "local")
	if err != nil || state.AssistantID != "assistant-1" || state.Status != personalassistant.StatusPaused ||
		state.HQWorkspaceID != hq || state.HQEntryAgentInstanceID != "entry-1" || state.Mandate != "" || len(state.FocusAreas) != 0 {
		t.Fatalf("legacy adoption: %+v %v", state, err)
	}
	var designated string
	if err := dest.db.QueryRowContext(ctx, `SELECT personal_workspace_id FROM users WHERE id='local'`).Scan(&designated); err != nil || designated != hq {
		t.Fatalf("HQ not designated: %q %v", designated, err)
	}
	attachment, _ := dest.local.Attachment(ctx, hq)
	if attachment.State != workspacecontinuity.ImportedInactive || attachment.Disposition != workspacecontinuity.AdoptedHQ {
		t.Fatalf("legacy HQ not marked an adopted import: %+v", attachment)
	}
	if policy, _ := dest.local.Policy(ctx, hq); policy.Automatic || !policy.Manual {
		t.Fatalf("legacy HQ admitted background routines: %+v", policy)
	}

	// Without the explicit choice the same copy is an ordinary import.
	plain := newContinuityInstallation(t, "plain")
	if code, payload := legacyImport(t, plain, copied, false); code != http.StatusCreated || payload["assistant_adoption"] != nil {
		t.Fatalf("plain legacy import: %d %v", code, payload)
	}
	if _, err := personalassistant.NewSQLiteStore(plain.db).GetState(ctx, "local"); err == nil {
		t.Fatal("an assistant was adopted without the user's choice")
	}
}

// An incumbent keeps its place; a copy whose profile does not prove the
// presented assistant offers nothing and adopts nothing.
func TestLegacyHQImportRefusesIncumbentAndContradictoryEvidence(t *testing.T) {
	ctx := t.Context()
	copied, _ := legacyHQCopy(t)

	incumbentHome := newContinuityInstallation(t, "incumbent")
	incumbent := personalassistant.NewState("local")
	incumbent.AssistantID, incumbent.Status, incumbent.DisplayName = "incumbent", personalassistant.StatusAwaitingHQ, "Bea"
	incumbent.Appearance, incumbent.GlobalAgentProfileName = types.NewAgentAppearance(), "Bea"
	hired := time.Date(2026, 8, 1, 7, 0, 0, 0, time.UTC)
	incumbent.HiredAt = &hired
	if _, err := personalassistant.NewSQLiteStore(incumbentHome.db).CreateState(ctx, incumbent); err != nil {
		t.Fatal(err)
	}
	review := reviewContinuityFolder(t, incumbentHome, copied)
	if review["legacy_adoption_blocked"] != "existing_assistant" {
		t.Fatalf("incumbent did not block adoption: %v", review)
	}
	_, payload := legacyImport(t, incumbentHome, copied, true)
	adoption, _ := payload["assistant_adoption"].(map[string]any)
	if adoption["adopted"] != false || adoption["reason"] != "existing_assistant" {
		t.Fatalf("incumbent adoption outcome: %v", payload)
	}
	if state, err := personalassistant.NewSQLiteStore(incumbentHome.db).GetState(ctx, "local"); err != nil || state.AssistantID != "incumbent" {
		t.Fatalf("incumbent changed: %+v %v", state, err)
	}

	// Rewrite the copied profile so it no longer names the presented assistant.
	profilePath := filepath.Join(copied, "agents", "ada", "config.json")
	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.ReplaceAll(string(data), personalassistant.ProfileAssistantMarker("assistant-1"),
		personalassistant.ProfileAssistantMarker("someone-else"))
	if err := os.WriteFile(profilePath, []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	other := newContinuityInstallation(t, "contradictory")
	code, reviewPayload := other.request(t, http.MethodGet, "/api/workspaces/import/check?path="+url.QueryEscape(copied), nil)
	contradictory, _ := reviewPayload["continuity"].(map[string]any)
	if code != http.StatusOK || contradictory["legacy_assistant"] != nil {
		t.Fatalf("contradictory evidence was offered: %d %v", code, contradictory)
	}
	_, payload = legacyImport(t, other, copied, true)
	adoption, _ = payload["assistant_adoption"].(map[string]any)
	if adoption["adopted"] != false || adoption["reason"] != "identity_incomplete" {
		t.Fatalf("contradictory adoption outcome: %v", payload)
	}
	if _, err := personalassistant.NewSQLiteStore(other.db).GetState(ctx, "local"); err == nil {
		t.Fatal("contradictory evidence adopted an assistant")
	}
}
