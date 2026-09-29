package sessionhttp

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

const continuityCleanupTimeout = 10 * time.Second

// restoreContinuityMember restores one reviewed workspace's records under
// its receipt. Every domain commits in its own transaction together with its
// record claims, so a retry skips exactly what already committed. Domains
// already reported (restored or unavailable) are not read again.
func (h *Handler) restoreContinuityMember(ctx context.Context, op workspacecontinuity.Operation, plan continuityPlanMember, member continuityImportMember) error {
	db := h.store.DB()
	local := workspacecontinuity.NewLocalStore(db)
	scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: plan.WorkspaceID, UserID: op.UserID}
	outcomes, err := local.ComponentOutcomes(ctx, scope)
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, outcome := range outcomes {
		done[outcome.Domain] = outcome.Status == "restored" || outcome.Status == "unavailable" || outcome.Status == "unsupported"
	}
	inspected := member.Inspection
	components := map[string]workspacecontinuity.Component{}
	for _, component := range inspected.Manifest.Components {
		components[component.Domain] = component
	}
	dir := member.Dir
	canonical, err := workspacecontinuity.ReadCanonicalFile(ctx, dir, agentworkspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return err
	}
	var record workspacecontinuity.Record
	if err := workspacecontinuity.ReadGenerationRecords(ctx, dir, workspacecontinuity.Generation(inspected), "workspace", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "workspaces" || len(chunk.Records) != 1 {
			return workspacecontinuity.ErrInvalid
		}
		record = chunk.Records[0]
		return nil
	}); err != nil {
		return err
	}
	sqlite := session.NewSQLiteStore(db)
	set := func(tx *sql.Tx, domain, status string, count int64, reason string) error {
		return workspacecontinuity.SetComponentOutcome(ctx, tx, scope, workspacecontinuity.ComponentOutcome{Domain: domain, Status: status, Count: count, Reason: reason})
	}
	// finish records a domain whose source component was not present: empty is
	// verified empty; unavailable/unsupported keeps its reason for the report.
	finish := func(domain string) (bool, error) {
		if done[domain] {
			return true, nil
		}
		component := components[domain]
		switch component.Availability {
		case workspacecontinuity.Present:
			return false, nil
		case workspacecontinuity.Empty:
			return true, db.InTransaction(ctx, func(tx *sql.Tx) error { return set(tx, domain, "restored", 0, "") })
		default:
			reason := component.Reason
			if reason == "" {
				reason = "not_in_copy"
			}
			return true, db.InTransaction(ctx, func(tx *sql.Tx) error { return set(tx, domain, "unavailable", 0, reason) })
		}
	}

	if !done["workspace"] {
		if err := db.InTransaction(ctx, func(tx *sql.Tx) error {
			if _, err := sqlite.RestoreContinuityWorkspaceAt(ctx, tx, scope, record, canonical, plan.ParentID, plan.FolderSlug); err != nil {
				return err
			}
			return set(tx, "workspace", "restored", 1, "")
		}); err != nil {
			return err
		}
	}

	// Sessions and messages: chunks are ordered, each session precedes its
	// messages. Each chunk commits with its claims; counts are verified last.
	if skip, err := finish("sessions"); err != nil {
		return err
	} else if !skip {
		var sessions []workspacecontinuity.Record
		var messages int64
		if err := workspacecontinuity.ReadGenerationRecords(ctx, dir, workspacecontinuity.Generation(inspected), "sessions", func(chunk workspacecontinuity.Chunk) error {
			return db.InTransaction(ctx, func(tx *sql.Tx) error {
				for _, r := range chunk.Records {
					switch chunk.Family {
					case "sessions":
						if _, err := sqlite.RestoreContinuitySession(ctx, tx, scope, r); err != nil {
							return err
						}
						sessions = append(sessions, r)
					case "messages":
						if _, err := sqlite.RestoreContinuityMessage(ctx, tx, scope, r); err != nil {
							return err
						}
						messages++
					default:
						return workspacecontinuity.ErrInvalid
					}
				}
				return nil
			})
		}); err != nil {
			return err
		}
		if err := db.InTransaction(ctx, func(tx *sql.Tx) error {
			for _, r := range sessions {
				if err := sqlite.CheckContinuitySessionCount(ctx, tx, scope, r); err != nil {
					return err
				}
			}
			return set(tx, "sessions", "restored", int64(len(sessions)), "")
		}); err != nil {
			return err
		}
		_ = messages
	}

	if err := h.restoreSimpleDomain(ctx, finish, set, dir, inspected, "tool_history", func(tx *sql.Tx, r workspacecontinuity.Record) error {
		_, err := session.NewSQLiteToolCallStore(db).RestoreContinuityToolCall(ctx, tx, scope, r)
		return err
	}); err != nil {
		return err
	}

	var restoredNotes []string
	if err := h.restoreSimpleDomain(ctx, finish, set, dir, inspected, "notes", func(tx *sql.Tx, r workspacecontinuity.Record) error {
		inserted, err := sqlite.RestoreContinuityNote(ctx, tx, scope, r)
		if inserted {
			restoredNotes = append(restoredNotes, r.ID)
		}
		return err
	}); err != nil {
		return err
	}
	if len(restoredNotes) > 0 {
		if err := sqlite.RebuildContinuityNoteIndexes(ctx, plan.WorkspaceID, restoredNotes); err != nil {
			return err
		}
	}

	followups := followup.NewSQLiteStore(db)
	if err := h.restoreSimpleDomain(ctx, finish, set, dir, inspected, "followups", func(tx *sql.Tx, r workspacecontinuity.Record) error {
		_, err := followups.RestoreContinuityFollowUp(ctx, tx, scope, r)
		return err
	}); err != nil {
		return err
	}

	briefs := dailybrief.NewSQLiteStore(db)
	if err := h.restoreSimpleDomain(ctx, finish, set, dir, inspected, "brief_config", func(tx *sql.Tx, r workspacecontinuity.Record) error {
		_, err := briefs.RestoreContinuityConfig(ctx, tx, scope, r)
		return err
	}); err != nil {
		return err
	}
	if err := h.restoreSimpleDomain(ctx, finish, set, dir, inspected, "brief_history", func(tx *sql.Tx, r workspacecontinuity.Record) error {
		_, err := briefs.RestoreContinuityRevision(ctx, tx, scope, r)
		return err
	}); err != nil {
		return err
	}

	if err := h.restoreContinuityUploads(ctx, finish, set, scope, dir, inspected); err != nil {
		return err
	}

	adopted := plan.Disposition == workspacecontinuity.AdoptedHQ
	if skip, err := finish("assistant"); err != nil {
		return err
	} else if !skip {
		if !adopted {
			// The destination's own assistant and HQ are untouched. The incoming
			// agreement is kept inert with the workspace so its own checkpoint
			// keeps carrying the assistant it arrived with.
			if err := h.retainContinuityAssistant(ctx, set, scope, dir, inspected, canonical, record); err != nil {
				return err
			}
		} else if err := h.adoptContinuityAssistant(ctx, set, scope, dir, inspected, canonical, record); err != nil {
			return err
		}
	}

	if skip, err := finish("setup"); err != nil {
		return err
	} else if !skip {
		if !adopted {
			var kept []workspacecontinuity.Record
			if err := workspacecontinuity.ReadGenerationRecords(ctx, dir, workspacecontinuity.Generation(inspected), "setup", func(chunk workspacecontinuity.Chunk) error {
				for _, r := range chunk.Records {
					if _, err := personalassistant.DecodeContinuityAssignment(r, plan.WorkspaceID); err != nil || chunk.Family != "assignments" {
						return errors.Join(workspacecontinuity.ErrInvalid, err)
					}
					kept = append(kept, r)
				}
				return nil
			}); err != nil {
				return err
			}
			if err := db.InTransaction(ctx, func(tx *sql.Tx) error {
				if err := workspacecontinuity.RetainRecords(ctx, tx, scope, "setup", "assignments", kept); err != nil {
					return err
				}
				return set(tx, "setup", "unavailable", 0, "not_adopted")
			}); err != nil {
				return err
			}
		} else {
			assistants := personalassistant.NewSQLiteStore(db)
			var verified, unverified int64
			if err := workspacecontinuity.ReadGenerationRecords(ctx, dir, workspacecontinuity.Generation(inspected), "setup", func(chunk workspacecontinuity.Chunk) error {
				return db.InTransaction(ctx, func(tx *sql.Tx) error {
					for _, r := range chunk.Records {
						_, ok, err := assistants.RestoreContinuityAssignment(ctx, tx, scope, r)
						if err != nil {
							return err
						}
						if ok {
							verified++
						} else {
							unverified++
						}
					}
					return nil
				})
			}); err != nil {
				return err
			}
			reason := ""
			if unverified > 0 {
				reason = "needs_review"
			}
			if err := db.InTransaction(ctx, func(tx *sql.Tx) error { return set(tx, "setup", "restored", verified, reason) }); err != nil {
				return err
			}
		}
	}
	// agents and knowledge are folder files; installContinuityTree finishes
	// them once their denied/rebound projections are in place.
	return nil
}

