package settingsreset

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestRecoverBeforeStoresAppliesSelectedCategoriesAndVerifiesSameOperation(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	fixture, owners, planner := previewFixture(t)
	paths := fixture.Paths()
	at := time.Now().UTC().Truncate(time.Second)
	if err := owners.Setup.SetProgression(types.ProgressionState{
		CompletedQuests: map[string]time.Time{"t1-first-message": at},
		SkippedQuests:   map[string]time.Time{"t2-build-hq": at},
		Dismissed:       true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := owners.Setup.SetAssistantProgress(&types.AssistantProgress{Level: 4, Experience: 120, Rank: "captain"}); err != nil {
		t.Fatal(err)
	}
	if err := owners.Allowlist.Add("retained-workspace"); err != nil {
		t.Fatal(err)
	}
	if _, err := owners.Uploads.AddFileFromReader("fixture-upload-session", strings.NewReader("owned upload"), "owned.txt", 12); err != nil {
		t.Fatal(err)
	}
	vaultBefore, err := fixture.Secrets().Get(vault.SecretKeyVaultDEK)
	if err != nil {
		t.Fatal(err)
	}

	lease, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	life := &fixtureLifecycle{drain: func(_ context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}}
	coordinator := NewCoordinator(lease, planner, life)
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{
		CategorySettings, CategoryAgents, CategoryAppRecords, CategorySetupSteps,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Blockers) != 0 {
		t.Fatalf("preview blockers: %+v", preview.Blockers)
	}
	op, err := coordinator.Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "recovery-request", Confirmation: "RESET",
	})
	if err != nil || op.State != StateAwaitingRestart {
		t.Fatalf("Stage = %+v, %v", op, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	relaunched, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relaunched.Close() })
	if err := RecoverBeforeStores(t.Context(), relaunched, RecoveryOptions{
		DataDir: paths.DataDir, SecretStore: fixture.Secrets(), Now: time.Now,
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewCoordinator(relaunched, nil, nil).Status(t.Context(), preview.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != StateCompleted || !recovered.VerifiedComplete() {
		t.Fatalf("recovered operation = %+v", recovered)
	}
	for _, result := range recovered.Results {
		if result.Outcome != OutcomeCompleted || result.Retryable {
			t.Fatalf("category not verified: %+v", result)
		}
	}
	for _, key := range []vault.SecretKey{
		vault.SecretKeyOpenAIAPIKey, vault.SecretKeyAnthropicAPIKey,
		vault.SecretKeyGeminiAPIKey, vault.SecretKeyBraveAPIKey,
	} {
		if _, err := fixture.Secrets().Get(key); !errors.Is(err, vault.ErrSecretNotFound) {
			t.Fatalf("provider/search slot %s remains: %v", key, err)
		}
	}
	if vaultAfter, err := fixture.Secrets().Get(vault.SecretKeyVaultDEK); err != nil || vaultAfter != vaultBefore {
		t.Fatalf("vault encryption slot changed: present=%t err=%v", vaultAfter != "", err)
	}
	setup, err := onboarding.OpenForReset(filepath.Join(paths.DataDir, "app_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	userName, assistantName := setup.GetNames()
	if userName != "Fixture User" || assistantName != resetfixture.AgentName {
		t.Fatalf("identity changed: %q, %q", userName, assistantName)
	}
	progress := setup.GetProgression()
	if len(progress.CompletedQuests) != 1 || len(progress.SkippedQuests) != 1 || !progress.Dismissed {
		t.Fatalf("setup reset changed progression: %#v", progress)
	}
	if _, err := os.Stat(filepath.Join(paths.DataDir, "session_files", "fixture-upload-session", "files", "owned.txt")); !os.IsNotExist(err) {
		t.Fatalf("owned upload remains: %v", err)
	}
	fixture.AssertPreserved(t)
}

func TestCompletedReceiptCanBeReplacedByOneNewReviewedOperation(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	fixture, owners, planner := previewFixture(t)
	paths := fixture.Paths()
	lease, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	life := &fixtureLifecycle{drain: func(_ context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}}
	first, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySetupSteps})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCoordinator(lease, planner, life).Stage(t.Context(), ExecuteRequest{
		PreviewID: first.ID, RequestID: "first-request", Confirmation: "RESET",
	}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	relaunched, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relaunched.Close() })
	if err := RecoverBeforeStores(t.Context(), relaunched, RecoveryOptions{DataDir: paths.DataDir, SecretStore: fixture.Secrets()}); err != nil {
		t.Fatal(err)
	}

	cfg := config.NewManagerWithSecretStore(filepath.Join(paths.DataDir, "settings.json"), fixture.Secrets())
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(paths.DataDir, "sessions.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	folders, err := workspace.NewFileStore(paths.Workspaces)
	if err != nil {
		t.Fatal(err)
	}
	secondOwners := &Owners{
		DataDir: paths.DataDir, Config: cfg, Database: db, Workspaces: folders,
		Setup:          onboarding.NewManager(filepath.Join(paths.DataDir, "app_state.json")),
		Vaults:         vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: paths.DataDir, ManagedVaultRoot: paths.Vaults}),
		CheckLifecycle: func(context.Context) []Blocker { return nil },
	}
	secondPlanner := NewPlanner(func() Owners { return *secondOwners })
	second, err := secondPlanner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySetupSteps})
	if err != nil {
		t.Fatal(err)
	}
	secondLife := &fixtureLifecycle{drain: func(_ context.Context) error {
		return errors.Join(folders.Close(), db.Close())
	}}
	staged, err := NewCoordinator(relaunched, secondPlanner, secondLife).Stage(t.Context(), ExecuteRequest{
		PreviewID: second.ID, RequestID: "second-request", Confirmation: "RESET",
	})
	if err != nil {
		t.Fatal(err)
	}
	if staged.ID == first.OperationID || staged.State != StateAwaitingRestart {
		t.Fatalf("new operation did not replace completed last result: %+v", staged)
	}
	if _, err := NewCoordinator(relaunched, nil, nil).Status(t.Context(), first.OperationID); !errors.Is(err, ErrOperationNotFound) {
		t.Fatal("retired operation remained ambiguous:", err)
	}
}

