package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

// StaffingProfile is an allowlisted inspection projection. Witness is opaque,
// process-local review material, NOT an agent ID or permission to bind. Never
// send it to a model. No private settings, paths or runtime state leave here.
type StaffingProfile struct {
	Name         string
	Purpose      string
	Role         string
	Capabilities []string
	Provider     string
	Model        string
	Paused       bool
	Source       string // root or legacy; never a workspace copy
	Witness      string `json:"-"`
}

// StaffingSnapshot reports coverage separately from matches. Incomplete reads
// must never be presented as an exhaustive search with no suitable agent.
type StaffingSnapshot struct {
	Profiles []StaffingProfile
	Complete bool
	// Includes unavailable/private records so a healthy namesake in another
	// member cannot turn an ambiguous source into reusable authority.
	sourceNames []string
}

// StaffingSnapshotReader is intentionally read-only. Unknown stores do not get
// reusable-profile authority through a ListAgents/GetAgent fallback.
type StaffingSnapshotReader interface {
	ReadStaffingSnapshot(context.Context) StaffingSnapshot
}

func ReadStaffingSnapshot(ctx context.Context, st Store) StaffingSnapshot {
	if reader, ok := st.(StaffingSnapshotReader); ok {
		return reader.ReadStaffingSnapshot(ctx)
	}
	return StaffingSnapshot{}
}

type staffingSourceWitness struct {
	file   os.FileInfo
	folder os.FileInfo
	digest [sha256.Size]byte
	token  string
}

// The snapshot never opens workspace copies, including cached/trashed ones.
// Holding c.mu prevents root switches while members are being inspected.
func (c *CompositeStore) ReadStaffingSnapshot(ctx context.Context) StaffingSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := ReadStaffingSnapshot(ctx, c.system)
	allNames := append([]string(nil), result.sourceNames...)
	if c.root == nil || inspectAgentRoot(c.rootPath) != "" {
		result.Complete = false
	} else {
		root := c.root.ReadStaffingSnapshot(ctx)
		result.Complete = result.Complete && root.Complete
		for i := range root.Profiles {
			root.Profiles[i].Source = "root"
		}
		result.Profiles = append(result.Profiles, root.Profiles...)
		allNames = append(allNames, root.sourceNames...)
	}
	// Same-name legacy/root records are ambiguous to a reviewed reusable
	// selection, even though the ordinary roster deterministically picks one.
	counts := make(map[string]int, len(result.Profiles))
	for _, name := range allNames {
		counts[strings.ToLower(name)]++
	}
	kept := make([]StaffingProfile, 0, len(result.Profiles))
	for _, profile := range result.Profiles {
		if counts[strings.ToLower(profile.Name)] != 1 {
			result.Complete = false
			continue
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", c.staffingRootRevision, profile.Witness)))
		profile.Witness = hex.EncodeToString(digest[:])
		kept = append(kept, profile)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Name < kept[j].Name })
	result.Profiles = kept
	return result
}

// ReadStaffingSnapshot uses canonical loaded definitions under their mutation
// lock. Disk checks prove freshness only; they never load or repair a profile.
// Restart intentionally expires witnesses: uncommitted choices need review.
func (s *fileStore) ReadStaffingSnapshot(ctx context.Context) StaffingSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := StaffingSnapshot{Complete: len(s.unreadable) == 0}
	for name := range s.agents {
		result.sourceNames = append(result.sourceNames, name)
	}
	for name := range s.unreadable {
		if _, loaded := s.agents[name]; !loaded {
			result.sourceNames = append(result.sourceNames, name)
		}
	}
	entries, err := os.ReadDir(s.agentsDir())
	if err != nil && !(os.IsNotExist(err) && len(s.agents) == 0) {
		return StaffingSnapshot{sourceNames: result.sourceNames}
	}
	// Detect newly added definitions omitted by the loaded store, without
	// loading their contents. An explicit normal rescan resolves this state.
	for _, entry := range entries {
		if ctx.Err() != nil {
			return StaffingSnapshot{}
		}
		if _, loaded := s.agents[entry.Name()]; loaded {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			result.Complete = false
			if _, unreadable := s.unreadable[entry.Name()]; !unreadable {
				result.sourceNames = append(result.sourceNames, entry.Name())
			}
			continue
		}
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Lstat(s.definitionPath(entry.Name())); !os.IsNotExist(err) {
			result.Complete = false
			if _, unreadable := s.unreadable[entry.Name()]; !unreadable {
				result.sourceNames = append(result.sourceNames, entry.Name())
			}
		}
	}
	names := make([]string, 0, len(s.agents))
	counts := make(map[string]int, len(s.agents))
	for name := range s.agents {
		names = append(names, name)
		counts[strings.ToLower(name)]++
	}
	sort.Strings(names)
	for _, name := range names {
		if ctx.Err() != nil {
			return StaffingSnapshot{}
		}
		ag := s.agents[name]
		if ag == nil || ag.WorkspaceLocalConfigID != "" || systemassistant.IsKnownName(name) || isMarkedSystemAgent(ag) {
			continue
		}
		if counts[strings.ToLower(name)] != 1 {
			result.Complete = false
			continue
		}
		witness, ok := s.staffingWitnessLocked(name)
		if !ok {
			result.Complete = false
			continue
		}
		profile := StaffingProfile{
			Name: name, Role: string(ag.Role), Capabilities: append([]string(nil), ag.Capabilities...),
			Provider: ag.Settings.Provider, Model: ag.Settings.Model,
			Paused: ag.Status == types.AgentStatusDisabled, Source: "legacy", Witness: witness,
		}
		if ag.Metadata != nil {
			profile.Purpose = ag.Metadata.Description
		}
		result.Profiles = append(result.Profiles, profile)
	}
	return result
}

func (s *fileStore) staffingWitnessLocked(name string) (string, bool) {
	path := s.definitionPath(name)
	folder, err := os.Lstat(filepath.Dir(path))
	if err != nil || !folder.IsDir() || folder.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return "", false
	}
	f, err := os.Open(path) // #nosec G304 -- path comes from this store's canonical loaded profile names, never a request.
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	known, haveKnown := s.definitionHashes[name]
	if err != nil || len(data) > 1024*1024 || !haveKnown || sha256.Sum256(data) != known {
		return "", false
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) {
		return "", false
	}
	// Fingerprint private configuration internally, without serializing an
	// Agent (which contains chat/runtime fields), logging it, or persisting it.
	ag := s.agents[name]
	config, err := json.Marshal(struct {
		Definition persistedDefinition
		Toolbox    *types.AgentDefaultToolbox
	}{persistedDefinition{Role: ag.Role, Capabilities: ag.Capabilities, Settings: ag.Settings,
		Metadata: ag.Metadata, Appearance: ag.Appearance, AssistantSetup: ag.AssistantSetup,
		Paused: ag.Status == types.AgentStatusDisabled}, ag.DefaultToolbox})
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(append(known[:], config...))
	old, found := s.staffingWitnesses[name]
	if !found || old.digest != digest || !os.SameFile(old.file, info) || !os.SameFile(old.folder, folder) || !old.file.ModTime().Equal(info.ModTime()) {
		if s.staffingWitnesses == nil {
			s.staffingWitnesses = make(map[string]staffingSourceWitness)
		}
		old = staffingSourceWitness{file: info, folder: folder, digest: digest, token: uuid.NewString()}
		s.staffingWitnesses[name] = old
	}
	return old.token, true
}
