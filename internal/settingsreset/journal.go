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
	selectionInput := v.Selected
	if v.Intent == IntentStartFresh {
		selectionInput = nil
	}
	selected, err := Selection(v.Intent, selectionInput)
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
	// Plugin ownership carries its own narrower boundary rather than relaxing the
	// generic target rules above. It is required exactly when the reviewed scope
	// owns installed plugins and refused otherwise, so an unrelated receipt can
	// never smuggle an external deletion root in.
	switch {
	case evidence.Plugins != nil:
		if !pluginEvidencePermitted(selected) {
			return ErrJournalInvalid
		}
		if err := validatePluginEvidence(root, evidence.Plugins); err != nil {
			return err
		}
	case pluginEvidenceRequired(selected):
		return ErrJournalInvalid
	}
	// The agents folder likewise keeps its own boundary: only with Agents
	// selected, and only as the Agents folder directly inside a retained
	// workspace root. Its absence is always valid — nothing is removed there.
	if evidence.AgentsFolder != "" {
		if !slices.Contains(selected, CategoryAgents) || len(evidence.AgentsFolder) > 4096 ||
			!agentsFolderWithinRetainedRoot(evidence.AgentsFolder, evidence.ProtectedPaths) ||
			containsPath(evidence.AgentsFolder, root) || evidence.AgentsFolder == root ||
			containsPath(filepath.Join(root, resetstate.Directory), evidence.AgentsFolder) {
			return ErrJournalInvalid
		}
		if v.AgentsFolder == nil || v.AgentsFolder.Path != evidence.AgentsFolder {
			return ErrJournalInvalid
		}
	} else if v.AgentsFolder != nil {
		return ErrJournalInvalid
	}
	// Start Fresh's Skills folder and plugin list may only be the ones beside
	// the reviewed agents folder, and only for Start Fresh.
	if evidence.SkillsFolder != "" || evidence.PluginList != "" {
		if v.Intent != IntentStartFresh || !validFreshSiblings(evidence) {
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
		if err := validateCategoryMembers(category, result, evidence.Plugins); err != nil {
			return err
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
		for _, kind := range optionalTargetKinds(id) {
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
	// Every required kind appeared exactly once; an optional one may be absent.
	for kind, id := range wantKinds {
		if !slices.Contains(optionalTargetKinds(id), kind) {
			return ErrJournalInvalid
		}
	}
	return nil
}

// optionalTargetKinds are target kinds a category may carry. A receipt written
// before the kind existed simply lacks it, and lacking it only ever means less
// is removed.
func optionalTargetKinds(id CategoryID) []string {
	if id == CategoryAgents {
		return []string{"agent_state"}
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
	case CategoryInstalledPlugins:
		// These are scopes the category edits within, not roots it deletes. The
		// personal skills root is deliberately absent: it lives outside the
		// installation and is carried by the dedicated plugin evidence instead.
		return []string{"plugin_registry_records", "plugin_mcp_entries", "plugin_surface_state", "plugin_managed_artifacts", "plugin_managed_clones", "plugin_preview_state"}
	case CategoryIdentityProgress:
		return []string{"first_run_state"}
	case CategoryAppConfiguration:
		return []string{"model_categories", "location_zones"}
	case CategoryIntegrations:
		return []string{"connection_metadata", "connection_consent", "mcp_registry", "mcp_search_sources", "mcp_search_cache", "plugin_registry", "plugin_marketplaces", "plugin_clones", "plugin_state", "plugin_artifacts", "plugin_preview"}
	case CategoryTemplates:
		return []string{"project_templates", "post_reset_project_templates", "workflow_templates"}
	case CategoryActivity:
		return []string{"usage_records", "activity_logs", "cli_event_logs"}
	case CategoryRuntimeCache:
		return []string{"cli_mcp_configs"}
	default:
		return nil
	}
}
