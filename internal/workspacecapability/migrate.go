package workspacecapability

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// LegacyStateProbe reports whether a workspace already has configured legacy
// Downloads Janitor state on disk.
//
// It is an interface rather than a direct call because the dependency runs the
// other way: the Janitor package imports this one (for its capability runtime,
// status contract, and identifier aliases), so this package cannot import the
// Janitor. The server implements this with the Janitor's own settings store.
type LegacyStateProbe interface {
	// HasConfiguredJanitorState reports whether workspaceID has a completed
	// Downloads Janitor setup — an approved root with a directory reference.
	// It must not report true merely because a state file exists.
	HasConfiguredJanitorState(workspaceID string) bool
}

// MigrationWorkspaceStore is the slice of the workspace store the backfill
// needs.
type MigrationWorkspaceStore interface {
	WorkspaceStore
	List() ([]string, error)
}

// LegacyDownloadsTemplateID is the built-in template a Downloads Janitor
// workspace was created from. It is one of the three authoritative signals.
const LegacyDownloadsTemplateID = "downloads-janitor"

// MigrationResult summarizes one backfill pass.
type MigrationResult struct {
	// Scanned is how many workspaces were examined.
	Scanned int
	// Migrated is how many gained a file-janitor install record.
	Migrated int
	// AlreadyInstalled is how many already had one (the steady state after the
	// first pass).
	AlreadyInstalled int
	// Skipped is how many carried no authoritative legacy signal.
	Skipped int
	// Failed is how many could not be migrated. Their legacy state is left
	// untouched and the rest of the pass continues (FR-139).
	Failed int
}

// Migrator backfills the file-janitor install record for workspaces that were
// already using Downloads Janitor before capabilities existed (FR-125).
//
// It is deliberately conservative. Installing the capability on a workspace
// that never had the Janitor would put a station on its Map and claim a
// capability the user never chose, so the backfill acts only on evidence that
// the workspace *was actually running* the feature.
type Migrator struct {
	registry *Registry
	store    MigrationWorkspaceStore
	probe    LegacyStateProbe
}

// NewMigrator builds the backfill. A nil probe simply removes the
// configured-state signal; the other two still apply.
func NewMigrator(registry *Registry, store MigrationWorkspaceStore, probe LegacyStateProbe) *Migrator {
	return &Migrator{registry: registry, store: store, probe: probe}
}

// Run backfills every workspace that carries an authoritative legacy signal.
//
// It is safe to call on every startup: a workspace that already holds the
// install record is counted and skipped, so repeated runs cannot duplicate
// records, directory references, watchers, schedules, stations, tasks, or
// agents (FR-127).
//
// A per-workspace failure is isolated: it is counted, logged with identifiers
// only, and the pass continues. The failed workspace keeps its legacy state
// exactly as it was, so a later run can retry (FR-139).
func (m *Migrator) Run() MigrationResult {
	var result MigrationResult
	if m == nil || m.store == nil || m.registry == nil {
		return result
	}
	if !m.registry.Has(workspace.CapabilityFileJanitor) {
		// This build does not compile File Janitor in. Backfilling an ID that
		// resolves to nothing would create install records the user cannot act
		// on (FR-14).
		return result
	}

	ids, err := m.store.List()
	if err != nil {
		logger.Warn("File Janitor migration could not list workspaces", logger.Fields{"error": err.Error()})
		return result
	}

	for _, id := range ids {
		result.Scanned++
		switch m.migrateOne(id) {
		case migrationMigrated:
			result.Migrated++
		case migrationAlreadyInstalled:
			result.AlreadyInstalled++
		case migrationSkipped:
			result.Skipped++
		case migrationFailed:
			result.Failed++
		}
	}

	if result.Migrated > 0 || result.Failed > 0 {
		logger.Info("File Janitor capability backfill", logger.Fields{
			"scanned":  result.Scanned,
			"migrated": result.Migrated,
			"skipped":  result.Skipped,
			"failed":   result.Failed,
		})
	}
	return result
}

type migrationOutcome int

const (
	migrationSkipped migrationOutcome = iota
	migrationMigrated
	migrationAlreadyInstalled
	migrationFailed
)

func (m *Migrator) migrateOne(workspaceID string) migrationOutcome {
	ws, err := m.store.Get(workspaceID)
	if err != nil || ws == nil {
		// Not a migration failure: a workspace that cannot be read has nothing
		// to migrate, and reporting it as failed would make a clean install
		// look broken.
		return migrationSkipped
	}

	if ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		return migrationAlreadyInstalled
	}
	if !m.hasAuthoritativeLegacySignal(ws) {
		return migrationSkipped
	}

	record := workspace.InstalledCapability{
		ID:      workspace.CapabilityFileJanitor,
		Version: FileJanitorDefinitionVersion,
		Source:  workspace.InstallSourceLegacyMigration,
	}

	// The install record is the ONLY thing this writes. It grants no directory,
	// registers no watcher, enables no schedule, creates no agent or task, and
	// changes no name, location, or template provenance (FR-129, FR-130).
	// Whatever automation the workspace was already running keeps running,
	// under its existing registration; whatever was paused stays paused.
	if err := m.store.Update(workspaceID, func(w *workspace.Workspace) error {
		if w.HasInstalledCapability(workspace.CapabilityFileJanitor) {
			return nil
		}
		_, addErr := w.AddInstalledCapability(record)
		return addErr
	}); err != nil {
		logger.Warn("File Janitor migration failed for one workspace", logger.Fields{
			"workspace_id": workspaceID,
			"capability":   workspace.CapabilityFileJanitor,
			"error":        err.Error(),
		})
		return migrationFailed
	}
	return migrationMigrated
}

// hasAuthoritativeLegacySignal reports whether this workspace was actually
// running Downloads Janitor (FR-126).
//
// Exactly three signals count, and the list is closed on purpose. Everything
// tempting and adjacent is explicitly NOT evidence (FR-136):
//
//   - the workspace's name, however much it looks like "Downloads" or "Janitor"
//   - an ordinary directory reference, even one pointing at ~/Downloads
//   - a folder watcher, which any feature may register
//   - a Filed/ directory on disk, which a user may have made by hand
//   - an agent named "Downloads Curator" or similar
//
// None of those mean the capability was installed; acting on them would put a
// station on an unrelated workspace and claim access the user never granted.
func (m *Migrator) hasAuthoritativeLegacySignal(ws *workspace.Workspace) bool {
	// 1. Built-in Downloads Janitor template provenance.
	if ws.IsFromTemplate(LegacyDownloadsTemplateID) {
		return true
	}

	// 2. Configured Janitor state: an approved root the user actually granted.
	if m.probe != nil && m.probe.HasConfiguredJanitorState(ws.ID) {
		return true
	}

	// 3. A pending Downloads Janitor setup requirement — the workspace was
	//    created for the Janitor and is mid-setup. Matched under both the legacy
	//    key and the canonical one.
	setup := FileJanitorDefinition().Setup
	for _, req := range ws.PendingDirectoryRequirements() {
		if setup.MatchesDirectoryRequirementKey(req.Key) {
			return true
		}
	}

	return false
}

// LooksLikeJanitorByName reports whether a workspace's name merely resembles the
// Janitor's.
//
// It exists to be called by tests, not by the migration: naming it, exporting
// it, and having the migration never consult it makes the FR-136 boundary
// explicit rather than implicit in an absence of code.
func LooksLikeJanitorByName(name string) bool {
	lowered := strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(lowered, "janitor") || strings.Contains(lowered, "downloads")
}