func TestBeforeStoresCompletesRecoveryAndPinsVerifiedRuntimeHandoff(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	fixture, owners, planner := previewFixture(t)
	paths := fixture.Paths()
	lease, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	life := &fixtureLifecycle{drain: func(_ context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}}
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySetupSteps})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCoordinator(lease, planner, life).Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "host-recovery-request", Confirmation: "RESET",
	}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	hostLease, err := BeforeStores(t.Context(), paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	release, err := hostLease.EnterRuntime(paths.DataDir, paths.DataDir)
	if err != nil {
		t.Fatal("verified receipt was not handed to runtime construction:", err)
	}
	release()
	if err := hostLease.Close(); !errors.Is(err, resetstate.ErrPinned) {
		t.Fatal("host recovery did not pin ownership:", err)
	}
	setup, err := onboarding.OpenForReset(filepath.Join(paths.DataDir, "app_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if state := setup.GetState(); state.Completed || state.CurrentStep != 0 {
		t.Fatalf("host did not apply setup reset before runtime: %#v", state)
	}
}

func TestRecoverBeforeStoresBlocksWhenRetainedContentChangesAfterDrain(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	fixture, owners, planner := previewFixture(t)
	paths := fixture.Paths()
	lease, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	life := &fixtureLifecycle{drain: func(_ context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}}
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySettings})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCoordinator(lease, planner, life).Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "changed-retained-request", Confirmation: "RESET",
	}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Workspaces, "retained", "new-after-drain.txt"), []byte("changed after drain"), 0o600); err != nil {
		t.Fatal(err)
	}

	relaunched, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relaunched.Close() })
	if err := RecoverBeforeStores(t.Context(), relaunched, RecoveryOptions{DataDir: paths.DataDir, SecretStore: fixture.Secrets()}); !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatal("changed retained evidence did not block recovery:", err)
	}
	blocked, err := NewCoordinator(relaunched, nil, nil).Status(t.Context(), preview.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.State != StateBlocked || len(blocked.Results) != 1 || blocked.Results[0].Outcome != OutcomePending {
		t.Fatalf("scope change applied selected data: %+v", blocked)
	}
	if _, err := fixture.Secrets().Get(vault.SecretKeyOpenAIAPIKey); err != nil {
		t.Fatal("credential changed despite pre-apply blocker:", err)
	}
}

type failDeleteOnceStore struct {
	vault.SecretStore
	key    vault.SecretKey
	failed bool
}

func (s *failDeleteOnceStore) Delete(key vault.SecretKey) error {
	if key == s.key && !s.failed {
		s.failed = true
		return errors.New("injected owned-slot delete failure")
	}
	return s.SecretStore.Delete(key)
}

