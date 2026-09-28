package sessionhttp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestScopedProfileCollectorCapturesOwnedDefinitionsWithoutInlineKeys(t *testing.T) {
	for _, mode := range []string{"valid", "inline_secret", "extra_asset", "image", "missing_image"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "native-like-hq")
			publishAssistantReviewFixture(t, dir, mode)
			inspected, err := workspacecontinuity.Inspect(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := workspacecontinuity.ReadCanonicalFile(t.Context(), dir, agentworkspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
			if err != nil {
				t.Fatal(err)
			}
			var ws *agentworkspace.Workspace
			err = workspacecontinuity.ReadComponentRecords(t.Context(), dir, inspected, "workspace", func(chunk workspacecontinuity.Chunk) error {
				if len(chunk.Records) != 1 {
					return workspacecontinuity.ErrIncomplete
				}
				var err error
				ws, err = agentworkspace.DecodeContinuityWorkspace(chunk.Records[0], canonical)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			spool, err := workspacecontinuity.NewSpool(t.TempDir(), ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = spool.Close() }()
			files, err := agentworkspace.CollectContinuityProfiles(t.Context(), dir, ws, spool)
			if mode == "extra_asset" {
				if !errors.Is(err, workspacecontinuity.ErrIncomplete) {
					t.Fatal("source with private unclaimed bytes was captured", err)
				}
				return
			}
			// An inline native key is collected as the denied definition; the
			// record itself never contains it (checked by the review tests).
			wantFiles := 1
			if mode == "image" {
				wantFiles = 2
			}
			if err != nil || len(files) != wantFiles {
				t.Fatal("source profile evidence omitted", files, err)
			}
			for _, file := range files {
				data, err := workspacecontinuity.ReadCanonicalFile(t.Context(), dir, file.Path, workspacecontinuity.MaxChunkBytes)
				if err != nil || file.Digest != workspacecontinuity.Digest(data) || file.Bytes != int64(len(data)) {
					t.Fatal("fingerprint not bound to owned bytes", file, err)
				}
			}
			components, _, err := spool.Seal(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, component := range components {
				if component.Domain == "agents" && (component.Availability != workspacecontinuity.Present || component.Counts["profiles"] != 1 || (mode == "image" && (component.Counts["appearances"] != 1 || len(component.Blobs) != 1)) || (mode == "missing_image" && (component.Counts["appearances"] != 1 || len(component.Blobs) != 0))) {
					t.Fatal("owned profile not captured", component)
				}
			}
		})
	}
}

func TestCopiedScopedAppearanceStagesExactOwnedBytesOrMissingEvidence(t *testing.T) {
	for _, mode := range []string{"image", "missing_image"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "copied-hq")
			publishAssistantReviewFixture(t, dir, mode)
			inspected, err := workspacecontinuity.Inspect(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			stage := t.TempDir()
			if err := os.Chmod(stage, 0700); err != nil {
				t.Fatal(err)
			}
			profile, err := agentworkspace.StageContinuityProfile(t.Context(), dir, stage, inspected, "Ada")
			if err != nil {
				t.Fatal(err)
			}
			status, err := agentworkspace.StageContinuityAppearance(t.Context(), dir, stage, inspected, profile)
			if err != nil {
				t.Fatal(err)
			}
			file := "agents/ada/appearance/ada.png"
			if mode == "image" {
				if status != "present" {
					t.Fatal(status)
				}
				original, err := workspacecontinuity.ReadCanonicalFile(t.Context(), dir, file, workspacecontinuity.MaxChunkBytes)
				if err != nil {
					t.Fatal(err)
				}
				staged, err := workspacecontinuity.ReadCanonicalFile(t.Context(), stage, file, workspacecontinuity.MaxChunkBytes)
				if err != nil || workspacecontinuity.Digest(staged) != workspacecontinuity.Digest(original) {
					t.Fatal("scoped image changed", err)
				}
				if _, err := agentworkspace.StageContinuityAppearance(t.Context(), dir, stage, inspected, profile); err == nil {
					t.Fatal("existing staged image was adopted")
				}
			} else {
				if status != "missing" {
					t.Fatal(status)
				}
				if _, err := workspacecontinuity.ReadCanonicalFile(t.Context(), stage, file, workspacecontinuity.MaxChunkBytes); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
					t.Fatal("missing image was fabricated", err)
				}
				if err := workspacecontinuity.ReplaceCanonicalFile(t.Context(), dir, file, "", []byte("late-file")); err != nil {
					t.Fatal(err)
				}
				if _, err := agentworkspace.StageContinuityAppearance(t.Context(), dir, stage, inspected, profile); !errors.Is(err, workspacecontinuity.ErrChanged) {
					t.Fatal("new source image was ignored", err)
				}
			}
			profile.Name = "Other"
			if _, err := agentworkspace.StageContinuityAppearance(t.Context(), dir, stage, inspected, profile); err == nil {
				t.Fatal("foreign profile was accepted")
			}
		})
	}
}