type continuityOutcomeSetter func(tx *sql.Tx, domain, status string, count int64, reason string) error

// restoreSimpleDomain restores every record of one single-family domain,
// committing each chunk with its claims.
func (h *Handler) restoreSimpleDomain(ctx context.Context, finish func(string) (bool, error), set continuityOutcomeSetter,
	dir string, inspected workspacecontinuity.Inspection, domain string, restore func(*sql.Tx, workspacecontinuity.Record) error) error {
	skip, err := finish(domain)
	if err != nil || skip {
		return err
	}
	db := h.store.DB()
	var count int64
	if err := workspacecontinuity.ReadGenerationRecords(ctx, dir, workspacecontinuity.Generation(inspected), domain, func(chunk workspacecontinuity.Chunk) error {
		return db.InTransaction(ctx, func(tx *sql.Tx) error {
			for _, r := range chunk.Records {
				if err := restore(tx, r); err != nil {
					return err
				}
				count++
			}
			return nil
		})
	}); err != nil {
		return err
	}
	return db.InTransaction(ctx, func(tx *sql.Tx) error { return set(tx, domain, "restored", count, "") })
}

// restoreContinuityUploads installs copied upload bytes into the normal
// session-files store and records linked or missing files as unavailable
// entries. Source paths are never installed or read.
func (h *Handler) restoreContinuityUploads(ctx context.Context, finish func(string) (bool, error), set continuityOutcomeSetter,
	scope workspacecontinuity.RestoreScope, dir string, inspected workspacecontinuity.Inspection) error {
	skip, err := finish("uploads")
	if err != nil || skip {
		return err
	}
	db := h.store.DB()
	if h.continuity.uploads == nil {
		return db.InTransaction(ctx, func(tx *sql.Tx) error { return set(tx, "uploads", "unavailable", 0, "uploads_store_unavailable") })
	}
	bySession := map[string][]sessionfiles.ContinuityInstall{}
	records := map[string][]workspacecontinuity.Record{}
	var order []string
	var unavailable int64
	if err := workspacecontinuity.ReadGenerationRecords(ctx, dir, workspacecontinuity.Generation(inspected), "uploads", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "uploads" {
			return workspacecontinuity.ErrInvalid
		}
		for _, r := range chunk.Records {
			value, err := sessionfiles.DecodeContinuityUpload(r, scope.WorkspaceID)
			if err != nil {
				return err
			}
			item := sessionfiles.ContinuityInstall{Upload: value}
			if value.State == "copied" && value.Blob != nil {
				item.Open = pipeContinuityBlob(dir, inspected, "uploads", *value.Blob)
			} else {
				unavailable++
			}
			if _, seen := bySession[value.SessionID]; !seen {
				order = append(order, value.SessionID)
			}
			bySession[value.SessionID] = append(bySession[value.SessionID], item)
			records[value.SessionID] = append(records[value.SessionID], r)
		}
		return nil
	}); err != nil {
		return err
	}
	var installed int64
	for _, sessionID := range order {
		// The session must be one this operation restored.
		var owned int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_records WHERE domain='sessions' AND family='sessions'
			AND record_id=? AND operation_id=? AND workspace_id=?`, sessionID, scope.OperationID, scope.WorkspaceID).Scan(&owned); err != nil {
			return err
		}
		if owned != 1 {
			return workspacecontinuity.ErrConflict
		}
		if _, err := h.continuity.uploads.InstallContinuityUploads(ctx, sessionID, bySession[sessionID]); err != nil {
			return err
		}
		if err := db.InTransaction(ctx, func(tx *sql.Tx) error {
			for _, r := range records[sessionID] {
				if _, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "uploads", "uploads", r.ID, workspacecontinuity.Digest(r.Data)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		installed += int64(len(bySession[sessionID]))
	}
	reason := ""
	if unavailable > 0 {
		reason = "some_files_unavailable"
	}
	return db.InTransaction(ctx, func(tx *sql.Tx) error { return set(tx, "uploads", "restored", installed, reason) })
}

// adoptContinuityAssistant restores the agreement as this installation's
// personal assistant and designates the HQ, in one transaction. It starts
// paused; the source's routines are never resumed by import.
func (h *Handler) adoptContinuityAssistant(ctx context.Context, set continuityOutcomeSetter, scope workspacecontinuity.RestoreScope,
	dir string, inspected workspacecontinuity.Inspection, canonical []byte, workspaceRecord workspacecontinuity.Record) error {
	ws, err := agentworkspace.DecodeContinuityWorkspace(workspaceRecord, canonical)
	if err != nil {
		return err
	}
	candidate, err := inspectModernAssistant(ctx, dir, inspected, ws)
	if err != nil || candidate == nil {
		return errors.Join(workspacecontinuity.ErrIncomplete, err)
	}
	binding, err := continuityBindingFor(ctx, dir, inspected, ws)
	if err != nil {
		return err
	}
	agreement, err := readContinuityAgreement(ctx, dir, inspected)
	if err != nil {
		return err
	}
	assistants := personalassistant.NewSQLiteStore(h.store.DB())
	return h.store.DB().InTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := assistants.RestoreContinuityAgreement(ctx, tx, scope, agreement, binding); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE users SET personal_workspace_id=? WHERE id=? AND COALESCE(personal_workspace_id,'') IN ('',?)`,
			scope.WorkspaceID, scope.UserID, scope.WorkspaceID)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return workspacecontinuity.ErrConflict
		}
		return set(tx, "assistant", "restored", 1, "")
	})
}

