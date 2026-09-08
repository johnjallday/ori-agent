package settingsreset

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

var ErrJournalInvalid = errors.New("reset receipt is invalid or unsupported; preserve it and recover before opening stores")

// This private journal is evidence, never authority to execute recovered paths.
// Pre-start apply must resolve owners independently and compare the reviewed scope.
// Only admission states are enabled here; adding apply states requires their real
// recovery/verification implementation, not merely another accepted enum value.
type journal struct {
	Version    int          `json:"version"`
	AcceptedBy string       `json:"accepted_by"`
	RequestID  string       `json:"request_id"`
	Plan       resolvedPlan `json:"plan"`
	Operation  Operation    `json:"operation"`
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		allowed := ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_'
		if !allowed {
			return false
		}
	}
	return true
}

func encodeJournal(j *journal) ([]byte, error) {
	if err := validateJournal(j); err != nil {
		return nil, err
	}
	data, err := json.Marshal(j)
	if err != nil {
		return nil, ErrJournalInvalid
	}
	if len(data) > resetstate.MaxRecordBytes {
		return nil, resetstate.ErrRecordLimit
	}
	return data, nil
}

func decodeJournal(data []byte) (*journal, error) {
	if len(data) == 0 || len(data) > resetstate.MaxRecordBytes {
		return nil, ErrJournalInvalid
	}
	var j journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, ErrJournalInvalid
	}
	canonical, err := encodeJournal(&j)
	// Private files have exactly one encoder. Refuse unknown/duplicate/case-
	// variant fields, missing fields, trailing JSON and manual reformatting
	// rather than silently repairing ambiguous recovery evidence.
	if err != nil || !bytes.Equal(data, canonical) {
		return nil, ErrJournalInvalid
	}
	return &j, nil
}