// This composes the inactive adapters using two separate physical directories
// and an isolated SQL database. It is NOT the product import coordinator: there
// is no HTTP confirmation, full domain set, file publication, or admission.
func TestCopiedAssistantEvidenceStagesAndRestoresPausedWithReceipt(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "source-hq")
	publishAssistantReviewFixture(t, source, "valid")
	copyDir := filepath.Join(t.TempDir(), "copied-hq")
	if err := os.Mkdir(copyDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(copyDir, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	h, cleanup := createTestHandler(t)
	defer cleanup()
	code, payload := requestImportReview(t, h, copyDir, true, false)
	if code != 200 {
		t.Fatal(code, payload)
	}
	review := payload["continuity"].(map[string]any)
	if review["status"] != "review_required" {
		t.Fatal(review)
	}
	inspected, err := workspacecontinuity.Inspect(ctx, copyDir)
	if err != nil {
		t.Fatal(err)
	}
	var workspaceRecord, profileRecord, agreementRecord workspacecontinuity.Record
	for _, domain := range []string{"workspace", "agents", "assistant"} {
		err := workspacecontinuity.ReadComponentRecords(ctx, copyDir, inspected, domain, func(chunk workspacecontinuity.Chunk) error {
			if len(chunk.Records) != 1 {
				return workspacecontinuity.ErrIncomplete
			}
			switch domain {
			case "workspace":
				workspaceRecord = chunk.Records[0]
			case "agents":
				profileRecord = chunk.Records[0]
			case "assistant":
				agreementRecord = chunk.Records[0]
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	canonical, err := workspacecontinuity.ReadCanonicalFile(ctx, copyDir, agentworkspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	if err := os.Chmod(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := agentworkspace.StageContinuityWorkspace(ctx, copyDir, stage, inspected, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := agentworkspace.StageContinuityProfile(ctx, copyDir, stage, inspected, "Ada"); err != nil {
		t.Fatal(err)
	}
	stageBytes, err := workspacecontinuity.ReadCanonicalFile(ctx, stage, "agents/ada/config.json", workspacecontinuity.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	var profile agent.Agent
	if err := json.Unmarshal(stageBytes, &profile); err != nil {
		t.Fatal(err)
	}
	db := h.store.DB()
	local := workspacecontinuity.NewLocalStore(db)
	op, err := local.BeginReviewedImport(ctx, workspacecontinuity.Operation{ID: uuid.NewString(), UserID: "local",
		TreeDigest: review["tree_digest"].(string), DestinationDigest: review["destination_digest"].(string), Action: workspacecontinuity.Continue},
		[]workspacecontinuity.ImportMember{{WorkspaceID: "reviewed-hq", Generation: inspected.Manifest.Generation,
			Digest: inspected.Pointer.Digest, Disposition: workspacecontinuity.AdoptedHQ}})
	if err != nil {
		t.Fatal(err)
	}
	scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: "reviewed-hq", UserID: "local"}
	// Claim profile only after its exact canonical bytes were staged, and do
	// not publish the staged folder or admit the partial workspace to execution.
	err = db.InTransaction(ctx, func(tx *sql.Tx) error {
		if inserted, err := session.NewSQLiteStore(db).RestoreContinuityWorkspace(ctx, tx, scope, workspaceRecord, canonical, ""); err != nil || !inserted {
			return errors.Join(err, workspacecontinuity.ErrIncomplete)
		}
		claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "agents", "profiles", profileRecord.ID, workspacecontinuity.Digest(profileRecord.Data))
		if err != nil || !claimed {
			return errors.Join(err, workspacecontinuity.ErrIncomplete)
		}
		origin, err := agentworkspace.DecodeContinuityWorkspace(workspaceRecord, canonical)
		if err != nil {
			return err
		}
		entries := make([]session.AgentInstance, 0, len(origin.AgentInstances))
		for _, v := range origin.AgentInstances {
			entries = append(entries, session.AgentInstance{ID: v.ID, Name: v.Name, EntryPoint: v.EntryPoint})
		}
		binding, err := personalassistant.NewContinuityBinding(&session.Workspace{ID: origin.ID, OwnerUserID: origin.OwnerUserID,
			AgentInstances: entries, SharedData: origin.SharedData}, &profile)
		if err != nil {
			return err
		}
		inserted, err := personalassistant.NewSQLiteStore(db).RestoreContinuityAgreement(ctx, tx, scope, agreementRecord, binding)
		if err != nil || !inserted {
			return errors.Join(err, workspacecontinuity.ErrIncomplete)
		}
		result, err := tx.ExecContext(ctx, `UPDATE users SET personal_workspace_id=? WHERE id='local' AND COALESCE(personal_workspace_id,'')=''`, scope.WorkspaceID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return errors.Join(err, workspacecontinuity.ErrConflict)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := session.NewSQLiteStore(db).GetWorkspace(ctx, scope.WorkspaceID)
	if err != nil || got.Name != "Copied HQ" {
		t.Fatal("workspace row not restored", err)
	}
	state, err := personalassistant.NewSQLiteStore(db).GetState(ctx, "local")
	if err != nil || state == nil || state.AssistantID != "assistant-1" || state.Status != personalassistant.StatusPaused {
		t.Fatal("assistant was not restored paused", state, err)
	}
	attachment, err := local.Attachment(ctx, scope.WorkspaceID)
	if err != nil || attachment.State != "restoring" {
		t.Fatal("partial workspace admitted", attachment, err)
	}
	if err := local.CompleteImport(ctx, op.ID, "local"); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("partial domain set completed", err)
	}
	// A local edit after receipt application belongs to this installation.
	if _, err := db.ExecContext(ctx, `UPDATE personal_assistant_state SET display_name='Local choice' WHERE user_id='local'`); err != nil {
		t.Fatal(err)
	}
	if _, err := local.BeginReviewedImport(ctx, op, []workspacecontinuity.ImportMember{{WorkspaceID: scope.WorkspaceID, Generation: inspected.Manifest.Generation,
		Digest: inspected.Pointer.Digest, Disposition: workspacecontinuity.AdoptedHQ}}); err != nil {
		t.Fatal("exact retry", err)
	}
	var display string
	if err := db.QueryRowContext(ctx, `SELECT display_name FROM personal_assistant_state WHERE user_id='local'`).Scan(&display); err != nil || display != "Local choice" {
		t.Fatal("retry overwrote local assistant edit", display, err)
	}
}
