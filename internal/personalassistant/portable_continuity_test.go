package personalassistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func portableMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func portableIdentity() (*session.Workspace, *agent.Agent) {
	ws := &session.Workspace{ID: "hq-local", Name: "Personal HQ", OwnerUserID: "local",
		CreatedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC), UpdatedAt: time.Date(2025, 1, 3, 3, 4, 5, 0, time.UTC),
		AgentInstances: []session.AgentInstance{{ID: "instance-local", Name: "Ada", EntryPoint: true}},
		SharedData:     map[string]any{"personal_assistant_presentation": map[string]any{"version": 1, "assistant_id": "assistant-a", "request_id": "hq-request"}}}
	profile := &agent.Agent{Role: types.RoleOrchestrator, Appearance: types.NewAgentAppearance(),
		Metadata: &types.AgentMetadata{Tags: []string{ProfileAssistantMarker("assistant-a"), ProfileHireMarker("hire-request")}},
		Settings: types.Settings{APIKey: "synthetic-profile-secret", SystemPrompt: "Synthetic prompt, not an agreement"}}
	return ws, profile
}

func portableUser(t *testing.T, store *SQLiteStore) {
	t.Helper()
	_, err := store.db.ExecContext(t.Context(), `INSERT INTO users(id,created_at,updated_at) VALUES ('local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) ON CONFLICT DO NOTHING`)
	portableMust(t, err)
}