// retainContinuityAssistant keeps a non-adopted HQ's agreement, verified
// against the reviewed entry profile, inert with the imported workspace. It
// changes no relationship, designation or profile here.
func (h *Handler) retainContinuityAssistant(ctx context.Context, set continuityOutcomeSetter, scope workspacecontinuity.RestoreScope,
	dir string, inspected workspacecontinuity.Inspection, canonical []byte, workspaceRecord workspacecontinuity.Record) error {
	ws, err := agentworkspace.DecodeContinuityWorkspace(workspaceRecord, canonical)
	if err != nil {
		return err
	}
	binding, err := continuityBindingFor(ctx, dir, inspected, ws)
	if err != nil {
		return err
	}
	agreement, err := readContinuityAgreement(ctx, dir, inspected)
	if err != nil {
		return err
	}
	if _, err := personalassistant.VerifyContinuityAgreement(agreement, binding); err != nil {
		return err
	}
	return h.store.DB().InTransaction(ctx, func(tx *sql.Tx) error {
		if err := workspacecontinuity.RetainRecords(ctx, tx, scope, "assistant", "agreements", []workspacecontinuity.Record{agreement}); err != nil {
			return err
		}
		return set(tx, "assistant", "unavailable", 0, "not_adopted")
	})
}

// readContinuityAgreement reads a member's one assistant agreement record.
func readContinuityAgreement(ctx context.Context, dir string, inspected workspacecontinuity.Inspection) (workspacecontinuity.Record, error) {
	var agreement workspacecontinuity.Record
	err := workspacecontinuity.ReadGenerationRecords(ctx, dir, workspacecontinuity.Generation(inspected), "assistant", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "agreements" || len(chunk.Records) != 1 || agreement.ID != "" {
			return workspacecontinuity.ErrInvalid
		}
		agreement = chunk.Records[0]
		return nil
	})
	if err == nil && agreement.ID == "" {
		err = workspacecontinuity.ErrIncomplete
	}
	return agreement, err
}

