package sessionhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// This source starts from real canonical SQL relationship/workspace rows, then
// reads them through the production collectors in ONE caller-owned SQL view.
// A test-only publication fence is not the production dirty-sequence worker.
func TestCanonicalAssistantCollectorsProduceDirectoryOnlyReview(t *testing.T) {
	ctx := t.Context()
	sourceDir := filepath.Join(t.TempDir(), "source-hq")
	publishAssistantReviewFixture(t, sourceDir, "valid")
	canonical, err := workspacecontinuity.ReadCanonicalFile(ctx, sourceDir, agentworkspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	var authored agentworkspace.Workspace
	if err := json.Unmarshal(canonical, &authored); err != nil {
		t.Fatal(err)
	}
	profileBytes, err := workspacecontinuity.ReadCanonicalFile(ctx, sourceDir, "agents/ada/config.json", workspacecontinuity.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	var scopedProfile agent.Agent
	if err := json.Unmarshal(profileBytes, &scopedProfile); err != nil {
		t.Fatal(err)
	}
	source, sourceCleanup := createTestHandler(t)
	defer sourceCleanup()
	db := source.store.DB()
	canonicalSQL := session.ConvertAgentWorkspace(&authored)
	if err := session.NewSQLiteStore(db).CreateWorkspace(ctx, canonicalSQL); err != nil {
		t.Fatal(err)
	}
	// Ordinary SQL creation assigns the source order; its canonical file is
	// saved from that same final state before taking the shared read view.
	authored.OrderIndex = canonicalSQL.OrderIndex
	updated, err := authored.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := workspacecontinuity.ReplaceCanonicalFile(ctx, sourceDir, agentworkspace.WorkspaceConfigFile, workspacecontinuity.Digest(canonical), updated); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE users SET personal_workspace_id=? WHERE id='local'`, authored.ID); err != nil {
		t.Fatal(err)
	}
	state := personalassistant.NewState("local")
	state.AssistantID = "assistant-1"
	state.Status = personalassistant.StatusActive
	state.DisplayName = "Ada"
	state.Appearance = scopedProfile.Appearance.Clone()
	state.HQWorkspaceID = authored.ID
	state.HQEntryAgentInstanceID = "entry-1"
	state.GlobalAgentProfileName = "Ada"
	state.Mandate = "Keep the important work visible."
	state.FocusAreas = []personalassistant.FocusArea{personalassistant.FocusPlanMyDay}
	state.SpecialistOfferState = personalassistant.SpecialistOfferDeclined
	state.LastHireRequestID = "hire-request"
	state.LastHQRequestID = "hq-request"
	hired := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	state.HiredAt = &hired
	if _, err := personalassistant.NewSQLiteStore(db).CreateState(ctx, state); err != nil {
		t.Fatal(err)
	}
	binding, err := personalassistant.NewContinuityBinding(canonicalSQL, &scopedProfile)
	if err != nil {
		t.Fatal(err)
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), authored.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	var files []workspacecontinuity.Fingerprint
	err = db.InTransaction(ctx, func(tx *sql.Tx) error {
		file, err := session.NewSQLiteStore(db).CollectContinuityWorkspace(ctx, tx, sourceDir, authored.ID, spool)
		if err != nil {
			return fmt.Errorf("workspace: %w", err)
		}
		files = append(files, file)
		profiles, err := agentworkspace.CollectContinuityProfiles(ctx, sourceDir, &authored, spool)
		if err != nil {
			return fmt.Errorf("profiles: %w", err)
		}
		files = append(files, profiles...)
		if err := personalassistant.NewSQLiteStore(db).CollectContinuityAgreement(ctx, tx, authored.ID, &binding, spool); err != nil {
			return fmt.Errorf("agreement: %w", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal("canonical source collectors", err)
	}
	components, objects, err := spool.Seal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	manifest := workspacecontinuity.Manifest{Version: workspacecontinuity.Version, WorkspaceID: authored.ID, Generation: uuid.NewString(),
		CheckpointAt: time.Now().UTC(), SourceRevision: uint64(authored.Version), Files: files, Components: components}
	manifest.SourceFingerprint = workspacecontinuity.FilesDigest(files)
	// This fence only proves that the bounded test collector can publish one
	// exact SQL view and canonical file set. It does not acknowledge dirty rows.
	if err := workspacecontinuity.Publish(ctx, sourceDir, manifest, objects, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(t.TempDir(), "copied-hq")
	if err := os.Mkdir(copyDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(copyDir, os.DirFS(sourceDir)); err != nil {
		t.Fatal(err)
	}
	destination, destinationCleanup := createTestHandler(t)
	defer destinationCleanup()
	code, payload := requestImportReview(t, destination, copyDir, true, false)
	if code != 200 {
		t.Fatal(code, payload)
	}
	review := payload["continuity"].(map[string]any)
	if review["status"] != "review_required" || review["import_supported"] != false {
		t.Fatal("review claimed an import it cannot perform", review)
	}
	candidates, ok := review["assistant_candidates"].([]any)
	if !ok || len(candidates) != 1 {
		t.Fatal("canonical assistant missing after directory-only copy", review)
	}
	candidate := candidates[0].(map[string]any)
	if candidate["assistant_id"] != "assistant-1" || candidate["source_status"] != "active" {
		t.Fatal("assistant changed across source copy", candidate)
	}
	if _, err := session.NewSQLiteStore(destination.store.DB()).GetWorkspace(ctx, authored.ID); err == nil {
		t.Fatal("read-only review registered workspace")
	}
	var count int
	if err := destination.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state`).Scan(&count); err != nil || count != 0 {
		t.Fatal("read-only review adopted an assistant", count, err)
	}
}
