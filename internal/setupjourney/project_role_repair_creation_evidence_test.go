package setupjourney

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A test-only stand-in for the *separate* Setup Journey create and consumed
// project-review receipts. The real paired-candidate sandbox uses a root run,
// connect_existing_project, and CommitReviewed (not group operations).
func seedReviewedConnectionFixture(t *testing.T, db *database.DB, primary workspace.Store, folder *workspace.FileStore, ownerID, homeID, projectID string) {
	t.Helper()
	seedReviewedConnectionForRunKind(t, db, primary, folder, ownerID, homeID, projectID, RunKindRoot)
}

func seedReviewedConnectionForRunKind(t *testing.T, db *database.DB, primary workspace.Store, folder *workspace.FileStore, ownerID, homeID, projectID string, kind RunKind) {
	t.Helper()
	const key = "original-create-key"
	inputDigest, ownerDigest, disclosureDigest := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64)
	reviewDigest, operationDigest := projectconnection.CreationEvidenceDigests(roleRepairFixtureRunID, projecttemplates.ProjectConnectionExistingProject, inputDigest, ownerDigest)
	update := func(current *workspace.Workspace) error {
		provenance := current.GetTemplateProvenance()
		if provenance == nil || provenance.GroupRequirement == nil {
			return errors.New("missing creation provenance")
		}
		provenance.GroupRequirement.ReviewDigest = reviewDigest
		provenance.GroupRequirement.OperationDigest = operationDigest
		current.SetTemplateProvenance(provenance)
		return nil
	}
	if item, err := primary.Get(projectID); err != nil {
		t.Fatal(err)
	} else if item.GetTemplateProvenance() != nil {
		if err := primary.Update(projectID, update); err != nil {
			t.Fatal(err)
		}
	}
	// Write the folder mirror exactly at the primary's version, as one fenced
	// SyncStore write would leave it, so the shared fence does not read this
	// seeded fixture as a split child. A bumping folder Update would put the
	// folder one version ahead of the primary.
	folderRecord, err := folder.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := update(folderRecord); err != nil {
		t.Fatal(err)
	}
	project, err := primary.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	folderRecord.Version = project.Version
	if err := folder.RestoreMirrorRecord(folderRecord); err != nil {
		t.Fatal(err)
	}
	pin := project.GetAssistantProjectLink().ProjectProvider
	now := time.Now().UTC()
	rootID := roleRepairFixtureRunID
	rootProjectID := projectID
	rootMode := "file_only"
	if kind == RunKindChild {
		rootID, rootProjectID, rootMode = "root-"+roleRepairFixtureRunID, "", ""
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO setup_journey_run
		(id, run_kind, owner_user_id, relationship_id, specialist_slug, journey_id, declaration_schema_version,
		declaration_version, state_revision, current_step_id, step_states_json, integration_plugin_id,
		integration_version, home_workspace_id, project_workspace_id, selected_mode_id, created_at, updated_at)
		VALUES (?, 'root', ?, 'music-fixture', 'music-fixture', 'project-setup', 1, 1, 6, 'project', '[]', ?, ?, ?, ?, ?, ?, ?)`,
		rootID, ownerID, pin.PluginID, pin.PluginVersion, homeID, rootProjectID, rootMode, now, now); err != nil {
		t.Fatal(err)
	}
	if kind == RunKindChild {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO setup_journey_run
			(id, run_kind, root_run_id, journey_id, declaration_schema_version, declaration_version,
			state_revision, current_step_id, step_states_json, project_workspace_id, selected_mode_id, created_at, updated_at)
			VALUES (?, 'child', ?, 'project-setup', 1, 1, 6, 'project', '[]', ?, 'file_only', ?, ?)`,
			roleRepairFixtureRunID, rootID, projectID, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO setup_journey_review_receipt
		(token, run_kind, run_id, idempotency_key, step_id, action_id, input_digest, run_revision,
		owner_revision_digest, disclosure_digest, created_at, expires_at, consumed_at, consumed_by_idempotency_key)
		VALUES ('original-review', ?, ?, ?, 'project', 'connect_existing_project', ?, 5, ?, ?, ?, ?, ?, ?)`,
		kind, roleRepairFixtureRunID, key, inputDigest, ownerDigest, disclosureDigest, now, now.Add(10*time.Minute), now, key); err != nil {
		t.Fatal(err)
	}
	result := CanonicalResult{ProjectWorkspaceID: projectID}
	if kind == RunKindRoot {
		result.HomeWorkspaceID = homeID
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO setup_journey_operation_receipt
		(run_kind, run_id, idempotency_key, step_id, action_id, input_digest, review_digest, status,
		result_code, result_json, run_revision_before, run_revision_after, created_at, completed_at)
		VALUES (?, ?, ?, 'project', 'connect_existing_project', ?, ?, 'succeeded', 'applied', ?, 5, 6, ?, ?)`,
		kind, roleRepairFixtureRunID, key, inputDigest, disclosureDigest, string(encodedResult), now, now); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRoleRepairReviewAcceptsOnlyExactChildRunWithParentOwnerAndHome(t *testing.T) {
	store, primary, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
	db, err := database.Open(t.Context(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	seedReviewedConnectionForRunKind(t, db, primary, folder, owner, homeID, projectID, RunKindChild)
	reviewer := NewProjectRoleRepairReviewer(store, db, func() ([]plugin.InstalledPlugin, error) { return installed, nil })
	if _, err := reviewer.Review(t.Context(), owner, homeID, projectID, "child-run"); err != nil {
		t.Fatalf("exact child run with parent root refused: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_run SET home_workspace_id = 'other-home' WHERE id = ?`, "root-"+roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewer.Review(t.Context(), owner, homeID, projectID, "after-parent-rebound"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("child run inherited an unrelated root's Home: %v", err)
	}
}

func TestProjectRoleRepairReviewRequiresOriginalConsumedCreatorEvidence(t *testing.T) {
	store, primary, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
	db, err := database.Open(t.Context(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	list := func() ([]plugin.InstalledPlugin, error) { return installed, nil }
	reviewer := NewProjectRoleRepairReviewer(store, db, list)
	refused := func(key string) {
		t.Helper()
		if _, err := reviewer.Review(t.Context(), owner, homeID, projectID, key); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
			t.Fatalf("unverified creator evidence minted %q: %v", key, err)
		}
	}
	refused("missing-operation")
	seedReviewedConnectionFixture(t, db, primary, folder, owner, homeID, projectID)
	valid, err := reviewer.Review(t.Context(), owner, homeID, projectID, "valid-original")
	if err != nil {
		t.Fatalf("consumed exact creator evidence was rejected: %v", err)
	}
	if pending, err := reviewer.InspectPendingReview(t.Context(), owner, homeID, projectID, valid.Token); err != nil || pending.Token != valid.Token {
		t.Fatalf("original exact review could not be reread: %+v %v", pending, err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_operation_receipt SET result_json = ? WHERE run_id = ?`, `{"project_workspace_id":"foreign"}`, roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	refused("changed-result")
	if _, err := reviewer.InspectPendingReview(t.Context(), owner, homeID, projectID, valid.Token); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("changed original creator result left a pending review valid: %v", err)
	}
	if _, err := reviewer.ClaimProjectRoleRepairOperation(t.Context(), owner, homeID, projectID, valid.Token, "stale-origin-claim"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("changed original creator receipt was consumed for a repair: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_operation_receipt SET result_json = ? WHERE run_id = ?`,
		`{"home_workspace_id":"`+homeID+`","project_workspace_id":"`+projectID+`"}`, roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_operation_receipt SET status = 'claimed' WHERE run_id = ?`, roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	refused("uncompleted-operation")
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_operation_receipt SET status = 'succeeded' WHERE run_id = ?`, roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_run SET home_workspace_id = 'foreign' WHERE id = ?`, roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	refused("changed-origin-home")
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_run SET home_workspace_id = ? WHERE id = ?`, homeID, roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_review_receipt SET owner_revision_digest = ? WHERE run_id = ?`, strings.Repeat("f", 64), roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	refused("changed-owner-review")
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_review_receipt SET owner_revision_digest = ? WHERE run_id = ?`, strings.Repeat("2", 64), roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_review_receipt SET consumed_at = NULL WHERE run_id = ?`, roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	refused("unconsumed-review")
	now := time.Now().UTC()
	if _, err := db.ExecContext(t.Context(), `UPDATE setup_journey_review_receipt SET consumed_at = ? WHERE run_id = ?`, now, roleRepairFixtureRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO setup_journey_review_receipt
		(token, run_kind, run_id, idempotency_key, step_id, action_id, input_digest, run_revision,
		owner_revision_digest, disclosure_digest, created_at, expires_at, consumed_at, consumed_by_idempotency_key)
		VALUES ('other-original-review', 'root', ?, 'other-create-key', 'project', 'connect_existing_project', ?, 5, ?, ?, ?, ?, ?, 'other-create-key')`,
		roleRepairFixtureRunID, strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), now, now.Add(10*time.Minute), now); err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(CanonicalResult{HomeWorkspaceID: homeID, ProjectWorkspaceID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO setup_journey_operation_receipt
		(run_kind, run_id, idempotency_key, step_id, action_id, input_digest, review_digest, status,
		result_code, result_json, run_revision_before, run_revision_after, created_at, completed_at)
		VALUES ('root', ?, 'other-create-key', 'project', 'connect_existing_project', ?, ?, 'succeeded', 'applied', ?, 5, 6, ?, ?)`,
		roleRepairFixtureRunID, strings.Repeat("1", 64), strings.Repeat("3", 64), string(result), now, now); err != nil {
		t.Fatal(err)
	}
	refused("ambiguous-original-operations")
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM project_role_repair_review`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid origin minted repair review: %d %v", count, err)
	}
}