func TestStartFreshRemovalTreatsAbsentNestedOwnersAsVerifiedEmpty(t *testing.T) {
	root := t.TempDir()
	targets := make(map[string]string)
	for _, kind := range targetKinds(CategoryIntegrations) {
		targets[kind] = filepath.Join(root, "missing", kind)
	}
	if err := removeFreshTargets(CategoryIntegrations, targets, root); err != nil {
		t.Fatalf("remove already-empty integration owners: %v", err)
	}
}

func TestStartFreshAppliesEveryEnumeratedOwnerAndPreservesProtectedBytes(t *testing.T) {
	f, owners, coordinator, lifecycle := coordinatorFixture(t)
	root := f.Paths().DataDir
	mustPreview(t, owners.Config.SetTemplatesRoot(filepath.Join(root, "templates")))
	mustPreview(t, owners.Config.Save())
	t.Setenv("ORI_TEMPLATES_DIR", filepath.Join(root, "templates"))
	t.Setenv("WORKFLOW_TEMPLATES_DIR", filepath.Join(root, "workflow_templates"))
	owners.FreshTargets = fixtureFreshTargets(root)
	for _, relative := range []string{
		"data/model_categories.json", "data/locations.json", "data/connections/google.json", "data/connections/consent.json",
		"data/mcp_registry.json", "data/mcp_search_sources.json", "data/mcp_search_cache.json", "data/plugins/installed.json",
		"data/plugins/marketplaces.json", "data/plugins/src/plugin/file", "data/plugins/state/plugin/file", "data/plugins/artifacts/plugin/file",
		"data/plugins/preview/plugin/file", "data/templates/custom/file", "data/workflow_templates/custom.json", "data/usage_data/usage_records.json",
		"data/activity_logs/agent.jsonl", "data/cli_agent_tasks/task/events.json", "data/cli-mcp/workspace.mcp.json",
	} {
		mustPreview(t, f.WriteFile(relative, []byte("owned reset fixture\n")))
	}
	vaultDEK, err := f.Secrets().Get(vault.SecretKeyVaultDEK)
	mustPreview(t, err)
	otherKey, err := f.OtherSecrets().Get(vault.SecretKeyOpenAIAPIKey)
	mustPreview(t, err)
	vaultPassword := rand.Text()
	retainedVault := vault.Vault{Name: "Retained Start Fresh Vault"}
	mustPreview(t, owners.Vaults.CreateVault(t.Context(), &retainedVault, vaultPassword))
	retainedRecord := vault.Record{VaultID: retainedVault.ID, Type: "personal_note", Label: "Retained", Payload: []byte(`{"owned":"vault"}`)}
	mustPreview(t, owners.Vaults.CreateRecord(t.Context(), &retainedRecord, vault.AccessContext{}))
	vaultBytes, err := os.ReadFile(retainedVault.FilePath)
	mustPreview(t, err)

	preview, err := coordinator.planner.Create(t.Context(), IntentStartFresh, nil)
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("Start Fresh blockers = %+v", preview.Blockers)
	}
	lifecycle.drain = func(context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}
	operation, err := coordinator.Stage(t.Context(), ExecuteRequest{PreviewID: preview.ID, RequestID: "fresh-fixture", Confirmation: "RESET"})
	mustPreview(t, err)
	if operation.State != StateAwaitingRestart {
		t.Fatalf("staged Start Fresh = %+v", operation)
	}
	receipt, err := coordinator.lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	privateJournal, err := decodeJournal(receipt)
	mustPreview(t, err)
	if _, _, err := validateRecoveryScope(t.Context(), coordinator.lease, privateJournal, RecoveryOptions{DataDir: root, SecretStore: f.Secrets()}); err != nil {
		expected, _ := independentlyResolvedTargets(root)
		for _, target := range privateJournal.Plan.Targets {
			if expected[target.Kind] != target.Path {
				t.Logf("target mismatch %s: expected %q, got %q", target.Kind, expected[target.Kind], target.Path)
			}
		}
		t.Fatalf("preflight fresh recovery scope: %v", err)
	}
	if err := RecoverBeforeStores(t.Context(), coordinator.lease, RecoveryOptions{DataDir: root, SecretStore: f.Secrets()}); err != nil {
		failed, _ := coordinator.Status(t.Context(), operation.ID)
		t.Fatalf("recover Start Fresh: %v: %+v", err, failed)
	}
	operation, err = coordinator.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if !operation.VerifiedComplete() || operation.Intent != IntentStartFresh || len(operation.CompletedCategories()) != 9 {
		t.Fatalf("completed Start Fresh = %+v", operation)
	}
	completedRevision := operation.Revision
	mustPreview(t, RecoverBeforeStores(t.Context(), coordinator.lease, RecoveryOptions{DataDir: root, SecretStore: f.Secrets()}))
	operation, err = coordinator.Status(t.Context(), operation.ID)
	mustPreview(t, err)
	if operation.Revision != completedRevision || !operation.VerifiedComplete() {
		t.Fatal("relaunch retried an already completed Start Fresh category")
	}

	state, err := onboarding.OpenForReset(filepath.Join(root, "app_state.json"))
	mustPreview(t, err)
	if !state.IsCanonicalFirstRun() {
		t.Fatal("app state did not return to canonical first-run defaults")
	}
	for _, target := range owners.FreshTargets {
		if target.Kind == "first_run_state" {
			continue
		}
		if _, err := os.Lstat(target.Path); !os.IsNotExist(err) {
			t.Fatalf("fresh target remains: %s: %v", target.Kind, err)
		}
	}
	if value, err := f.Secrets().Get(vault.SecretKeyVaultDEK); err != nil || value != vaultDEK {
		t.Fatal("Start Fresh changed retained vault encryption material")
	}
	if value, err := f.OtherSecrets().Get(vault.SecretKeyOpenAIAPIKey); err != nil || value != otherKey {
		t.Fatal("Start Fresh changed an unrelated secret namespace")
	}
	afterVaultBytes, err := os.ReadFile(retainedVault.FilePath)
	if err != nil || !bytes.Equal(vaultBytes, afterVaultBytes) {
		t.Fatal("Start Fresh changed retained vault package bytes")
	}
	reopenedDB, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, "sessions.db"), WALMode: true})
	mustPreview(t, err)
	t.Cleanup(func() { _ = reopenedDB.Close() })
	reopenedVaults := vault.NewStore(reopenedDB, vault.StoreOptions{VaultFilesBaseDir: root, ManagedVaultRoot: f.Paths().Vaults})
	registered, err := reopenedVaults.ListVaults(t.Context())
	mustPreview(t, err)
	if len(registered) != 0 {
		t.Fatal("retained vault package reattached without user action")
	}
	attached, err := reopenedVaults.AttachVaultPackage(t.Context(), filepath.Dir(retainedVault.FilePath))
	mustPreview(t, err)
	if attached.ID != retainedVault.ID {
		t.Fatal("attached retained vault identity changed")
	}
	mustPreview(t, reopenedVaults.Unlock(t.Context(), retainedVault.ID, vaultPassword))
	decrypted, err := reopenedVaults.GetRecord(t.Context(), retainedRecord.ID, vault.AccessContext{})
	if err != nil || !bytes.Equal(decrypted.Payload, retainedRecord.Payload) {
		t.Fatal("explicitly attached vault did not decrypt retained record")
	}
	f.AssertPreserved(t)
}

