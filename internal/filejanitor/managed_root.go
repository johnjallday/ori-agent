package filejanitor

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var ErrManagedRootUnavailable = errors.New("managed folder metadata is unavailable")

var managedRootGeneration = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// ManagedRoot is a server-only identity snapshot. No caller may serialize its
// path. Reading it uses Ori's saved settings/workspace metadata, never Status,
// readiness, directory enumeration, stat, symlink resolution or provider IO.
// Comparing the complete value after an asynchronous read fences late facts.
type ManagedRoot struct {
	WorkspaceID          string `json:"-"`
	Owner                string `json:"-"`
	Generation           string `json:"-"`
	DirectoryReferenceID string `json:"-"`
	Path                 string `json:"-"`
	BindingID            string `json:"-"`
}

func (s *Service) StoredManagedRoot(owner, workspaceID string) (ManagedRoot, error) {
	unavailable := func() (ManagedRoot, error) { return ManagedRoot{}, ErrManagedRootUnavailable }
	if s == nil || s.store == nil || strings.TrimSpace(owner) == "" {
		return unavailable()
	}
	if s.workspaces == nil {
		return unavailable()
	}
	var ws *workspace.Workspace
	var err error
	if folders, ok := s.workspaces.(FolderWorkspaceSource); ok {
		// Unlike the generic operational fallback, facts cannot resurrect a
		// stale database grant when the authoritative folder record is unreadable.
		ws, err = folders.GetFolderWorkspace(workspaceID)
	} else {
		ws, err = s.workspaces.Get(workspaceID)
	}
	if err != nil || ws == nil || ws.ID != workspaceID || !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		return unavailable()
	}
	actualOwner := strings.TrimSpace(ws.OwnerUserID)
	if actualOwner == "" {
		actualOwner = userprofile.LocalUserID
	}
	if !strings.EqualFold(actualOwner, owner) {
		return unavailable()
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil || settings.WorkspaceID != workspaceID || !settings.IsSetUp() || !managedRootGeneration.MatchString(settings.RootID) || settings.RootConflictWorkspaceID != "" || !filepath.IsAbs(settings.RootPath) {
		return unavailable()
	}
	refs := 0
	for _, ref := range ws.DirectoryReferences {
		if ref.ID != settings.DirectoryReferenceID {
			continue
		}
		refs++
		if ref.WorkspaceID != workspaceID || ref.Purpose != "" || ref.Path != settings.RootPath {
			return unavailable()
		}
	}
	if refs != 1 {
		return unavailable()
	}
	// Revoked/disabled/replaced grants cannot leave apparently current facts.
	// Inspect the saved grant, not the MCP registry or the folder itself.
	bindings, bindingID := 0, ""
	for _, binding := range ws.MCPBindings {
		if !strings.EqualFold(strings.TrimSpace(binding.Alias), LegacyBindingAlias) {
			continue
		}
		bindings++
		var roots []string
		encoded, err := json.Marshal(binding.Config["roots"])
		if err != nil || json.Unmarshal(encoded, &roots) != nil || len(roots) != 1 || roots[0] != settings.RootPath || !binding.Enabled || binding.ServerName != filesystemServerName || !bindingToolsAreReadOnly(binding) {
			return unavailable()
		}
		for _, tool := range JanitorReadTools {
			if !slices.Contains(binding.AllowedTools, tool) {
				return unavailable()
			}
		}
		bindingID = binding.ID
	}
	if bindings != 1 || bindingID == "" {
		return unavailable()
	}
	return ManagedRoot{WorkspaceID: workspaceID, Owner: actualOwner, Generation: settings.RootID, DirectoryReferenceID: settings.DirectoryReferenceID, Path: settings.RootPath, BindingID: bindingID}, nil
}
