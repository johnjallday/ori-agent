package skillshttp

// One-time import of the skills a user already has in ~/.agents/skills (and
// ~/.config/agents/skills, where the skills tool puts -g installs) into the
// Workspace Directory's Skills folder. Import copies; the originals are never
// changed, moved, or deleted. The panel offering it shows once per machine and
// Workspace Directory, and that choice lives in the data dir.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
)

// skillsImportStateFileName records, per Workspace Directory, that the import
// panel was answered on this machine.
const skillsImportStateFileName = "skills_import_state.json"

// importStateMu serializes reads and writes of the import state file.
var importStateMu sync.Mutex

// importCandidate is one skill folder the panel can offer.
type importCandidate struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	SourceFolder   string `json:"source_folder"`
	AlreadyPresent bool   `json:"already_present"`
	path           string
}

type importFailure struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// importSourceFolders are the folders the import panel reads from:
// ~/.agents/skills, and $XDG_CONFIG_HOME/agents/skills (~/.config/agents/skills
// when XDG_CONFIG_HOME is not set).
func importSourceFolders() []string {
	home, homeErr := os.UserHomeDir()
	var folders []string
	if homeErr == nil {
		folders = append(folders, filepath.Join(home, ".agents", "skills"))
	}
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" && homeErr == nil {
		configHome = filepath.Join(home, ".config")
	}
	if configHome != "" {
		folders = append(folders, filepath.Join(configHome, "agents", "skills"))
	}
	return folders
}

// importLockFile is the skills tool's own record of where it installed each
// skill from.
func importLockFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agents", ".skill-lock.json")
}

// isImportableName accepts a single visible folder name.
func isImportableName(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && filepath.Base(name) == name && filepath.IsLocal(name) &&
		!strings.ContainsAny(name, `/\:`)
}

// importCandidates lists the importable skill folders: visible, holding a
// SKILL.md, and not a copy a plugin made (those come from the plugin itself
// now). A name in both folders is offered once, from ~/.agents/skills.
func (h *Handler) importCandidates() ([]importCandidate, error) {
	skillsDir, err := h.skillsFolder()
	if err != nil {
		return nil, err
	}
	pluginProvided := map[string]bool{}
	if h.manager != nil {
		for _, name := range h.manager.PluginSkillNames() {
			pluginProvided[strings.ToLower(name)] = true
		}
	}
	seen := map[string]bool{}
	candidates := []importCandidate{}
	for _, folder := range importSourceFolders() {
		entries, err := os.ReadDir(folder)
		if err != nil {
			continue // a folder that does not exist has nothing to offer
		}
		for _, entry := range entries {
			name := entry.Name()
			key := strings.ToLower(name)
			if !isImportableName(name) || seen[key] || pluginProvided[key] {
				continue
			}
			path := filepath.Join(folder, name)
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				continue
			}
			skillFile := filepath.Join(path, "SKILL.md")
			if _, err := os.Stat(skillFile); err != nil {
				continue
			}
			if _, err := os.Lstat(filepath.Join(path, plugin.LegacySkillReceiptFileName)); err == nil {
				continue
			}
			seen[key] = true
			candidate := importCandidate{Name: name, SourceFolder: folder, path: path}
			if summary, err := skills.ReadSkillSummary(skillFile, name); err == nil {
				candidate.Description = summary.Description
			}
			if _, err := os.Lstat(filepath.Join(skillsDir, name)); err == nil {
				candidate.AlreadyPresent = true
			}
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return strings.ToLower(candidates[i].Name) < strings.ToLower(candidates[j].Name) })
	return candidates, nil
}

// importSkills copies each named candidate into the Skills folder. A name
// already there is skipped; one failure does not stop the rest.
func (h *Handler) importSkills(names []string) (imported, skipped []string, failed []importFailure, err error) {
	skillsDir, err := h.skillsFolder()
	if err != nil {
		return nil, nil, nil, err
	}
	candidates, err := h.importCandidates()
	if err != nil {
		return nil, nil, nil, err
	}
	byName := make(map[string]importCandidate, len(candidates))
	for _, candidate := range candidates {
		byName[candidate.Name] = candidate
	}
	lock := readSkillLock(importLockFile())
	imported, skipped, failed = []string{}, []string{}, []importFailure{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		candidate, ok := byName[name]
		switch {
		case !ok:
			failed = append(failed, importFailure{Name: name, Reason: "not found in ~/.agents/skills"})
			continue
		case candidate.AlreadyPresent:
			skipped = append(skipped, name)
			continue
		}
		if err := copyImportedSkill(candidate.path, skillsDir, name, lock[name]); err != nil {
			failed = append(failed, importFailure{Name: name, Reason: err.Error()})
			continue
		}
		imported = append(imported, name)
	}
	return imported, skipped, failed, nil
}

// skillLockEntry is one skill in the skills tool's lock file.
type skillLockEntry struct {
	Source     string `json:"source"`
	SourceType string `json:"sourceType"`
}