func TestRecoverBeforeStoresRetainsPartialFailureAndRetriesOnlyUnresolvedCategory(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	fixture, owners, planner := previewFixture(t)
	paths := fixture.Paths()
	lease, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	life := &fixtureLifecycle{drain: func(_ context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}}
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySettings})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCoordinator(lease, planner, life).Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "retry-request", Confirmation: "RESET",
	}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	relaunched, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relaunched.Close() })
	flaky := &failDeleteOnceStore{SecretStore: fixture.Secrets(), key: vault.SecretKeyOpenAIAPIKey}
	if err := RecoverBeforeStores(t.Context(), relaunched, RecoveryOptions{DataDir: paths.DataDir, SecretStore: flaky}); !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatal("partial recovery did not hold startup:", err)
	}
	partial, err := NewCoordinator(relaunched, nil, nil).Status(t.Context(), preview.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if partial.State != StatePartialFailure || len(partial.Results) != 1 || partial.Results[0].Outcome != OutcomeFailed || !partial.Results[0].Retryable {
		t.Fatalf("partial result not retained for retry: %+v", partial)
	}
	firstRevision := partial.Revision
	if err := RecoverBeforeStores(t.Context(), relaunched, RecoveryOptions{DataDir: paths.DataDir, SecretStore: fixture.Secrets()}); err != nil {
		t.Fatal(err)
	}
	completed, err := NewCoordinator(relaunched, nil, nil).Status(t.Context(), preview.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != StateCompleted || !completed.VerifiedComplete() || completed.Revision <= firstRevision {
		t.Fatalf("retry did not complete the same operation: %+v", completed)
	}
}