func validateJournal(j *journal) error {
	p, op := &j.Plan, &j.Operation
	v := &p.Preview
	if j.Version != SchemaVersion || op.SchemaVersion != SchemaVersion || v.SchemaVersion != SchemaVersion ||
		!validID(j.AcceptedBy) || !validID(j.RequestID) || !validID(v.ID) || !validID(op.ID) || op.ID != v.OperationID ||
		op.Intent != v.Intent || op.Revision == 0 || op.CreatedAt.IsZero() || op.UpdatedAt.Before(op.CreatedAt) ||
		v.ExpiresAt.IsZero() || len(v.ScopeDigest) != 64 || len(v.Blockers) != 0 || len(v.Dependencies) != 0 || v.RetryOperationID != "" ||
		op.Restart.Mode != RestartProcessRelaunch || op.Restart != v.Restart {
		return ErrJournalInvalid
	}
	if _, err := hex.DecodeString(v.ScopeDigest); err != nil || op.ID == v.ID || op.CreatedAt.After(v.ExpiresAt) {
		return ErrJournalInvalid
	}
	if op.Revision > 1024 || (op.State == StatePreparing && op.Revision != 1) || (op.State != StatePreparing && op.Revision < 2) {
		return ErrJournalInvalid
	}
	selected, err := Selection(v.Intent, v.Selected)
	if err != nil || !slices.Equal(selected, v.Selected) || len(v.Categories) != len(selected) || len(op.Results) != len(selected) {
		return ErrJournalInvalid
	}
	root := p.Installation
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Dir(root) == root || len(root) > 4096 {
		return ErrJournalInvalid
	}
	evidence := p.Evidence
	if !filepath.IsAbs(evidence.DatabasePath) || filepath.Clean(evidence.DatabasePath) != evidence.DatabasePath ||
		!containsPath(root, evidence.DatabasePath) || len(evidence.DatabaseSchema) != 64 || evidence.DatabaseSchemaVersion <= 0 {
		return ErrJournalInvalid
	}
	if _, err := hex.DecodeString(evidence.DatabaseSchema); err != nil {
		return ErrJournalInvalid
	}
	if !slices.IsSorted(evidence.ProtectedPaths) || len(evidence.ProtectedDigests) != len(evidence.ProtectedPaths) {
		return ErrJournalInvalid
	}
	for i, path := range evidence.ProtectedPaths {
		fingerprint := evidence.ProtectedDigests[i]
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 ||
			(i > 0 && evidence.ProtectedPaths[i-1] == path) || fingerprint.Path != path || len(fingerprint.Digest) != 64 {
			return ErrJournalInvalid
		}
		if _, err := hex.DecodeString(fingerprint.Digest); err != nil {
			return ErrJournalInvalid
		}
	}
	wantKinds := make(map[string]CategoryID)
	allPending, allComplete, hasUnresolved := true, true, false
	for i, id := range selected {
		def, _ := definition(id)
		category, result := v.Categories[i], op.Results[i]
		if category.ID != id || result.ID != id || len(result.Checks) != len(def.Checks) ||
			!reflect.DeepEqual(category.Retained, result.Retained) {
			return ErrJournalInvalid
		}
		switch result.Outcome {
		case OutcomePending:
			if result.Retryable || result.Message != "" {
				return ErrJournalInvalid
			}
			allComplete = false
		case OutcomeCompleted:
			if result.Retryable {
				return ErrJournalInvalid
			}
			allPending = false
		case OutcomeFailed, OutcomeUnknown:
			if !result.Retryable || result.Message == "" {
				return ErrJournalInvalid
			}
			allPending, allComplete, hasUnresolved = false, false, true
		default:
			return ErrJournalInvalid
		}
		for n, name := range def.Checks {
			check := result.Checks[n]
			if check.Name != name {
				return ErrJournalInvalid
			}
			switch result.Outcome {
			case OutcomePending:
				if check.Outcome != OutcomePending || check.Message != "" {
					return ErrJournalInvalid
				}
			case OutcomeCompleted:
				if check.Outcome != OutcomeCompleted {
					return ErrJournalInvalid
				}
			default:
				if check.Outcome != OutcomeCompleted && check.Outcome != OutcomeFailed && check.Outcome != OutcomeUnknown {
					return ErrJournalInvalid
				}
			}
		}
		for _, kind := range targetKinds(id) {
			wantKinds[kind] = id
		}
	}
	switch op.State {
	case StatePreparing:
		if !allPending || len(op.Blockers) != 0 {
			return ErrJournalInvalid
		}
	case StateAwaitingRestart:
		if !allPending || len(op.Blockers) != 0 || op.Revision != 2 {
			return ErrJournalInvalid
		}
	case StateApplying:
		if len(op.Blockers) != 0 || allComplete {
			return ErrJournalInvalid
		}
	case StateVerifying:
		if len(op.Blockers) != 0 {
			return ErrJournalInvalid
		}
	case StateCompleted:
		if !allComplete || len(op.Blockers) != 0 {
			return ErrJournalInvalid
		}
	case StatePartialFailure:
		if !hasUnresolved || len(op.Blockers) != 0 {
			return ErrJournalInvalid
		}
	case StateBlocked:
		if !allPending || len(op.Blockers) != 1 ||
			!slices.Contains([]string{"drain_failed", "scope_changed_after_fence", "retained_evidence_unavailable", "recovery_scope_changed", "recovery_unavailable"}, op.Blockers[0].Code) {
			return ErrJournalInvalid
		}
	default:
		return ErrJournalInvalid
	}
	if len(p.Targets) != len(wantKinds) {
		return ErrJournalInvalid
	}
	for _, target := range p.Targets {
		if id, ok := wantKinds[target.Kind]; !ok || id != target.Category {
			return ErrJournalInvalid
		}
		delete(wantKinds, target.Kind)
		if !filepath.IsAbs(target.Path) || filepath.Clean(target.Path) != target.Path || len(target.Path) > 4096 ||
			target.Path == root || !containsPath(root, target.Path) || containsPath(filepath.Join(root, resetstate.Directory), target.Path) {
			return ErrJournalInvalid
		}
		for _, kept := range evidence.ProtectedPaths {
			if pathsOverlap(target.Path, kept) {
				return ErrJournalInvalid
			}
		}
	}
	return nil
}

func targetKinds(id CategoryID) []string {
	switch id {
	case CategorySettings:
		return []string{"settings_fields"}
	case CategoryAgents:
		return []string{"agent_index", "agent_profiles", "agent_projection"}
	case CategoryAppRecords:
		return []string{"workspace_registration_fields", "workspace_permissions", "database_records", "owned_uploads"}
	case CategorySetupSteps:
		return []string{"setup_fields"}
	default:
		return nil
	}
}
