package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var (
	ErrProfileSkillStateUnavailable = errors.New("profile skill state is unavailable")
	ErrProfileSkillsChanged         = errors.New("the reviewed profile skills changed; review the team again")
)

// ProfileSkillSnapshot contains collection membership only. It does not read
// SKILL.md, prompts, tool settings, workspace grants or private configuration.
// Enabled skills are not necessarily available or active: those separate
// constraints remain the caller's responsibility.
type ProfileSkillSnapshot struct {
	States  map[string]bool
	Witness string `json:"-"`
}

// Enabled applies the registry's explicit/wildcard decision for a file-based
// skill. Built-in CLI skills have separate implicit defaults; this inspection
// does not infer those, source availability, or active Toolbox membership.
func (s ProfileSkillSnapshot) Enabled(name string) bool {
	if value, present := s.States[normalizeSkillKey(name)]; present {
		return value
	}
	return s.States["*"]
}

// ProfileSkillInspection captures a directory before a canonical profile source
// fence is taken. Neither method resolves a profile or calls its store. Callers
// must verify that this path still belongs to the exact selected source; this
// object is not standalone source or grant authority.
type ProfileSkillInspection struct {
	manager *Manager
	path    string
}

func (m *Manager) PrepareProfileSkillInspection(name string) (*ProfileSkillInspection, error) {
	if m == nil {
		return nil, ErrProfileSkillStateUnavailable
	}
	path, err := m.profileRegistryPath(name)
	if err != nil {
		return nil, ErrProfileSkillStateUnavailable
	}
	return &ProfileSkillInspection{manager: m, path: path}, nil
}

func (i *ProfileSkillInspection) Read() (ProfileSkillSnapshot, error) {
	if i == nil || i.manager == nil {
		return ProfileSkillSnapshot{}, ErrProfileSkillStateUnavailable
	}
	i.manager.registryMu.RLock()
	defer i.manager.registryMu.RUnlock()
	return inspectProfileSkillState(i.path)
}

// WithUnchanged serializes a short canonical bind against Ori's skill toggles.
// The callback must not call the skills manager (including prepared grant writes)
// or re-enter the profile store. Never hold this guard across inference or tools.
// External filesystem writes are detected at checking time, not OS-wide locked.
func (i *ProfileSkillInspection) WithUnchanged(witness string, apply func() error) error {
	if i == nil || i.manager == nil || witness == "" || apply == nil {
		return ErrProfileSkillsChanged
	}
	i.manager.registryMu.RLock()
	defer i.manager.registryMu.RUnlock()
	snapshot, err := inspectProfileSkillState(i.path)
	if err != nil || snapshot.Witness != witness {
		return ErrProfileSkillsChanged
	}
	return apply()
}

func inspectProfileSkillState(path string) (ProfileSkillSnapshot, error) {
	const maxBytes = 256 * 1024
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return ProfileSkillSnapshot{}, ErrProfileSkillStateUnavailable
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		digest := sha256.Sum256([]byte("profile-skills-v1:absent"))
		return ProfileSkillSnapshot{States: map[string]bool{}, Witness: hex.EncodeToString(digest[:])}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return ProfileSkillSnapshot{}, ErrProfileSkillStateUnavailable
	}
	file, err := os.Open(path) // #nosec G304 -- captured canonical profile directory; caller verifies its source before using the witness.
	if err != nil {
		return ProfileSkillSnapshot{}, ErrProfileSkillStateUnavailable
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return ProfileSkillSnapshot{}, ErrProfileSkillStateUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return ProfileSkillSnapshot{}, ErrProfileSkillStateUnavailable
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) {
		return ProfileSkillSnapshot{}, ErrProfileSkillStateUnavailable
	}
	var registry SkillRegistry
	if json.Unmarshal(data, &registry) != nil || len(registry.Skills) > 1024 {
		return ProfileSkillSnapshot{}, ErrProfileSkillStateUnavailable
	}
	states := make(map[string]bool, len(registry.Skills))
	for name, state := range registry.Skills {
		key := normalizeSkillKey(name)
		if _, duplicate := states[key]; key == "" || len(key) > 120 || duplicate {
			return ProfileSkillSnapshot{}, ErrProfileSkillStateUnavailable
		}
		states[key] = state.Enabled
	}
	digest := sha256.Sum256(append([]byte("profile-skills-v1:"), data...))
	return ProfileSkillSnapshot{States: states, Witness: hex.EncodeToString(digest[:])}, nil
}