// continuityBindingFor rebuilds the exact assistant binding from the reviewed
// member's own entry profile, as review did.
func continuityBindingFor(ctx context.Context, dir string, inspected workspacecontinuity.Inspection, ws *agentworkspace.Workspace) (personalassistant.ContinuityBinding, error) {
	var binding personalassistant.ContinuityBinding
	entry := ""
	for _, instance := range ws.AgentInstances {
		if instance.EntryPoint {
			entry = instance.Name
		}
	}
	files := map[string]workspacecontinuity.Fingerprint{}
	for _, file := range inspected.Manifest.Files {
		files[file.Path] = file
	}
	var found bool
	err := workspacecontinuity.ReadGenerationRecords(ctx, dir, workspacecontinuity.Generation(inspected), "agents", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "profiles" {
			return nil
		}
		for _, r := range chunk.Records {
			value, _, err := agentworkspace.DecodeContinuityProfile(r, ws)
			if err != nil {
				return err
			}
			if value.Name != entry {
				continue
			}
			path := agentworkspace.WorkspaceAgentsDir + "/" + agentworkspace.Slugify(entry) + "/" + agentworkspace.WorkspaceAgentConfigFile
			_, profile, err := agentworkspace.VerifyContinuityProfileEvidence(ctx, dir, ws, r, files[path])
			if err != nil {
				return err
			}
			binding, err = personalassistant.NewContinuityBinding(session.ConvertAgentWorkspace(ws), profile)
			found = err == nil
			return err
		}
		return nil
	})
	if err == nil && !found {
		err = workspacecontinuity.ErrIncomplete
	}
	return binding, err
}
