package settingsreset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var ErrRecoveryIncomplete = errors.New("reset recovery is blocked or incomplete; preserve recovery metadata and relaunch to retry")

// RecoveryOptions are independently resolved by the process host before any
// long-lived application owner is constructed. OpenSecretStore is lazy so an
// absent, corrupt, blocked, or non-Settings receipt never touches native
// credentials. Its result must use the same installation-specific namespace
// as the later config owner.
type RecoveryOptions struct {
	DataDir         string
	SecretStore     vault.SecretStore
	OpenSecretStore func() (vault.SecretStore, error)
	Now             func() time.Time
}

// ProductionRecoveryOptions resolves and migrates the canonical Settings
// namespace only if a validated operation actually selected Settings. It does
// not contact a provider or inspect external CLI authentication.
func ProductionRecoveryOptions(dataDir string) RecoveryOptions {
	return RecoveryOptions{
		DataDir: dataDir,
		OpenSecretStore: func() (vault.SecretStore, error) {
			settingsPath := filepath.Join(dataDir, "settings.json")
			canonical := vault.NewDefaultSecretStoreForNamespace(settingsPath)
			legacy := vault.NewDefaultSecretStoreForNamespace("settings.json")
			if err := config.MigrateInstallationSecretNamespace(legacy, canonical); err != nil {
				return nil, err
			}
			return canonical, nil
		},
		Now: time.Now,
	}
}

