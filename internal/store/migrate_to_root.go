package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
)

// Migration outcomes, as recorded in RootMigrationReport.Status.
const (
	// MigrationDone: every movable agent is in the root (now or on an
	// earlier start); the marker file exists.
	MigrationDone = "done"
	// MigrationNotNeeded: the data dir holds no user agents to move.
	MigrationNotNeeded = "not_needed"
	// MigrationRootMissing: the root does not exist; nothing was touched.
	MigrationRootMissing = "root_missing"
	// MigrationBlockedByWorkspace: a workspace occupies <root>/Agents.
	MigrationBlockedByWorkspace = "blocked_by_workspace"
	// MigrationNotWritable: the root cannot be written; retried next start.
	MigrationNotWritable = "not_writable"
	// MigrationBackupFailed: the backup could not be made; nothing was moved.
	MigrationBackupFailed = "backup_failed"
	// MigrationIncomplete: some agents moved and the run stopped; the next
	// start finishes the rest.
	MigrationIncomplete = "incomplete"
)

// RootMigrationMarker is the data-dir file recording a finished migration.
const RootMigrationMarker = "agents_migrated_to_root.json"

// migratingPrefix names the hidden folder an agent is assembled in before it
// is renamed into place, so an interrupted copy never looks like an agent.
const migratingPrefix = ".migrating-"