func portableSource(t *testing.T) (*SQLiteStore, workspacecontinuity.Record, ContinuityBinding, *State) {
	t.Helper()
	store, _ := newTestStore(t)
	portableUser(t, store)
	ws, profile := portableIdentity()
	portableMust(t, session.NewSQLiteStore(store.db).CreateWorkspace(t.Context(), ws))
	_, err := store.db.ExecContext(t.Context(), `UPDATE users SET personal_workspace_id=? WHERE id='local'`, ws.ID)
	portableMust(t, err)
	binding, err := NewContinuityBinding(ws, profile)
	portableMust(t, err)
	state := activeTestState("local", "assistant-a")
	state.LastHireRequestID, state.LastHQRequestID = "hire-request", "hq-request"
	state.HirePayloadHash, state.HirePayloadJSON = "synthetic-hash", `{"private_pending_command":"do-not-export"}`
	state.SpecialistSlug, state.SpecialistOfferState = "unavailable-old-specialist", SpecialistOfferDeclined
	hired := time.Date(2025, 1, 2, 1, 2, 3, 456, time.UTC)
	state.HiredAt = &hired
	store.now = func() time.Time { return hired.Add(time.Hour) }
	created, err := store.CreateState(t.Context(), state)
	portableMust(t, err)
	store.now = func() time.Time { return hired.Add(2 * time.Hour) }
	created, err = store.UpdateState(t.Context(), created, created.StateVersion)
	portableMust(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var record *workspacecontinuity.Record
	portableMust(t, store.db.InTransaction(ctx, func(tx *sql.Tx) error {
		record, err = store.SnapshotContinuityAgreement(ctx, tx, binding)
		return err
	}))
	if record == nil {
		t.Fatal("saved relationship missing")
	}
	for _, excluded := range []string{"synthetic-profile-secret", "Synthetic prompt", "private_pending_command", "hire_payload", "rename_step", "apply_request"} {
		if strings.Contains(string(record.Data), excluded) {
			t.Fatal("agreement leaked runtime configuration or pending commands")
		}
	}
	return store, *record, binding, created
}

func portableDestination(t *testing.T, action workspacecontinuity.ImportAction) (*SQLiteStore, workspacecontinuity.RestoreScope) {
	t.Helper()
	store, _ := newTestStore(t)
	portableUser(t, store)
	op := workspacecontinuity.Operation{ID: uuid.NewString(), UserID: "local", Action: action,
		TreeDigest: workspacecontinuity.Digest([]byte("tree")), DestinationDigest: workspacecontinuity.Digest([]byte("destination"))}
	disposition := workspacecontinuity.AdoptedHQ
	if action == workspacecontinuity.WorkspaceOnly {
		disposition = workspacecontinuity.WorkspaceOnlyHQ
	}
	ws, _ := portableIdentity()
	_, err := workspacecontinuity.NewLocalStore(store.db).BeginImport(t.Context(), op, []workspacecontinuity.ImportMember{{
		WorkspaceID: ws.ID, Generation: uuid.NewString(), Digest: workspacecontinuity.Digest([]byte("manifest")), Disposition: disposition}})
	portableMust(t, err)
	scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: ws.ID, UserID: "local"}
	instances, err := json.Marshal(ws.AgentInstances)
	portableMust(t, err)
	shared, err := json.Marshal(ws.SharedData)
	portableMust(t, err)
	// The workspace adapter will own this insertion in production. This fixture
	// supplies exactly its transaction/receipt boundary, not implicit ID trust.
	portableMust(t, store.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := workspacecontinuity.ClaimRecord(t.Context(), tx, scope, "workspace", "workspaces", ws.ID, workspacecontinuity.Digest([]byte("workspace")))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,agent_instances,shared_data,created_at,updated_at)
			VALUES (?,?,?,?,?,?)`, ws.ID, ws.Name, string(instances), string(shared), ws.CreatedAt, ws.UpdatedAt)
		return err
	}))
	return store, scope
}

func TestVerifyContinuityAgreementRequiresExactScopedBinding(t *testing.T) {
	_, record, binding, _ := portableSource(t)
	agreement, err := VerifyContinuityAgreement(record, binding)
	portableMust(t, err)
	if agreement.ProfileName != "Ada" {
		t.Fatal("review chose another agent")
	}
	altered := record
	altered.Data = []byte(strings.Replace(string(record.Data), `"profile_name":"Ada"`, `"profile_name":"Other"`, 1))
	if string(altered.Data) == string(record.Data) {
		t.Fatal("fixture did not alter profile")
	}
	if _, err := VerifyContinuityAgreement(altered, binding); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("same-name or foreign binding was accepted", err)
	}
}

func TestCollectContinuityAgreementDistinguishesHistoryFromMissingRelationship(t *testing.T) {
	source, expected, binding, _ := portableSource(t)
	capture := func(workspaceID string, value *ContinuityBinding) workspacecontinuity.Component {
		t.Helper()
		spool, err := workspacecontinuity.NewSpool(t.TempDir(), workspaceID)
		portableMust(t, err)
		defer func() { portableMust(t, spool.Close()) }()
		portableMust(t, source.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
			return source.CollectContinuityAgreement(t.Context(), tx, workspaceID, value, spool)
		}))
		components, objects, err := spool.Seal(t.Context())
		portableMust(t, err)
		for _, component := range components {
			if component.Domain != "assistant" {
				continue
			}
			if component.Availability == workspacecontinuity.Present {
				if len(component.Chunks) != 1 || component.Counts["agreements"] != 1 {
					t.Fatal("agreement count dropped")
				}
				reader, err := objects(t.Context(), component.Chunks[0].Digest)
				portableMust(t, err)
				data, err := io.ReadAll(reader)
				portableMust(t, err)
				portableMust(t, reader.Close())
				chunk, err := workspacecontinuity.DecodeChunk(strings.NewReader(string(data)), component.Chunks[0], "hq-local", "assistant")
				portableMust(t, err)
				if len(chunk.Records) != 1 || !reflect.DeepEqual(chunk.Records[0], expected) {
					t.Fatal("spool changed authored agreement")
				}
			}
			return component
		}
		t.Fatal("assistant availability absent")
		return workspacecontinuity.Component{}
	}
	if got := capture("hq-local", &binding); got.Availability != workspacecontinuity.Present {
		t.Fatal("saved agreement unavailable", got)
	}
	// A missing binding for a marked HQ must not silently call it empty.
	hqSpool, err := workspacecontinuity.NewSpool(t.TempDir(), "hq-local")
	portableMust(t, err)
	err = source.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		return source.CollectContinuityAgreement(t.Context(), tx, "hq-local", nil, hqSpool)
	})
	portableMust(t, hqSpool.Close())
	if !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("HQ identity hidden by nil binding", err)
	}
	ordinary := &session.Workspace{ID: "ordinary-workspace", Name: "Ordinary", OwnerUserID: "local", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	portableMust(t, session.NewSQLiteStore(source.db).CreateWorkspace(t.Context(), ordinary))
	if got := capture(ordinary.ID, nil); got.Availability != workspacecontinuity.Empty {
		t.Fatal("ordinary workspace fabricated relationship", got)
	}
	_, err = source.db.ExecContext(t.Context(), `UPDATE workspaces SET shared_data=? WHERE id=?`, `{"PERSONAL_ASSISTANT_PRESENTATION":{"version":1}}`, ordinary.ID)
	portableMust(t, err)
	aliasSpool, err := workspacecontinuity.NewSpool(t.TempDir(), ordinary.ID)
	portableMust(t, err)
	err = source.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		return source.CollectContinuityAgreement(t.Context(), tx, ordinary.ID, nil, aliasSpool)
	})
	portableMust(t, aliasSpool.Close())
	if !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("aliased HQ marker was called empty", err)
	}
	_, err = source.db.ExecContext(t.Context(), `DELETE FROM personal_assistant_state WHERE user_id='local'`)
	portableMust(t, err)
	if got := capture("hq-local", &binding); got.Availability != workspacecontinuity.Unavailable || got.Reason != "missing_agreement" {
		t.Fatal("provenance-bearing HQ mislabeled absent history as intentionally empty", got)
	}
}

func TestPortableAgreementPreservesChoicesWithoutCommandsOrActivation(t *testing.T) {
	_, record, binding, source := portableSource(t)
	store, scope := portableDestination(t, workspacecontinuity.Continue)
	portableMust(t, store.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		created, err := store.RestoreContinuityAgreement(t.Context(), tx, scope, record, binding)
		if err == nil && !created {
			t.Fatal("first restoration did not insert")
		}
		return err
	}))
	got, err := store.GetState(t.Context(), "local")
	portableMust(t, err)
	if got.AssistantID != source.AssistantID || got.HQWorkspaceID != source.HQWorkspaceID || got.HQEntryAgentInstanceID != source.HQEntryAgentInstanceID ||
		got.DisplayName != source.DisplayName || got.GlobalAgentProfileName != source.GlobalAgentProfileName ||
		got.Mandate != source.Mandate || !reflect.DeepEqual(got.FocusAreas, source.FocusAreas) || !reflect.DeepEqual(got.Appearance, source.Appearance) ||
		got.SpecialistSlug != source.SpecialistSlug || got.SpecialistOfferState != source.SpecialistOfferState ||
		!got.HiredAt.Equal(*source.HiredAt) || !got.CreatedAt.Equal(source.CreatedAt) || !got.UpdatedAt.Equal(source.UpdatedAt) {
		t.Fatal("restoration changed source identity, choices or timestamps")
	}
	if got.Status != StatusPaused || got.StateVersion != 1 || got.FirstAssignmentStatus != FirstAssignmentFailed ||
		got.HirePayloadJSON != "" || got.HQPayloadJSON != "" || got.RenameStep != RenameNone || got.HirePayloadHash != "" {
		t.Fatal("source authority, CAS, completion flag or command restored")
	}
	attachment, err := workspacecontinuity.NewLocalStore(store.db).Attachment(t.Context(), scope.WorkspaceID)
	portableMust(t, err)
	if attachment.AllowsAutomatic() || attachment.AllowsManual() {
		t.Fatal("partial restoration granted admission")
	}
	var designation string
	portableMust(t, store.db.QueryRowContext(t.Context(), `SELECT personal_workspace_id FROM users WHERE id='local'`).Scan(&designation))
	if designation != "" {
		t.Fatal("adapter independently designated HQ")
	}
	got.Mandate, got.Status = "A local edit", StatusActive
	got, err = store.UpdateState(t.Context(), got, got.StateVersion)
	portableMust(t, err)
	for _, deleted := range []bool{false, true} {
		if deleted {
			_, err := store.db.ExecContext(t.Context(), `DELETE FROM personal_assistant_state WHERE user_id='local'`)
			portableMust(t, err)
		}
		portableMust(t, store.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
			// A retry does not depend on the old profile still having its old
			// name/appearance. Ownership wins before mutable identity checks.
			created, err := store.RestoreContinuityAgreement(t.Context(), tx, scope, record, ContinuityBinding{})
			if created {
				t.Fatal("retry inserted over an edit or deletion")
			}
			return err
		}))
		current, err := store.GetState(t.Context(), "local")
		if deleted {
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("deletion resurrected: %v", err)
			}
		} else if err != nil || !reflect.DeepEqual(current, got) {
			t.Fatal("retry changed a newer relationship")
		}
	}
}

func TestPortableAgreementPreservesPauseButDoesNotTrustCompletionFlags(t *testing.T) {
	source, _, binding, state := portableSource(t)
	state.Status, state.FirstAssignmentStatus = StatusPaused, FirstAssignmentCompleted
	_, err := source.UpdateState(t.Context(), state, state.StateVersion)
	portableMust(t, err)
	record, err := source.SnapshotContinuityAgreement(t.Context(), source.db, binding)
	portableMust(t, err)
	agreement, err := DecodeContinuityAgreement(*record)
	portableMust(t, err)
	if agreement.SourceStatus != StatusPaused || agreement.FirstAssignmentStatus != FirstAssignmentCompleted {
		t.Fatal("source intent/evidence omitted")
	}
	destination, scope := portableDestination(t, workspacecontinuity.Continue)
	portableMust(t, destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := destination.RestoreContinuityAgreement(t.Context(), tx, scope, *record, binding)
		return err
	}))
	restored, err := destination.GetState(t.Context(), "local")
	portableMust(t, err)
	if restored.Status != StatusPaused || restored.FirstAssignmentStatus == FirstAssignmentCompleted {
		t.Fatal("a source pause was resumed or an unverified completion claimed")
	}
	var assignments int
	portableMust(t, destination.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM personal_assistant_assignment`).Scan(&assignments))
	if assignments != 0 {
		t.Fatal("restoring an agreement reconstructed assignment commands")
	}
}

