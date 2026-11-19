package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Store nodes were canvas objects that said where a ticket's result file was
// written. They are retired: every target now lives on the ticket. A workspace is
// rewritten wherever it is decoded (workspace.MigrateLegacyStoreNodes), so nothing
// at runtime ever sees a store node. This pass is the one place that also writes
// the rewrite back, once, at startup, so the workspace file and the SQLite row stop
// carrying the retired keys without waiting for the next save.
//
// It backs up exactly what it is about to replace, saves only through the
// workspace store's own Update (which restores folder-only fields such as
// `designation` before the save), and records what it did in a marker file. It
// never short-circuits on the marker: the legacy filter is cheap, and a workspace
// that failed once is retried on the next start.

const (
	// StoreNodeMigrationNotNeeded: nothing carries a store node; no backup and no
	// marker are written.
	StoreNodeMigrationNotNeeded = "not_needed"
	// StoreNodeMigrationDone: every workspace that carried a store node was rewritten.
	StoreNodeMigrationDone = "done"
	// StoreNodeMigrationBackupFailed: the backup could not be made, so nothing was
	// saved. The next start tries again.
	StoreNodeMigrationBackupFailed = "backup_failed"
	// StoreNodeMigrationIncomplete: some workspaces were rewritten and some failed;
	// the failures are listed and retried on the next start.
	StoreNodeMigrationIncomplete = "incomplete"
)

// StoreNodeMigrationMarker is the data-dir file recording the latest run that had
// anything to do.
const StoreNodeMigrationMarker = "store_nodes_migrated.json"

// StoreNodeMigrationReport is what one run did. The marker file holds the same
// fields.
type StoreNodeMigrationReport struct {
	Status     string                        `json:"status"`
	MigratedAt time.Time                     `json:"migrated_at"`
	Backup     string                        `json:"backup,omitempty"`
	Migrated   []string                      `json:"migrated"`
	Failed     map[string]string             `json:"failed,omitempty"`
	Lossy      []workspace.LegacyStoreChange `json:"lossy,omitempty"`
	Error      string                        `json:"error,omitempty"`
}

// StoreNodeWorkspaceUpdater is the workspace store the rewrite is saved through:
// the SQLite-primary SyncStore, or the folder store when SQLite is not primary.
type StoreNodeWorkspaceUpdater interface {
	Update(wsID string, fn func(*workspace.Workspace) error) error
}

// StoreNodeMigration rewrites every workspace that still carries a store node.
type StoreNodeMigration struct {
	// DataDir holds the backup (<DataDir>/recovery/...) and the marker.
	DataDir string
	// DB is the SQLite database whose workspaces table may hold legacy rows. Nil
	// when SQLite is not in use.
	DB *database.DB
	// Workspaces is the store the rewrite is saved through.
	Workspaces StoreNodeWorkspaceUpdater
	// Folders is the folder store whose workspace.json files may hold legacy keys.
	// Nil when there is none.
	Folders *workspace.FileStore
	// Now defaults to time.Now.
	Now func() time.Time
}

// legacyStoreEvidence is the raw, unmigrated state of one workspace that carries a
// store node. It is what the backup keeps and what the lossy report is computed
// from, because the stores decode (and so migrate) a workspace before this pass
// can see it.
type legacyStoreEvidence struct {
	sqlStoreNodes string
	sqlTasks      string
	hasSQL        bool
	filePath      string
	fileRaw       []byte
	hasFile       bool
}