// RootMigrationReport is what one migration run did. The marker file holds the
// same fields for a finished run.
type RootMigrationReport struct {
	Status     string    `json:"status"`
	Root       string    `json:"root"`
	MigratedAt time.Time `json:"migrated_at"`
	Migrated   []string  `json:"migrated"`
	Skipped    []string  `json:"skipped"`
	Backup     string    `json:"backup,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// KeepsLegacyLocation reports whether the user's agents stay in the data dir
// for this session, so new agents must be created there too.
func (r *RootMigrationReport) KeepsLegacyLocation() bool {
	switch r.Status {
	case MigrationBlockedByWorkspace, MigrationNotWritable, MigrationBackupFailed:
		return true
	}
	return false
}

// RootMigration moves the user's agents from <data dir>/agents into
// <root>/Agents once. The caller decides whether it may run at all (no
// AGENT_STORE_PATH, a root this data dir has confirmed, no pending reset).
//
// It is safe to run again and safe to interrupt: each agent is assembled in a
// hidden folder and renamed into place, and a target that already holds
// exactly what the run would write is recognized as its own earlier work.
type RootMigration struct {
	DataDir   string
	Root      string
	StateRoot string // defaults to <DataDir>/agent_state
	Now       func() time.Time

	// afterAgent runs after each agent is moved. Tests use it to interrupt
	// a run part-way.
	afterAgent func(name string) error
}

// Run performs the migration and reports what happened. It never returns an
// error: every failure is a status the caller can show and retry.
func (m RootMigration) Run() *RootMigrationReport {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	stateRoot := m.StateRoot
	if stateRoot == "" {
		stateRoot = filepath.Join(m.DataDir, config.AgentStateDirName)
	}
	report := &RootMigrationReport{Root: m.Root, Migrated: []string{}, Skipped: []string{}}
	markerPath := filepath.Join(m.DataDir, RootMigrationMarker)

	if data, err := os.ReadFile(markerPath); err == nil { // #nosec G304 -- fixed name in the data dir
		var previous RootMigrationReport
		if json.Unmarshal(data, &previous) == nil {
			previous.Status = MigrationDone
			return &previous
		}
		report.Status = MigrationDone
		return report
	}
	if inspectAgentRoot(m.Root) == RootReasonMissing {
		report.Status = MigrationRootMissing
		return report
	}

	legacy := &fileStore{path: filepath.Join(m.DataDir, "agents.json"), agents: make(map[string]*agent.Agent)}
	if err := legacy.load(); err != nil && !os.IsNotExist(err) {
		report.Status = MigrationIncomplete
		report.Error = err.Error()
		return report
	}
	names := userAgentNames(legacy.agents)
	if len(names) == 0 {
		report.Status = MigrationNotNeeded
		return report
	}
	if inspectAgentRoot(m.Root) == RootReasonOccupiedByWorkspace {
		report.Status = MigrationBlockedByWorkspace
		return report
	}
	if err := probeWritable(m.Root); err != nil {
		report.Status = MigrationNotWritable
		report.Error = err.Error()
		return report
	}

	started := now().UTC()
	backup, err := backupLegacyAgents(m.DataDir, started)
	if err != nil {
		report.Status = MigrationBackupFailed
		report.Error = err.Error()
		return report
	}
	report.Backup = backup

	agentsDir := config.RootAgentsDir(m.Root)
	if err := os.MkdirAll(agentsDir, 0o750); err != nil {
		report.Status = MigrationNotWritable
		report.Error = err.Error()
		return report
	}
	removeInterruptedMoves(agentsDir)

	rootState := NewRuntimeStateStore(stateRoot, m.Root)
	legacyState := legacy.stateStore()
	for _, name := range names {
		moved, err := moveAgentToRoot(legacy, name, agentsDir, rootState, legacyState)
		if err != nil {
			report.Status = MigrationIncomplete
			report.Error = fmt.Sprintf("%s: %v", name, err)
			return report
		}
		if moved {
			report.Migrated = append(report.Migrated, name)
		} else {
			report.Skipped = append(report.Skipped, name)
		}
		if m.afterAgent != nil {
			if err := m.afterAgent(name); err != nil {
				report.Status = MigrationIncomplete
				report.Error = err.Error()
				return report
			}
		}
	}

	report.Status = MigrationDone
	report.MigratedAt = started
	data, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		err = writeFileAtomic(markerPath, append(data, '\n'))
	}
	if err != nil {
		// Every agent moved; only the record is missing. The next start
		// finds no user agents left to move and reports not_needed.
		logger.Warn("Agents moved to the workspace root, but the migration record could not be written", logger.Fields{
			"error": err.Error(),
		})
	}
	return report
}

// userAgentNames lists the agents that belong in the root: everything except
// the built-in assistant, which stays in the data dir.
func userAgentNames(agents map[string]*agent.Agent) []string {
	anyMarked := false
	for _, ag := range agents {
		if isMarkedSystemAgent(ag) {
			anyMarked = true
			break
		}
	}
	names := make([]string, 0, len(agents))
	for name, ag := range agents {
		if isMarkedSystemAgent(ag) || systemassistant.IsCanonicalName(name) {
			continue
		}
		// A retired assistant name is the assistant itself until a marked
		// record exists; after that it is free for the user's own agent.
		if systemassistant.IsLegacyName(name) && !anyMarked {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isMarkedSystemAgent(ag *agent.Agent) bool {
	return ag != nil && ag.Metadata != nil && systemassistant.HasProtectedMarker(ag.Metadata.Tags)
}

// probeWritable checks that the root accepts a new file, without leaving one.
func probeWritable(root string) error {
	probe, err := os.CreateTemp(root, ".ori-write-check-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	closeErr := probe.Close()
	return errors.Join(closeErr, os.Remove(name))
}

// backupLegacyAgents copies <data dir>/agents and agents.json under
// <data dir>/recovery/agents-to-root-<UTC time>/ and returns that folder.
func backupLegacyAgents(dataDir string, at time.Time) (string, error) {
	backup := filepath.Join(dataDir, "recovery", "agents-to-root-"+at.Format("20060102T150405Z"))
	if err := os.MkdirAll(backup, 0o750); err != nil {
		return "", err
	}
	if err := copyTree(filepath.Join(dataDir, "agents"), filepath.Join(backup, "agents"), nil); err != nil {
		_ = os.RemoveAll(backup)
		return "", err
	}
	index := filepath.Join(dataDir, "agents.json")
	if data, err := os.ReadFile(index); err == nil { // #nosec G304 -- fixed name in the data dir
		if err := os.WriteFile(filepath.Join(backup, "agents.json"), data, 0o600); err != nil { // #nosec G703 -- a fixed name inside the backup folder this function created

			_ = os.RemoveAll(backup)
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.RemoveAll(backup)
		return "", err
	}
	return backup, nil
}

// removeInterruptedMoves deletes half-assembled agents an interrupted run left.
func removeInterruptedMoves(agentsDir string) {
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), migratingPrefix) {
			_ = os.RemoveAll(filepath.Join(agentsDir, entry.Name()))
		}
	}
}

// moveAgentToRoot moves one agent and reports false when it was skipped
// because the root already has a different agent by that name.
func moveAgentToRoot(legacy *fileStore, name, agentsDir string, rootState, legacyState *RuntimeStateStore) (bool, error) {
	ag := legacy.agents[name]
	// A legacy status "disabled" becomes "paused": true here, because the
	// definition is written from the decoded agent.
	definition, err := encodeDefinition(ag)
	if err != nil {
		return false, err
	}
	target := filepath.Join(agentsDir, name)
	legacyFolder := filepath.Join(legacy.agentsDir(), name)

	if _, err := os.Stat(target); err == nil {
		existing, readErr := os.ReadFile(filepath.Join(target, definitionFileName)) // #nosec G304 -- inside the root's agents folder
		if readErr != nil || !bytes.Equal(existing, definition) {
			// Someone else's agent by this name: never overwrite it. The
			// legacy folder stays where it is and still loads from there.
			return false, nil
		}
		// Exactly what this migration writes: an earlier run was interrupted
		// after the rename. Finish it below.
	} else if errors.Is(err, os.ErrNotExist) {
		staging := filepath.Join(agentsDir, migratingPrefix+name)
		_ = os.RemoveAll(staging)
		skipDefinition := func(rel string) bool { return rel == definitionFileName }
		if err := copyTree(legacyFolder, staging, skipDefinition); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.RemoveAll(staging)
			return false, err
		}
		if err := os.MkdirAll(staging, 0o750); err != nil {
			return false, err
		}
		if err := writeFileAtomic(filepath.Join(staging, definitionFileName), definition); err != nil {
			_ = os.RemoveAll(staging)
			return false, err
		}
		if err := os.Rename(staging, target); err != nil {
			_ = os.RemoveAll(staging)
			return false, err
		}
	} else {
		return false, err
	}

	if err := rootState.Save(name, runtimeStateOf(ag)); err != nil {
		return false, err
	}
	if err := os.RemoveAll(legacyFolder); err != nil {
		return false, err
	}
	flat := filepath.Join(legacy.agentsDir(), name+".json")
	if err := os.Remove(flat); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := legacyState.Delete(name); err != nil {
		logger.Warn("Migrated agent's old runtime state could not be removed", logger.Fields{"agent": name, "error": err.Error()})
	}
	return true, nil
}

// copyTree copies the regular files and folders under src into dst. Both are
// opened as os.Root, so a symbolic link can never lead a read or a write
// outside them; links are not copied at all. skip, when set, names paths
// (relative to src, slash-separated) to leave out.
func copyTree(src, dst string, skip func(rel string) bool) error {
	from, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer func() { _ = from.Close() }()
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	to, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer func() { _ = to.Close() }()

	return fs.WalkDir(from.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if rel == "." {
			return nil
		}
		if skip != nil && skip(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case d.IsDir():
			return to.MkdirAll(rel, 0o750)
		case d.Type().IsRegular():
			data, err := from.ReadFile(rel)
			if err != nil {
				return err
			}
			return to.WriteFile(rel, data, 0o600)
		default:
			return nil // symlinks and devices are not agent data
		}
	})
}