// RecoverBeforeStores validates the private receipt against independently
// resolved startup paths, applies pending categories idempotently, and records
// named verification before normal constructors are allowed. Invalid evidence
// is never repaired or interpreted as an empty installation.
func RecoverBeforeStores(ctx context.Context, lease *resetstate.Lease, options RecoveryOptions) error {
	if lease == nil {
		return ErrLifecycleUnavailable
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	root, err := resolvePath(options.DataDir)
	if err != nil || root != lease.Path() {
		return ErrJournalInvalid
	}
	policy, err := ReadStartupPolicy(lease)
	if err != nil {
		return err
	}
	data, err := lease.Read(resetstate.OperationRecord)
	if err != nil {
		return ErrJournalInvalid
	}
	if data == nil {
		if policy.SchemaVersion != 0 {
			return ErrJournalInvalid
		}
		return nil
	}
	j, err := decodeJournal(data)
	if err != nil || j.Plan.Installation != root {
		return ErrJournalInvalid
	}
	lease.ExpectOperation()

	switch j.Operation.State {
	case StateCompleted:
		return lease.AuthorizeRecoveredRuntime()
	case StateBlocked:
		return ErrRecoveryIncomplete
	case StatePreparing:
		blockRecovery(j, "recovery_unavailable", options.Now(), "Reset admission was interrupted before writer drain completed; no category was applied.")
		return finishRecovery(lease, j)
	case StateAwaitingRestart, StateApplying, StateVerifying, StatePartialFailure:
		// Continue pending/failed categories below; completed categories are
		// immutable evidence and are never applied again.
	default:
		return ErrJournalInvalid
	}

	targets, configManager, err := validateRecoveryScope(ctx, lease, j, options)
	if err != nil {
		if allResultsPending(j.Operation.Results) {
			blockRecovery(j, "recovery_scope_changed", options.Now(), "Reset scope changed before pre-start application; no pending category was applied.")
		} else {
			markPendingUnknown(j, "Scope changed while recovering this operation.")
			j.Operation.State = StatePartialFailure
			j.Operation.Revision++
			j.Operation.UpdatedAt = options.Now().UTC()
		}
		return finishRecovery(lease, j)
	}
	if err := writeStartupPolicy(lease, j.Operation.ID, j.Plan.Preview.Selected, options.Now()); err != nil {
		if allResultsPending(j.Operation.Results) {
			blockRecovery(j, "recovery_unavailable", options.Now(), "Import-suppression policy could not be made durable; no pending category was applied.")
		} else {
			markPendingUnknown(j, "Import-suppression policy could not be verified.")
			j.Operation.State = StatePartialFailure
			j.Operation.Revision++
			j.Operation.UpdatedAt = options.Now().UTC()
		}
		return finishRecovery(lease, j)
	}
	if noUnresolvedResults(j.Operation.Results) {
		if allResultsCompleted(j.Operation.Results) {
			j.Operation.State = StateCompleted
		} else {
			j.Operation.State = StatePartialFailure
		}
		j.Operation.Revision++
		j.Operation.UpdatedAt = options.Now().UTC()
		return finishRecovery(lease, j)
	}

	j.Operation.State = StateApplying
	j.Operation.Revision++
	j.Operation.UpdatedAt = options.Now().UTC()
	if err := persistRecoveryJournal(lease, j); err != nil {
		return err
	}
	for i := range j.Operation.Results {
		if !resultUnresolved(j.Operation.Results[i]) {
			continue
		}
		j.Operation.Results[i] = applyRecoveryCategory(
			ctx, j.Operation.Results[i], j.Plan.Preview.Selected, j.Plan.Evidence,
			targets, configManager, lease,
		)
		if i == len(j.Operation.Results)-1 || noUnresolvedResults(j.Operation.Results) {
			j.Operation.State = StateVerifying
		}
		j.Operation.Revision++
		j.Operation.UpdatedAt = options.Now().UTC()
		if err := persistRecoveryJournal(lease, j); err != nil {
			return err
		}
	}
	if noUnresolvedResults(j.Operation.Results) {
		if allResultsCompleted(j.Operation.Results) {
			j.Operation.State = StateCompleted
		} else {
			j.Operation.State = StatePartialFailure
		}
	} else {
		markPendingUnknown(j, "Recovery stopped before this category could be verified.")
		j.Operation.State = StatePartialFailure
	}
	j.Operation.Revision++
	j.Operation.UpdatedAt = options.Now().UTC()
	return finishRecovery(lease, j)
}

func finishRecovery(lease *resetstate.Lease, j *journal) error {
	if err := persistRecoveryJournal(lease, j); err != nil {
		return err
	}
	if j.Operation.State != StateCompleted {
		return ErrRecoveryIncomplete
	}
	return lease.AuthorizeRecoveredRuntime()
}

func validateRecoveryScope(ctx context.Context, lease *resetstate.Lease, j *journal, options RecoveryOptions) (map[string]string, *config.Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	root := lease.Path()
	if !protectedDigestsUnchanged(ctx, j.Plan.Evidence) {
		return nil, nil, ErrScopeChanged
	}
	expected, err := independentlyResolvedTargets(root)
	if err != nil {
		return nil, nil, err
	}
	actual := make(map[string]string, len(j.Plan.Targets))
	for _, target := range j.Plan.Targets {
		want, ok := expected[target.Kind]
		if !ok {
			return nil, nil, ErrJournalInvalid
		}
		resolved, err := resolvePath(want)
		if err != nil || resolved != target.Path {
			return nil, nil, ErrScopeChanged
		}
		for _, kept := range j.Plan.Evidence.ProtectedPaths {
			if pathsOverlap(resolved, kept) {
				return nil, nil, ErrScopeChanged
			}
		}
		actual[target.Kind] = resolved
	}
	if len(actual) != len(expectedKinds(j.Plan.Preview.Selected)) {
		return nil, nil, ErrJournalInvalid
	}
	resolvedDatabase, err := resolvePath(expected["database_records"])
	if err != nil || resolvedDatabase != j.Plan.Evidence.DatabasePath {
		return nil, nil, ErrScopeChanged
	}
	report, err := database.InspectResetFile(ctx, resolvedDatabase)
	if err != nil || len(report.Problems) != 0 || report.SchemaDigest != j.Plan.Evidence.DatabaseSchema || report.Version != j.Plan.Evidence.DatabaseSchemaVersion {
		return nil, nil, ErrScopeChanged
	}
	secretStore := options.SecretStore
	if slices.Contains(j.Plan.Preview.Selected, CategorySettings) && secretStore == nil {
		if options.OpenSecretStore == nil {
			return nil, nil, ErrScopeChanged
		}
		secretStore, err = options.OpenSecretStore()
		if err != nil || secretStore == nil {
			return nil, nil, ErrScopeChanged
		}
	}
	settingsPath := expected["settings_fields"]
	manager := config.NewManagerWithSecretStore(settingsPath, secretStore)
	if err := manager.Load(); err != nil {
		return nil, nil, ErrScopeChanged
	}
	if !slices.Contains(j.Plan.Preview.Selected, CategoryAppRecords) && manager.IsWorkspaceRootConfirmed() != j.Plan.Evidence.WorkspaceRootConfirmed {
		return nil, nil, ErrScopeChanged
	}
	return actual, manager, nil
}

func independentlyResolvedTargets(root string) (map[string]string, error) {
	agentIndex := filepath.Join(root, "agents.json")
	if configured := strings.TrimSpace(os.Getenv("AGENT_STORE_PATH")); configured != "" {
		if filepath.IsAbs(configured) {
			agentIndex = configured
		} else {
			agentIndex = filepath.Join(root, configured)
		}
	}
	agentIndex, err := filepath.Abs(agentIndex)
	if err != nil {
		return nil, err
	}
	agentProfiles := filepath.Join(filepath.Dir(agentIndex), "agents")
	cleanIndex := filepath.Clean(agentIndex)
	for _, separator := range []string{"/agents/", "\\agents\\"} {
		if at := strings.LastIndex(cleanIndex, separator); at >= 0 {
			agentProfiles = cleanIndex[:at+len(separator)-1]
			break
		}
	}
	return map[string]string{
		"settings_fields":               filepath.Join(root, "settings.json"),
		"agent_index":                   cleanIndex,
		"agent_profiles":                agentProfiles,
		"agent_projection":              filepath.Join(root, "agents.json"),
		"workspace_registration_fields": filepath.Join(root, "settings.json"),
		"workspace_permissions":         filepath.Join(root, workspace.DefaultAllowlistFilename),
		"database_records":              filepath.Join(root, "sessions.db"),
		"owned_uploads":                 filepath.Join(root, "session_files"),
		"setup_fields":                  filepath.Join(root, "app_state.json"),
	}, nil
}

func expectedKinds(selected []CategoryID) map[string]bool {
	result := make(map[string]bool)
	for _, id := range selected {
		for _, kind := range targetKinds(id) {
			result[kind] = true
		}
	}
	return result
}

func applyRecoveryCategory(ctx context.Context, result CategoryResult, selected []CategoryID, evidence resolvedEvidence, targets map[string]string, manager *config.Manager, lease *resetstate.Lease) CategoryResult {
	for i := range result.Checks {
		result.Checks[i].Outcome = OutcomeFailed
		result.Checks[i].Message = "Postcondition was not verified."
	}
	result.Outcome = OutcomeFailed
	result.Message = "One or more named postconditions could not be verified."
	result.Retryable = true

	switch result.ID {
	case CategorySettings:
		preserveWorkspace := !slices.Contains(selected, CategoryAppRecords)
		beforeWorkspace, beforeVault := manager.GetWorkspaceRoot(), manager.GetVaultRoot()
		options := config.ResetPreferencesOptions{PreserveWorkspaceRoot: preserveWorkspace, PreserveVaultRoot: true}
		if err := manager.ResetPreferences(options); err != nil {
			return result
		}
		if manager.ResetPreferencesVerified(options) {
			completeResultCheck(&result, "preferences_default")
		}
		if _, err := manager.DeleteResetCredentials(); err == nil {
			completeResultCheck(&result, "provider_search_keys_absent")
		}
		workspaceRetained := !preserveWorkspace || manager.GetWorkspaceRoot() == beforeWorkspace
		if workspaceRetained && manager.GetVaultRoot() == beforeVault && protectedDigestsUnchanged(ctx, evidence) {
			completeResultCheck(&result, "retained_roots_usable")
		}
	case CategoryAgents:
		if _, err := store.ResetAgentPersistence(targets["agent_index"], targets["agent_profiles"], targets["agent_projection"]); err != nil {
			return result
		}
		completeResultCheck(&result, "old_profiles_absent")
		policy, err := ReadStartupPolicy(lease)
		if err == nil && policy.SuppressAgentRehydration {
			completeResultCheck(&result, "agent_rehydration_disabled")
		}
	case CategoryAppRecords:
		registrationsOK := manager.ClearWorkspaceRegistration() == nil
		allowlist := workspace.NewAllowlist(targets["workspace_permissions"])
		registrationsOK = allowlist.Save() == nil && registrationsOK
		if registrationsOK {
			completeResultCheck(&result, "registrations_detached")
		}
		if _, err := database.ResetAppRecords(ctx, targets["database_records"]); err == nil {
			completeResultCheck(&result, "app_records_default")
		}
		if _, err := sessionfiles.ResetOwnedUploads(targets["owned_uploads"]); err != nil {
			return result
		}
		policy, err := ReadStartupPolicy(lease)
		if err == nil && policy.SuppressWorkspaceAdoption && policy.SuppressProfileSeed {
			completeResultCheck(&result, "automatic_reattachment_disabled")
		}
		if protectedDigestsUnchanged(ctx, evidence) {
			completeResultCheck(&result, "retained_files_usable")
		}
	case CategorySetupSteps:
		setup, err := onboarding.OpenForReset(targets["setup_fields"])
		if err != nil {
			return result
		}
		beforeNamesUser, beforeNamesAssistant := setup.GetNames()
		beforeProfile := setup.GetUserProfile()
		beforeProgress := setup.GetProgression()
		beforeAssistantProgress := setup.GetAssistantProgress()
		if err := setup.ResetOnboarding(); err != nil {
			return result
		}
		verified, err := onboarding.OpenForReset(targets["setup_fields"])
		if err != nil {
			return result
		}
		state := verified.GetState()
		if !state.Completed && state.CurrentStep == 0 && state.SkippedAt.IsZero() && len(state.StepsCompleted) == 0 && len(state.StepsSkipped) == 0 {
			completeResultCheck(&result, "setup_incomplete")
		}
		afterUser, afterAssistant := verified.GetNames()
		if beforeNamesUser == afterUser && beforeNamesAssistant == afterAssistant &&
			reflect.DeepEqual(beforeProfile, verified.GetUserProfile()) &&
			reflect.DeepEqual(beforeProgress, verified.GetProgression()) &&
			reflect.DeepEqual(beforeAssistantProgress, verified.GetAssistantProgress()) {
			completeResultCheck(&result, "identity_and_progress_unchanged")
		}
	}
	if !protectedDigestsUnchanged(ctx, evidence) {
		result.Message = "Retained workspace or vault evidence changed while applying this category."
		return result
	}
	if resultChecksComplete(result) {
		result.Outcome = OutcomeCompleted
		result.Message = ""
		result.Retryable = false
	}
	return result
}

func protectedDigestsUnchanged(ctx context.Context, evidence resolvedEvidence) bool {
	if len(evidence.ProtectedDigests) != len(evidence.ProtectedPaths) {
		return false
	}
	for i, fingerprint := range evidence.ProtectedDigests {
		if fingerprint.Path != evidence.ProtectedPaths[i] {
			return false
		}
		resolved, err := resolvePath(fingerprint.Path)
		if err != nil || resolved != fingerprint.Path {
			return false
		}
		digest, err := digestProtectedPath(ctx, fingerprint.Path)
		if err != nil || digest != fingerprint.Digest {
			return false
		}
	}
	return true
}

func completeResultCheck(result *CategoryResult, name string) {
	for i := range result.Checks {
		if result.Checks[i].Name == name {
			result.Checks[i].Outcome = OutcomeCompleted
			result.Checks[i].Message = ""
		}
	}
}

func resultChecksComplete(result CategoryResult) bool {
	for _, check := range result.Checks {
		if check.Outcome != OutcomeCompleted {
			return false
		}
	}
	return true
}

func allResultsPending(results []CategoryResult) bool {
	for _, result := range results {
		if result.Outcome != OutcomePending {
			return false
		}
	}
	return true
}

func noUnresolvedResults(results []CategoryResult) bool {
	for _, result := range results {
		if resultUnresolved(result) {
			return false
		}
	}
	return true
}

func resultUnresolved(result CategoryResult) bool {
	return result.Outcome == OutcomePending ||
		result.Retryable && (result.Outcome == OutcomeFailed || result.Outcome == OutcomeUnknown)
}

func allResultsCompleted(results []CategoryResult) bool {
	for _, result := range results {
		if result.Outcome != OutcomeCompleted {
			return false
		}
	}
	return true
}

func markPendingUnknown(j *journal, message string) {
	for i := range j.Operation.Results {
		if j.Operation.Results[i].Outcome != OutcomePending {
			continue
		}
		j.Operation.Results[i].Outcome = OutcomeUnknown
		j.Operation.Results[i].Message = message
		j.Operation.Results[i].Retryable = true
		for n := range j.Operation.Results[i].Checks {
			j.Operation.Results[i].Checks[n].Outcome = OutcomeUnknown
			j.Operation.Results[i].Checks[n].Message = "Postcondition is unknown."
		}
	}
}

func blockRecovery(j *journal, code string, now time.Time, message string) {
	j.Operation.State = StateBlocked
	j.Operation.Revision++
	j.Operation.UpdatedAt = now.UTC()
	j.Operation.Blockers = []Blocker{{
		Code: code, Message: message,
		Recovery: "Preserve .ori-reset and this installation. Start Ori to view the durable result; review a new operation only after resolving the blocker.",
	}}
}

func persistRecoveryJournal(lease *resetstate.Lease, j *journal) error {
	data, err := encodeJournal(j)
	if err == nil {
		err = lease.Replace(resetstate.OperationRecord, data)
	}
	if err != nil {
		lease.MarkUncertain()
		return ErrAdmissionUncertain
	}
	lease.ExpectOperation()
	return nil
}