// Run does the migration once. The error is only for something that stops the
// pass from starting at all; every outcome, including failures, is in the report.
func (m StoreNodeMigration) Run() (*StoreNodeMigrationReport, error) {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	report := &StoreNodeMigrationReport{Status: StoreNodeMigrationNotNeeded, Migrated: []string{}}

	evidence, err := m.detect()
	if err != nil {
		return report, err
	}
	if len(evidence) == 0 {
		return report, nil
	}
	if m.Workspaces == nil {
		return report, errors.New("store node migration needs a workspace store to save through")
	}

	at := now().UTC()
	report.MigratedAt = at
	ids := make([]string, 0, len(evidence))
	for id := range evidence {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	backup := filepath.Join(m.DataDir, "recovery", "store-nodes-to-tickets-"+at.Format("20060102T150405Z"))
	report.Backup = backup
	if err := backupLegacyStoreNodes(backup, ids, evidence); err != nil {
		report.Status = StoreNodeMigrationBackupFailed
		report.Error = err.Error()
		logger.Warn("Store node migration: backup failed, nothing was changed", logger.Fields{"backup": backup, "error": err.Error()})
		m.writeMarker(report)
		return report, nil
	}

	for _, id := range ids {
		report.Lossy = append(report.Lossy, lossyFromEvidence(id, evidence[id])...)
		if err := m.Workspaces.Update(id, func(*workspace.Workspace) error { return nil }); err != nil {
			recordStoreNodeFailure(report, id, err.Error())
			continue
		}
		report.Migrated = append(report.Migrated, id)
	}

	// A save that reports success is not trusted on its own: read the stores back.
	if remaining, err := m.detect(); err != nil {
		report.Error = "could not verify the rewrite: " + err.Error()
	} else {
		for id := range remaining {
			if _, failed := report.Failed[id]; !failed {
				recordStoreNodeFailure(report, id, "still carries store nodes after the rewrite")
			}
		}
		report.Migrated = withoutIDs(report.Migrated, report.Failed)
	}

	report.Status = StoreNodeMigrationDone
	if len(report.Failed) > 0 {
		report.Status = StoreNodeMigrationIncomplete
	}
	logger.Info("Store node migration finished", logger.Fields{
		"status": report.Status, "migrated": len(report.Migrated), "failed": len(report.Failed), "lossy": len(report.Lossy), "backup": backup,
	})
	m.writeMarker(report)
	return report, nil
}

func recordStoreNodeFailure(report *StoreNodeMigrationReport, id, reason string) {
	if report.Failed == nil {
		report.Failed = map[string]string{}
	}
	report.Failed[id] = reason
	logger.Warn("Store node migration: workspace not rewritten", logger.Fields{"workspace_id": id, "error": reason})
}

func withoutIDs(ids []string, drop map[string]string) []string {
	kept := ids[:0:0]
	for _, id := range ids {
		if _, gone := drop[id]; !gone {
			kept = append(kept, id)
		}
	}
	return kept
}

// detect finds every workspace that still carries a store node, in the SQLite
// table and in the folder store's workspace.json files, as raw evidence.
func (m StoreNodeMigration) detect() (map[string]*legacyStoreEvidence, error) {
	found := map[string]*legacyStoreEvidence{}
	get := func(id string) *legacyStoreEvidence {
		if found[id] == nil {
			found[id] = &legacyStoreEvidence{}
		}
		return found[id]
	}

	if m.DB != nil {
		// The filter is a cheap pre-check; the decode below is what decides.
		rows, err := m.DB.QueryContext(context.Background(),
			`SELECT id, store_nodes_json, tasks_json FROM workspaces
			 WHERE store_nodes_json NOT IN ('', '[]', 'null') OR tasks_json LIKE '%"store_node_id":%'`)
		if err != nil {
			return nil, fmt.Errorf("list legacy workspace rows: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			var storeNodes, tasks sql.NullString
			if err := rows.Scan(&id, &storeNodes, &tasks); err != nil {
				return nil, fmt.Errorf("read legacy workspace row: %w", err)
			}
			if !rawCarriesStoreNodes(storeNodes.String, tasks.String) {
				continue
			}
			e := get(id)
			e.sqlStoreNodes, e.sqlTasks, e.hasSQL = storeNodes.String, tasks.String, true
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("list legacy workspace rows: %w", err)
		}
	}

	if m.Folders != nil {
		ids, err := m.Folders.List()
		if err != nil {
			return nil, fmt.Errorf("list folder workspaces: %w", err)
		}
		for _, id := range ids {
			folder, err := m.Folders.GetFolderPath(id)
			if err != nil {
				continue
			}
			path := filepath.Join(folder, workspace.WorkspaceConfigFile)
			raw, err := os.ReadFile(path) // #nosec G304 -- a workspace.json inside a workspace folder the store itself resolved
			if err != nil {
				continue
			}
			if !fileCarriesStoreNodes(raw) {
				continue
			}
			e := get(id)
			e.filePath, e.fileRaw, e.hasFile = path, raw, true
		}
	}
	return found, nil
}

// rawCarriesStoreNodes decides from a row's two JSON columns.
func rawCarriesStoreNodes(storeNodesJSON, tasksJSON string) bool {
	var nodes []json.RawMessage
	if strings.TrimSpace(storeNodesJSON) != "" && json.Unmarshal([]byte(storeNodesJSON), &nodes) == nil && len(nodes) > 0 {
		return true
	}
	var tasks []struct {
		ResultStorage *struct {
			StoreNodeID string `json:"store_node_id"`
		} `json:"result_storage"`
	}
	if strings.TrimSpace(tasksJSON) != "" && json.Unmarshal([]byte(tasksJSON), &tasks) == nil {
		for _, task := range tasks {
			if task.ResultStorage != nil && task.ResultStorage.StoreNodeID != "" {
				return true
			}
		}
	}
	return false
}

// fileCarriesStoreNodes decides from a workspace.json's raw bytes.
func fileCarriesStoreNodes(raw []byte) bool {
	var doc struct {
		StoreNodes []json.RawMessage `json:"store_nodes"`
		Tasks      json.RawMessage   `json:"tasks"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	return len(doc.StoreNodes) > 0 || rawCarriesStoreNodes("", string(doc.Tasks))
}

// lossyFromEvidence reports what the rewrite will not reproduce exactly, computed
// from the raw state the stores no longer show. It changes nothing.
func lossyFromEvidence(id string, e *legacyStoreEvidence) []workspace.LegacyStoreChange {
	ws := &workspace.Workspace{ID: id}
	switch {
	case e.hasSQL:
		if strings.TrimSpace(e.sqlStoreNodes) != "" {
			_ = json.Unmarshal([]byte(e.sqlStoreNodes), &ws.LegacyStoreNodes)
		}
		if strings.TrimSpace(e.sqlTasks) != "" {
			_ = json.Unmarshal([]byte(e.sqlTasks), &ws.Tasks)
		}
	case e.hasFile:
		if err := json.Unmarshal(e.fileRaw, ws); err != nil {
			return nil
		}
		ws.ID = id
	}
	return workspace.MigrateLegacyStoreNodesWithReport(ws).Lossy
}

// backupLegacyStoreNodes keeps the raw SQLite columns and the raw workspace.json of
// every workspace about to be rewritten.
func backupLegacyStoreNodes(dir string, ids []string, evidence map[string]*legacyStoreEvidence) error {
	for _, id := range ids {
		if id == "" || id != filepath.Base(id) || id == "." || id == ".." {
			return fmt.Errorf("workspace id %q cannot be a backup folder name", id)
		}
		e := evidence[id]
		wsDir := filepath.Join(dir, id)
		if err := os.MkdirAll(wsDir, 0o750); err != nil {
			return fmt.Errorf("create backup folder: %w", err)
		}
		if e.hasSQL {
			data, err := json.MarshalIndent(map[string]string{"store_nodes_json": e.sqlStoreNodes, "tasks_json": e.sqlTasks}, "", "  ")
			if err != nil {
				return fmt.Errorf("encode sqlite backup for %s: %w", id, err)
			}
			if err := os.WriteFile(filepath.Join(wsDir, "sqlite.json"), data, 0o600); err != nil {
				return fmt.Errorf("write sqlite backup for %s: %w", id, err)
			}
		}
		if e.hasFile {
			if err := os.WriteFile(filepath.Join(wsDir, "workspace.json"), e.fileRaw, 0o600); err != nil {
				return fmt.Errorf("write workspace.json backup for %s: %w", id, err)
			}
		}
	}
	return nil
}

// writeMarker records the run atomically. A marker that cannot be written is
// logged and does not undo the migration.
func (m StoreNodeMigration) writeMarker(report *StoreNodeMigrationReport) {
	if m.DataDir == "" {
		return
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		logger.Warn("Store node migration: could not encode the marker", logger.Fields{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(m.DataDir, 0o750); err != nil {
		logger.Warn("Store node migration: could not create the data dir for the marker", logger.Fields{"error": err.Error()})
		return
	}
	path := filepath.Join(m.DataDir, StoreNodeMigrationMarker)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		logger.Warn("Store node migration: could not write the marker", logger.Fields{"error": err.Error()})
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		logger.Warn("Store node migration: could not move the marker into place", logger.Fields{"error": err.Error()})
	}
}