func TestPortableAgreementRefusesIncumbentsAndUnconfirmedAdoption(t *testing.T) {
	_, record, binding, _ := portableSource(t)
	for _, scenario := range []string{"workspace-only", "awaiting-hq", "incumbent-hq", "wrong-instance", "unowned-workspace", "changed-profile"} {
		t.Run(scenario, func(t *testing.T) {
			action := workspacecontinuity.Continue
			if scenario == "workspace-only" {
				action = workspacecontinuity.WorkspaceOnly
			}
			store, scope := portableDestination(t, action)
			candidate := binding
			var incumbent *State
			switch scenario {
			case "awaiting-hq":
				state := activeTestState("local", "incumbent")
				state.Status, state.HQWorkspaceID, state.HQEntryAgentInstanceID = StatusAwaitingHQ, "", ""
				var err error
				incumbent, err = store.CreateState(t.Context(), state)
				portableMust(t, err)
			case "incumbent-hq":
				_, err := store.db.ExecContext(t.Context(), `UPDATE users SET personal_workspace_id='unrelated-hq' WHERE id='local'`)
				portableMust(t, err)
			case "wrong-instance":
				_, err := store.db.ExecContext(t.Context(), `UPDATE workspaces SET agent_instances='[]' WHERE id='hq-local'`)
				portableMust(t, err)
			case "unowned-workspace":
				_, err := store.db.ExecContext(t.Context(), `DELETE FROM continuity_records WHERE domain='workspace'`)
				portableMust(t, err)
			case "changed-profile":
				candidate.assistantID = "same-name-foreign-assistant"
			}
			err := store.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
				_, err := store.RestoreContinuityAgreement(t.Context(), tx, scope, record, candidate)
				return err
			})
			if !errors.Is(err, workspacecontinuity.ErrConflict) {
				t.Fatalf("unsafe adoption accepted: %v", err)
			}
			var count int
			portableMust(t, store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM continuity_records WHERE domain='assistant'`).Scan(&count))
			if count != 0 {
				t.Fatal("failed restore left a claim")
			}
			if incumbent != nil {
				current, err := store.GetState(t.Context(), "local")
				portableMust(t, err)
				if !reflect.DeepEqual(current, incumbent) {
					t.Fatal("incumbent changed")
				}
			}
		})
	}
}

func TestPortableAgreementRejectsAmbiguousIdentityAndPendingSource(t *testing.T) {
	for _, scenario := range []string{"duplicate-tag", "wrong-profile", "two-entries", "foreign-owner", "external-image"} {
		t.Run(scenario, func(t *testing.T) {
			ws, profile := portableIdentity()
			switch scenario {
			case "duplicate-tag":
				profile.Metadata.Tags = append(profile.Metadata.Tags, ProfileAssistantMarker("assistant-a"))
			case "wrong-profile":
				profile.Metadata.Tags[0] = ProfileAssistantMarker("foreign")
			case "two-entries":
				ws.AgentInstances = append(ws.AgentInstances, session.AgentInstance{ID: "other-instance", Name: "Ada", EntryPoint: true})
			case "foreign-owner":
				ws.OwnerUserID = "other-user"
			case "external-image":
				profile.Appearance.Uploaded = &types.UploadedAppearance{Image: "../outside.png"}
			}
			if _, err := NewContinuityBinding(ws, profile); !errors.Is(err, workspacecontinuity.ErrInvalid) {
				t.Fatalf("contradictory ownership accepted: %v", err)
			}
		})
	}
	ws, profile := portableIdentity()
	ws.SharedData["personal_assistant_presentation"].(map[string]any)["version"] = 99
	if _, err := NewContinuityBinding(ws, profile); !errors.Is(err, workspacecontinuity.ErrVersion) {
		t.Fatalf("future identity schema accepted: %v", err)
	}
	store, record, binding, _ := portableSource(t)
	for _, addition := range []string{`"hire_payload_json":"queued command",`, `"Source_status":"paused",`} {
		bad := record
		bad.Data = []byte("{" + addition + string(record.Data[1:]))
		if _, err := DecodeContinuityAgreement(bad); !errors.Is(err, workspacecontinuity.ErrInvalid) {
			t.Fatalf("unknown field/alias accepted: %v", err)
		}
	}
	_, err := store.db.ExecContext(t.Context(), `UPDATE personal_assistant_state SET rename_step='profile_pending',rename_from_name='Ada',rename_to_name='New name' WHERE user_id='local'`)
	portableMust(t, err)
	if _, err := store.SnapshotContinuityAgreement(t.Context(), store.db, binding); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatalf("unfinished rename was portable: %v", err)
	}
	_, err = store.db.ExecContext(t.Context(), `DELETE FROM personal_assistant_state WHERE user_id='local'`)
	portableMust(t, err)
	if got, err := store.SnapshotContinuityAgreement(t.Context(), store.db, binding); err != nil || got != nil {
		t.Fatal("missing relationship fabricated an agreement")
	}
}
