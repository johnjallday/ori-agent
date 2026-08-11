package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/types"
)

// This file is the single normalization contract for agent appearance on every
// read and write path.
//
// There are two persistence seams — the global agents/{name}/agent_settings.json
// records and the workspace-local agents/{slug}/config.json snapshots — and they
// deserialize the same agent.Agent type. Routing both through one function is
// what stops a stale snapshot from reintroducing the retired schema after the
// global store has already migrated (PRD FR-69, risk 7.6 "snapshot
// reintroduction").

// AppearanceUploadDir is the directory holding uploaded avatar images, relative
// to the process working directory. The internal directory name is deliberately
// unchanged: renaming user files to match new product vocabulary would be churn
// with a data-loss risk and no user benefit (FR-65).
const AppearanceUploadDir = "agent_avatars"

// EnsureAppearance guarantees the agent has a non-nil, structurally canonical
// Appearance. It is idempotent and does not need the catalog or the filesystem,
// so it is safe to call from anywhere, including hot paths (FR-4).
func (a *Agent) EnsureAppearance() {
	if a == nil {
		return
	}
	if a.Appearance == nil {
		a.Appearance = types.NewAgentAppearance()
		return
	}
	a.Appearance.Normalize()
}

// MigrateAppearance brings one loaded agent up to the canonical model, draining
// any legacy avatar/character fields captured while decoding.
//
// It reports whether the record changed, so the caller can decide whether a
// rewrite is warranted, and the stable reason codes for anything it had to
// downgrade or discard. Reasons are safe to log verbatim (FR-73).
//
// Calling it twice produces the same result and reports Changed=false the second
// time: the legacy side-channel is drained on the first pass and the canonical
// path only normalizes (FR-76).
func (a *Agent) MigrateAppearance(env types.AppearanceEnvironment) types.AppearanceMigration {
	if a == nil {
		return types.AppearanceMigration{Appearance: types.NewAgentAppearance()}
	}
	var legacy *types.LegacyAppearance
	if a.Metadata != nil {
		legacy = a.Metadata.TakeLegacyAppearance()
	}
	result := types.MigrateAppearance(a.Appearance, legacy, env)
	a.Appearance = result.Appearance
	return result
}

// DefaultAppearanceEnvironment returns the environment migration uses in the
// running application: the real embedded catalog and the real upload directory.
//
// A catalog that fails to load yields a nil CharacterVersion callback, which
// migration reads as "cannot tell" and therefore trusts the record — see
// types.AppearanceEnvironment. That is deliberate: a transient catalog problem
// must not permanently rewrite every agent's saved character choice.
func DefaultAppearanceEnvironment(uploadDir string) types.AppearanceEnvironment {
	env := types.AppearanceEnvironment{
		UploadExists: uploadExistsIn(uploadDir),
	}
	if cat, err := charactercatalog.Load(); err == nil && cat != nil {
		env.CharacterVersion = func(catalogID string) (int, bool) {
			id := charactercatalog.CharacterID(strings.TrimSpace(catalogID))
			if !cat.IsAssignable(id) {
				return 0, false
			}
			entry, ok := cat.Get(id)
			if !ok {
				return 0, false
			}
			return entry.EntryVersion, true
		}
	}
	return env
}

// AppearanceMigrationNote records one record that migration had to downgrade,
// discard something from, or could not rewrite.
//
// Notes exist because a silent migration is indistinguishable from data loss:
// a user whose character quietly reverted to Generated has no way to tell
// whether the catalog withdrew the entry or the upgrade ate it. Every field here
// is safe to display — an agent name the user already knows, a scope label, and
// fixed reason codes. No filesystem path ever appears (FR-73/FR-75).
type AppearanceMigrationNote struct {
	Agent   string   `json:"agent"`
	Scope   string   `json:"scope"`
	Reasons []string `json:"reasons"`
	Error   string   `json:"error,omitempty"`
}

var (
	appearanceNotesMu sync.Mutex
	appearanceNotes   = map[string]AppearanceMigrationNote{}
)

// RecordAppearanceMigrationNote registers a note for later health/startup
// reporting. Notes are keyed by scope+agent, so re-reading the same record
// replaces its note rather than accumulating duplicates on every reload.
func RecordAppearanceMigrationNote(note AppearanceMigrationNote) {
	if len(note.Reasons) == 0 && note.Error == "" {
		return
	}
	appearanceNotesMu.Lock()
	defer appearanceNotesMu.Unlock()
	appearanceNotes[note.Scope+"\x00"+note.Agent] = note
}

// AppearanceMigrationNotes returns the recorded notes in a stable order.
func AppearanceMigrationNotes() []AppearanceMigrationNote {
	appearanceNotesMu.Lock()
	defer appearanceNotesMu.Unlock()
	out := make([]AppearanceMigrationNote, 0, len(appearanceNotes))
	for _, note := range appearanceNotes {
		out = append(out, note)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// ResetAppearanceMigrationNotes clears the registry. Used by tests, and by a
// caller that is deliberately re-running a full load.
func ResetAppearanceMigrationNotes() {
	appearanceNotesMu.Lock()
	defer appearanceNotesMu.Unlock()
	appearanceNotes = map[string]AppearanceMigrationNote{}
}

// uploadExistsIn builds an existence check confined to dir.
//
// A stored filename that is not a plain filename is reported missing rather than
// resolved: the upload endpoint only ever writes server-generated basenames, so
// anything with a separator in it came from somewhere it should not have, and
// following it would be a traversal (FR-64).
func uploadExistsIn(dir string) func(string) bool {
	return func(filename string) bool {
		name := strings.TrimSpace(filename)
		if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
			return false
		}
		info, err := os.Stat(filepath.Join(dir, name))
		return err == nil && !info.IsDir()
	}
}