// readSkillLock reads the skills tool's lock file. Anything unreadable means
// no entries: those skills are imported without update checks.
func readSkillLock(path string) map[string]skillLockEntry {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the skills tool's fixed lock file under HOME
	if err != nil {
		return nil
	}
	var lock struct {
		Skills map[string]skillLockEntry `json:"skills"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return nil
	}
	return lock.Skills
}

// copyImportedSkill copies one skill folder, following symlinks so the real
// contents are copied, into a hidden staging folder, and renames it into
// place. A github lock entry becomes a source file, so update checks work.
func copyImportedSkill(src, skillsDir, name string, lock skillLockEntry) error {
	if err := os.MkdirAll(skillsDir, 0o750); err != nil {
		return fmt.Errorf("prepare the Skills folder: %w", err)
	}
	destination := filepath.Join(skillsDir, name)
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("already in your Skills folder")
	}
	staged, err := os.MkdirTemp(skillsDir, ".ori-import-")
	if err != nil {
		return fmt.Errorf("prepare the copy: %w", err)
	}
	defer func() { _ = os.RemoveAll(staged) }()
	copied := filepath.Join(staged, name)
	if err := copyFollowingLinks(src, copied, map[string]bool{}); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if lock.SourceType == "github" && strings.TrimSpace(lock.Source) != "" {
		digest, err := skillFolderDigest(copied)
		if err != nil {
			return fmt.Errorf("read the copy: %w", err)
		}
		now := time.Now().UTC()
		source := SkillSource{Package: lock.Source + "@" + name, InstalledAt: now, UpdatedAt: now, TreeDigest: digest}
		if err := writeSkillSource(copied, source); err != nil {
			return fmt.Errorf("record the skill's source: %w", err)
		}
	}
	if err := os.Rename(copied, destination); err != nil {
		return fmt.Errorf("move the copy into place: %w", err)
	}
	return nil
}

// copyFollowingLinks copies src to dst, reading through symbolic links so
// every file arrives as a real file. visited guards against a link loop.
func copyFollowingLinks(src, dst string, visited map[string]bool) error {
	real, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(real)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a plain file", filepath.Base(src))
		}
		return copySkillFile(real, dst, info.Mode().Perm()|0o600)
	}
	if visited[real] {
		return fmt.Errorf("%s links back into itself", filepath.Base(src))
	}
	visited[real] = true
	defer delete(visited, real)
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == plugin.LegacySkillReceiptFileName {
			continue
		}
		if err := copyFollowingLinks(filepath.Join(real, entry.Name()), filepath.Join(dst, entry.Name()), visited); err != nil {
			return err
		}
	}
	return nil
}

// importStatePath is the data-dir file of answered import panels.
func importStatePath() string {
	return filepath.Join(config.DefaultDataDir(), skillsImportStateFileName)
}

// importAnswered reports whether this machine answered the import panel for
// the current Workspace Directory.
func (h *Handler) importAnswered() (bool, error) {
	skillsDir, err := h.skillsFolder()
	if err != nil {
		return false, err
	}
	importStateMu.Lock()
	defer importStateMu.Unlock()
	state, err := readImportState()
	if err != nil {
		return false, err
	}
	_, answered := state[store.RootKey(filepath.Dir(skillsDir))]
	return answered, nil
}

// markImportAnswered records that the panel was answered (imported or
// dismissed) for the current Workspace Directory.
func (h *Handler) markImportAnswered() error {
	skillsDir, err := h.skillsFolder()
	if err != nil {
		return err
	}
	importStateMu.Lock()
	defer importStateMu.Unlock()
	state, err := readImportState()
	if err != nil {
		state = map[string]string{}
	}
	key := store.RootKey(filepath.Dir(skillsDir))
	if _, done := state[key]; done {
		return nil
	}
	state[key] = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(importStatePath()), 0o750); err != nil {
		return err
	}
	temp := importStatePath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, importStatePath())
}

func readImportState() (map[string]string, error) {
	data, err := os.ReadFile(importStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	state := map[string]string{}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state, nil
}

// ImportCandidates answers GET /api/skills/import/candidates.
func (h *Handler) ImportCandidates(w http.ResponseWriter, _ *http.Request) {
	candidates, err := h.importCandidates()
	if err != nil {
		orihttp.InternalError(w, "failed to list skills to import: "+err.Error())
		return
	}
	answered, err := h.importAnswered()
	if err != nil {
		answered = true // an unreadable state file never nags
	}
	importable := false
	for _, candidate := range candidates {
		if !candidate.AlreadyPresent {
			importable = true
			break
		}
	}
	orihttp.Success(w, map[string]any{
		"candidates":  candidates,
		"should_show": importable && !answered,
	})
}

// Import answers POST /api/skills/import with {names: [...]}.
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	imported, skipped, failed, err := h.importSkills(req.Names)
	if err != nil {
		orihttp.InternalError(w, "failed to import skills: "+err.Error())
		return
	}
	_ = h.markImportAnswered()
	orihttp.Success(w, map[string]any{
		"imported": imported,
		"skipped":  skipped,
		"failed":   failed,
	})
}

// DismissImport answers POST /api/skills/import/dismiss ("Not now").
func (h *Handler) DismissImport(w http.ResponseWriter, _ *http.Request) {
	if err := h.markImportAnswered(); err != nil {
		orihttp.InternalError(w, "failed to save the choice: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"dismissed": true})
}
