package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func TestPurchasedFilingPreparesOnlyTheReviewedSelection(t *testing.T) {
	for _, tc := range []struct{ name, outcome string }{{"batch", filejanitor.ScanOutcomeBatch}, {"empty", filejanitor.ScanOutcomeNoEligible}} {
		t.Run(tc.name, func(t *testing.T) {
			b, handler := filingPurchaseFixture(t)
			decodeFilingResult(t, postFilingJSON(handler, filingPurchasePath, filingConfirmBody(t, 7, false)), http.StatusOK, "delivered")
			claim, err := b.filingClaimStore().FindFilingClaim(t.Context(), userprofile.LocalUserID, "")
			if err != nil || claim == nil {
				t.Fatalf("purchase missing: %v", err)
			}
			folder := filepath.Join(os.Getenv("HOME"), "Sandbox Inbox")
			if err := os.Mkdir(folder, 0o750); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(folder, "invoice.pdf")
			if tc.name == "batch" {
				if err := os.WriteFile(file, []byte("not read or moved by a metadata review"), 0o600); err != nil {
					t.Fatal(err)
				}
				old := time.Now().Add(-time.Hour)
				if err := os.Chtimes(file, old, old); err != nil {
					t.Fatal(err)
				}
			}
			status, err := b.fileJanitorService.Status(claim.WorkspaceID)
			if err != nil || status.Suggestion == nil || !status.Suggestion.ReviewSelectedFolder {
				t.Fatalf("purchased setup is not reachable: %+v %v", status, err)
			}
			base := "/api/workspaces/" + claim.WorkspaceID + "/file-janitor"
			post := func(path string, req any) *httptest.ResponseRecorder {
				t.Helper()
				jsonBody, err := json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				return postFilingJSON(handler, path, jsonBody)
			}
			if w := post(base+"/setup", map[string]any{"path": folder, "paused": true}); w.Code < 400 {
				t.Fatalf("unreviewed setup accepted: %s", w.Body.String())
			}
			picked := post("/api/folder-picker/select-path", map[string]string{"path": folder, "workspace_id": claim.WorkspaceID})
			var selection struct {
				Token string `json:"selection_token"`
				Path  string `json:"path"`
			}
			if err := json.Unmarshal(picked.Body.Bytes(), &selection); err != nil || picked.Code != 200 || selection.Token == "" {
				t.Fatalf("pick: %d %s %v", picked.Code, picked.Body.String(), err)
			}
			reviewResponse := post(base+"/setup-review", map[string]any{"selection_token": selection.Token, "monitoring": false})
			var preview struct {
				Review filejanitor.LinkedSetupReview `json:"review"`
			}
			if err := json.Unmarshal(reviewResponse.Body.Bytes(), &preview); err != nil || reviewResponse.Code != 200 || preview.Review.State != "review" ||
				len(preview.Review.Candidates) != 1 || preview.Review.Candidates[0].Path != selection.Path || preview.Review.Terms.PrivacyMode != filejanitor.ContentModeMetadataOnly {
				t.Fatalf("review: %d %s %v", reviewResponse.Code, reviewResponse.Body.String(), err)
			}
			if _, err := os.Stat(filepath.Join(folder, "Filed")); !os.IsNotExist(err) {
				t.Fatalf("preview mutated folder: %v", err)
			}
			status, err = b.fileJanitorService.Status(claim.WorkspaceID)
			if err != nil || status.Settings.IsSetUp() || status.Settings.PendingFolderGrant != nil || status.Settings.SetupPreparation != nil {
				t.Fatalf("preview granted access: %+v %v", status.Settings, err)
			}
			if b.progressionEngine.HasCompleted(progression.ShowFolderQuestID) || b.progressionEngine.HasCompleted(progression.FolderFirstLookQuestID) {
				t.Fatal("preview completed missions without a result")
			}
			confirmation := map[string]any{"reference_id": preview.Review.SelectedReference, "review_revision": preview.Review.Revision, "monitoring": false}
			if w := post(base+"/prepare-review", confirmation); w.Code != http.StatusConflict {
				t.Fatalf("accepted a provisional folder without scoped selection: %d %s", w.Code, w.Body.String())
			}
			confirmation["selection_token"] = selection.Token
			response := post(base+"/prepare-review", confirmation)
			var prepared struct {
				Preparation filejanitor.SetupPreparationResult `json:"preparation"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &prepared); err != nil || response.Code != 200 || prepared.Preparation.Result == nil || prepared.Preparation.Result.Outcome != tc.outcome {
				t.Fatalf("prepared: %d %s %v", response.Code, response.Body.String(), err)
			}
			status = *prepared.Preparation.Status
			if !status.Settings.ManualScansOnly() || !status.Settings.AutomationApprovedAt.IsZero() || status.Settings.RootPath != selection.Path {
				t.Fatalf("setup did not preserve manual metadata review: %+v", status.Settings)
			}
			if tc.name == "batch" {
				if _, err := os.Stat(file); err != nil {
					t.Fatalf("source disappeared: %v", err)
				}
			}
			if !b.progressionEngine.HasCompleted(progression.ShowFolderQuestID) || !b.progressionEngine.HasCompleted(progression.FolderFirstLookQuestID) || craftBalance(t, b) != 14 {
				t.Fatal("real first result did not complete the two canonical outcomes")
			}
			delete(confirmation, "selection_token") // Existing preparation can recover without a live picker token.
			replayed := post(base+"/prepare-review", confirmation)
			var replay struct {
				Preparation filejanitor.SetupPreparationResult `json:"preparation"`
			}
			if err := json.Unmarshal(replayed.Body.Bytes(), &replay); err != nil || replayed.Code != 200 || replay.Preparation.Result == nil ||
				!replay.Preparation.Result.CompletedAt.Equal(prepared.Preparation.Result.CompletedAt) || craftBalance(t, b) != 14 {
				t.Fatalf("replay scanned or paid twice: %d %s %v", replayed.Code, replayed.Body.String(), err)
			}
		})
	}
}
