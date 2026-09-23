package plugin

// Offline plugin reset. This file is the single owner of "what does an installed
// plugin own, and how is it removed exactly" for the staged Settings reset.
//
// Everything here is deliberately file-level and manager-free. It is used both
// by the live preview and by the pre-store recovery boundary, where no Manager,
// MCP registry, surface registry, service manager, or skills manager exists yet.
// It therefore never resolves a source, clones, downloads, executes a plugin
// command, starts a service, or runs a plugin-authored uninstall hook. The valid
// installed record is the only ownership authority, exactly as it is for the
// explicit uninstall path (Manager.Uninstall).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

// MaxResetItems bounds one reviewed plugin inventory. Reset metadata is capped at
// 64 KiB, so an installation with more plugins than this blocks with a precise
// reason instead of silently reviewing a truncated scope.
const MaxResetItems = 64

const (
	maxResetNameBytes      = 128
	maxResetComponentItems = 64
	maxResetPathBytes      = 4096
)

// Reset problem codes. They are stable identifiers rendered as blockers, not
// free-form text, and they never carry a raw path, command, or error string.
const (
	ResetProblemRegistryUnreadable = "plugin_registry_unreadable"
	ResetProblemInventoryTooLarge  = "plugin_inventory_too_large"
	ResetProblemNameUnsafe         = "plugin_name_unsafe"
	ResetProblemDuplicateRecord    = "plugin_record_duplicated"
	ResetProblemComponentUnsafe    = "plugin_component_unsafe"
	ResetProblemRootUnresolved     = "plugin_install_root_unresolved"
	ResetProblemOwnerUnavailable   = "plugin_reset_owner_unavailable"
)

// ResetProblem blocks a reviewed plugin reset. Detail is a short bounded phrase
// written by this package; it never contains untrusted plugin text or a path.
type ResetProblem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// ResetPaths are the owner-resolved roots the reset operates within. Every field
// is supplied by the host (the builder in production, the independent startup
// resolver during recovery) and must be absolute and cleaned. None is ever
// derived from a browser request or from a plugin manifest.
//
// Every root is inside the Ori installation. A plugin's skills live in its own
// install folder, so no skills folder is ever a reset root: a managed clone
// takes its skills with it and a linked source keeps them.
type ResetPaths struct {
	PluginsDir  string
	CloneDir    string
	MCPRegistry string
}

// DefaultResetPaths derives the canonical layout from the installation data
// directory, matching what the live plugin handler and MCP config manager use.
func DefaultResetPaths(dataDir string) ResetPaths {
	pluginsDir := filepath.Join(dataDir, "plugins")
	return ResetPaths{
		PluginsDir:  pluginsDir,
		CloneDir:    filepath.Join(pluginsDir, "src"),
		MCPRegistry: filepath.Join(dataDir, "mcp_registry.json"),
	}
}

func (p ResetPaths) RegistryPath() string  { return filepath.Join(p.PluginsDir, "installed.json") }
func (p ResetPaths) StateRoot() string     { return filepath.Join(p.PluginsDir, "state") }
func (p ResetPaths) ArtifactsRoot() string { return filepath.Join(p.PluginsDir, "artifacts") }
func (p ResetPaths) PreviewRoot() string   { return filepath.Join(p.PluginsDir, "preview") }

// MarketplacesPath is never a reset target. Selected plugin reset preserves it
// and verifies that it is unchanged; only Start Fresh removes it under its own
// broader integration policy.
func (p ResetPaths) MarketplacesPath() string {
	return filepath.Join(p.PluginsDir, "marketplaces.json")
}

// Resolved reports whether these roots are usable at all.
func (p ResetPaths) Resolved() bool { return p.validate() == nil }

// ResetStateNamespace reports the managed directory name that holds a plugin's
// host-owned namespaced Workspace Surface state. Tests and fixtures use it so
// the layout is stated in exactly one place.
func ResetStateNamespace(pluginID string) string {
	digest := sha256.Sum256([]byte(pluginID))
	return hex.EncodeToString(digest[:])
}

// ValidResetName reports whether a recorded plugin or skill name is a plain,
// visible, single path component. Callers outside this package use it to
// validate persisted reset evidence before it can address a location.
func ValidResetName(name string) bool { return validResetSegment(name) }

// ValidResetServerName reports whether a recorded MCP registration is namespaced
// to the plugin that claims it, which is what registration always produces.
func ValidResetServerName(pluginName, server string) bool {
	return validResetServerName(pluginName, server)
}

func (p ResetPaths) validate() error {
	for _, path := range []string{p.PluginsDir, p.CloneDir, p.MCPRegistry} {
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > maxResetPathBytes {
			return errors.New("plugin: reset paths are unresolved")
		}
	}
	if !containsResetPath(p.PluginsDir, p.CloneDir) {
		return errors.New("plugin: managed clone root is outside the plugins directory")
	}
	return nil
}

// ResetItem is one installed plugin's complete reviewed footprint. Public
// summary fields are safe to render; the path fields are private evidence used
// only by this package's apply and verify operations.
//
// Skills names the plugin's skills for review only: they live in its install
// folder and nothing removes them by name. SkillOwnershipSchema is kept so a
// receipt written while plugin skills were copied still decodes; it is ignored.
type ResetItem struct {
	Name                 string   `json:"name"`
	Version              string   `json:"version,omitempty"`
	Enabled              bool     `json:"enabled"`
	Generation           uint64   `json:"generation,omitempty"`
	MCPServers           []string `json:"mcp_servers,omitempty"`
	Skills               []string `json:"skills,omitempty"`
	SkillOwnershipSchema int      `json:"skill_ownership_schema,omitempty"`
	Surfaces             bool     `json:"surfaces"`
	Artifacts            int      `json:"artifacts,omitempty"`
	// Managed reports whether Ori created and owns the install root. A linked
	// source lives outside the managed clone directory and is never removed.
	Managed     bool   `json:"managed"`
	InstallRoot string `json:"install_root,omitempty"`
}

// SourceDisposition renders the managed-versus-linked distinction for review.
func (item ResetItem) SourceDisposition() string {
	if item.Managed {
		return "managed clone (removed)"
	}
	return "linked source (kept)"
}

// ResetInventory is the bounded authoritative view of every installed plugin.
type ResetInventory struct {
	Items          []ResetItem `json:"items"`
	RegistryPath   string      `json:"registry_path"`
	RegistryDigest string      `json:"registry_digest"`
}

// InspectReset reads the installed registry and derives the exact reviewed
// footprint. It performs no mutation of any kind — no command, service, network
// call, provider call, credential read, workspace write, or vault write. A
// problem is a refusal to proceed, never a silently reduced scope.
func InspectReset(paths ResetPaths) (ResetInventory, []ResetProblem) {
	inventory := ResetInventory{Items: []ResetItem{}}
	if err := paths.validate(); err != nil {
		return inventory, []ResetProblem{{Code: ResetProblemOwnerUnavailable, Detail: "plugin reset roots are unresolved"}}
	}
	inventory.RegistryPath = paths.RegistryPath()

	records, digest, err := readResetRegistry(inventory.RegistryPath)
	if err != nil {
		return inventory, []ResetProblem{{Code: ResetProblemRegistryUnreadable, Detail: "the installed plugin registry could not be read or parsed"}}
	}
	inventory.RegistryDigest = digest
	if len(records) > MaxResetItems {
		return inventory, []ResetProblem{{Code: ResetProblemInventoryTooLarge, Detail: "more installed plugins than one reviewed reset supports"}}
	}

	var problems []ResetProblem
	add := func(code, detail string) {
		item := ResetProblem{Code: code, Detail: detail}
		if !slices.Contains(problems, item) {
			problems = append(problems, item)
		}
	}
	seenNames := make(map[string]bool, len(records))
	serverOwner := make(map[string]string)

	for _, record := range records {
		if !validResetSegment(record.Name) {
			add(ResetProblemNameUnsafe, "an installed record has a name that is not a safe single path segment")
			continue
		}
		if seenNames[record.Name] {
			add(ResetProblemDuplicateRecord, "the installed registry lists the same plugin more than once")
			continue
		}
		seenNames[record.Name] = true

		item := ResetItem{
			Name: record.Name, Version: boundedResetText(record.Version), Enabled: record.Enabled,
			Generation: record.Generation, Surfaces: record.WorkspaceSurfaces != nil,
			Artifacts: len(record.ResolvedArtifacts),
		}
		if len(record.MCPServers) > maxResetComponentItems || len(record.Skills) > maxResetComponentItems {
			add(ResetProblemInventoryTooLarge, "an installed record declares more components than one reviewed reset supports")
			continue
		}
		safe := true
		for _, server := range record.MCPServers {
			if !validResetServerName(record.Name, server) {
				add(ResetProblemComponentUnsafe, "an installed record claims an MCP registration it does not namespace")
				safe = false
				continue
			}
			if owner, exists := serverOwner[server]; exists && owner != record.Name {
				add(ResetProblemComponentUnsafe, "two installed records claim the same MCP registration")
				safe = false
				continue
			}
			if slices.Contains(item.MCPServers, server) {
				add(ResetProblemComponentUnsafe, "an installed record lists the same MCP registration twice")
				safe = false
				continue
			}
			serverOwner[server] = record.Name
			item.MCPServers = append(item.MCPServers, server)
		}
		// Skills are named for review only, so a name that is not a plain
		// single segment is simply left out of the summary.
		for _, skill := range record.Skills {
			if validResetSegment(skill) && !slices.Contains(item.Skills, skill) {
				item.Skills = append(item.Skills, skill)
			}
		}
		if !safe {
			continue
		}

		root, managed, err := resolveResetInstallRoot(paths, record)
		if err != nil {
			add(ResetProblemRootUnresolved, "an installed record's source location cannot be resolved unambiguously")
			continue
		}
		item.InstallRoot, item.Managed = root, managed
		sort.Strings(item.MCPServers)
		sort.Strings(item.Skills)
		inventory.Items = append(inventory.Items, item)
	}
	sort.Slice(inventory.Items, func(i, j int) bool { return inventory.Items[i].Name < inventory.Items[j].Name })
	if len(problems) != 0 {
		// Ownership is all-or-nothing: never return a partial inventory that a
		// caller could mistake for the complete reviewed scope.
		return ResetInventory{
			Items: []ResetItem{}, RegistryPath: inventory.RegistryPath,
			RegistryDigest: inventory.RegistryDigest,
		}, problems
	}
	return inventory, nil
}

// RemoveResetItem applies one plugin's exact removal offline and idempotently.
//
// The order mirrors Manager.Uninstall so that an interrupted run leaves the
// installed record — the ownership authority — in place for a retry: namespaced
// state, then managed artifacts, then the managed clone (and the skills inside
// it), then recorded MCP registrations, and only then the record.
func RemoveResetItem(paths ResetPaths, item ResetItem) error {
	// Every piece of evidence is validated before the first effect, so an item
	// that is unsafe in any respect removes nothing at all.
	if err := validateResetItem(paths, item); err != nil {
		return err
	}
	// Host-owned namespaced key/value state and the plugin service's own data
	// root are different directories under the same managed root. Both belong to
	// this plugin and neither is reachable by name alone without validation.
	for _, directory := range resetStateDirectories(paths, item.Name) {
		if err := removeResetTree(paths.StateRoot(), directory); err != nil {
			return err
		}
	}
	if err := removeResetTree(paths.ArtifactsRoot(), filepath.Join(paths.ArtifactsRoot(), item.Name)); err != nil {
		return err
	}
	if item.Managed {
		clone, err := managedCloneChild(paths, item.InstallRoot)
		if err != nil {
			return err
		}
		if err := removeResetTree(paths.CloneDir, clone); err != nil {
			return err
		}
	}
	if len(item.MCPServers) != 0 {
		if _, err := mcp.RemoveRegisteredServers(paths.MCPRegistry, item.MCPServers); err != nil {
			return err
		}
	}
	return deleteResetRecord(paths.RegistryPath(), item.Name)
}

// validateResetItem rejects any evidence that could name a location outside an
// owned root, before a removal or a verification touches the filesystem.
func validateResetItem(paths ResetPaths, item ResetItem) error {
	if err := paths.validate(); err != nil {
		return err
	}
	if !validResetSegment(item.Name) {
		return fmt.Errorf("plugin: reset item name is unsafe")
	}
	for _, server := range item.MCPServers {
		if !validResetServerName(item.Name, server) {
			return fmt.Errorf("plugin: reset MCP registration is not namespaced to this plugin")
		}
	}
	if item.Managed {
		if _, err := managedCloneChild(paths, item.InstallRoot); err != nil {
			return err
		}
	}
	return nil
}

// ResetItemRemoved verifies one plugin's named postconditions by re-reading the
// owners. A missing registry row alone is never accepted as proof: every
// recorded external component is checked independently.
func ResetItemRemoved(paths ResetPaths, item ResetItem) (bool, error) {
	if err := validateResetItem(paths, item); err != nil {
		return false, err
	}
	records, _, err := readResetRegistry(paths.RegistryPath())
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if record.Name == item.Name {
			return false, nil
		}
	}
	for _, directory := range resetStateDirectories(paths, item.Name) {
		if exists, err := resetPathExists(directory); err != nil || exists {
			return false, err
		}
	}
	if exists, err := resetPathExists(filepath.Join(paths.ArtifactsRoot(), item.Name)); err != nil || exists {
		return false, err
	}
	if item.Managed && item.InstallRoot != "" {
		clone, err := managedCloneChild(paths, item.InstallRoot)
		if err != nil {
			return false, err
		}
		if exists, err := resetPathExists(clone); err != nil || exists {
			return false, err
		}
	}
	if len(item.MCPServers) != 0 {
		names, err := mcp.RegisteredServerNames(paths.MCPRegistry)
		if err != nil {
			return false, err
		}
		for _, server := range item.MCPServers {
			if slices.Contains(names, server) {
				return false, nil
			}
		}
	}
	return true, nil
}

// RemoveResetPreviewCache clears the managed update-preview checkouts. It is a
// category-level step that runs only once every reviewed plugin is verifiably
// removed, because the cache is shared, re-derivable, and owned by no single
// record. Linked sources and marketplaces are untouched.
func RemoveResetPreviewCache(paths ResetPaths) error {
	if err := paths.validate(); err != nil {
		return err
	}
	return removeResetTree(paths.PluginsDir, paths.PreviewRoot())
}

// ResetRegistryEmpty reports whether the installed registry lists no plugin.
func ResetRegistryEmpty(paths ResetPaths) (bool, error) {
	if err := paths.validate(); err != nil {
		return false, err
	}
	records, _, err := readResetRegistry(paths.RegistryPath())
	if err != nil {
		return false, err
	}
	return len(records) == 0, nil
}

// resetStateDirectories returns both managed namespaced-state locations for a
// plugin: the hashed host-owned key/value namespace and the plugin service's own
// data root, which the Workspace Surface context addresses by raw plugin id.
func resetStateDirectories(paths ResetPaths, name string) []string {
	root := paths.StateRoot()
	return []string{
		filepath.Join(root, ResetStateNamespace(name)),
		filepath.Join(root, name),
	}
}

func managedCloneChild(paths ResetPaths, installRoot string) (string, error) {
	if installRoot == "" || !containsResetPath(paths.CloneDir, installRoot) {
		return "", fmt.Errorf("plugin: managed clone is outside the managed clone root")
	}
	relative, err := filepath.Rel(paths.CloneDir, installRoot)
	if err != nil || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("plugin: managed clone is outside the managed clone root")
	}
	first := relative
	if index := strings.IndexRune(relative, filepath.Separator); index >= 0 {
		first = relative[:index]
	}
	if !validResetSegment(first) {
		return "", fmt.Errorf("plugin: managed clone directory name is unsafe")
	}
	return filepath.Join(paths.CloneDir, first), nil
}

func resolveResetInstallRoot(paths ResetPaths, record InstalledPlugin) (string, bool, error) {
	root, err := canonicalInstallRoot(record.InstallDir, record.Source, paths.CloneDir)
	if err != nil {
		// An absolute recorded root that no longer exists is unambiguous: there is
		// nothing left to remove and nothing to confuse it with.
		trimmed := strings.TrimSpace(record.InstallDir)
		if trimmed == "" || !filepath.IsAbs(trimmed) {
			return "", false, err
		}
		root = filepath.Clean(trimmed)
	}
	if len(root) > maxResetPathBytes {
		return "", false, fmt.Errorf("plugin: install root is unsupported")
	}
	managed := containsResetPath(paths.CloneDir, root)
	if managed {
		if _, err := managedCloneChild(paths, root); err != nil {
			return "", false, err
		}
	}
	return root, managed, nil
}

// removeResetTree deletes exactly one entry confined to an owned root. It never
// follows a symlinked parent out of that root and never removes the root itself.
func removeResetTree(root, path string) error {
	if root == "" || path == "" || path == root || !containsResetPath(root, path) {
		return fmt.Errorf("plugin: reset target is outside its owned root")
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return fmt.Errorf("plugin: reset target is outside its owned root")
	}
	owned, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = owned.Close() }()
	if _, err := owned.Lstat(relative); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := owned.RemoveAll(relative); err != nil {
		return err
	}
	parent, err := owned.Open(filepath.Dir(relative))
	if err != nil {
		return err
	}
	syncErr := parent.Sync()
	closeErr := parent.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return err
	}
	if _, err := owned.Lstat(relative); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("plugin: reset target remained after removal")
	}
	return nil
}

func resetPathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func readResetRegistry(path string) ([]InstalledPlugin, string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, resetDigest(nil), nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, "", fmt.Errorf("plugin: installed registry is unreadable")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- canonical absolute registry path resolved by the host, never client supplied
	if err != nil {
		return nil, "", fmt.Errorf("plugin: installed registry is unreadable")
	}
	var records []InstalledPlugin
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, "", fmt.Errorf("plugin: installed registry is unparsable")
	}
	return records, resetDigest(data), nil
}

// deleteResetRecord removes one record and rewrites the registry.
//
// The encoding matches Store.save byte-for-byte so the file install and
// uninstall write is the file reset leaves behind. The write itself is atomic
// and fsynced, which Store.save is not: recovery runs before any store exists
// to repair a torn registry, and the registry is the ownership authority a
// retry depends on.
func deleteResetRecord(path, name string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	records, _, err := readResetRegistry(path)
	if err != nil {
		return err
	}
	remaining := make([]InstalledPlugin, 0, len(records))
	for _, record := range records {
		if record.Name != name {
			remaining = append(remaining, record)
		}
	}
	if len(remaining) == len(records) {
		return nil
	}
	data, err := json.MarshalIndent(remaining, "", "  ")
	if err != nil {
		return fmt.Errorf("plugin: encode installed registry: %w", err)
	}
	return writeResetFileAtomic(path, data, info.Mode().Perm())
}

func writeResetFileAtomic(path string, data []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o600
	}
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".installed-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	committed = true
	parent, err := os.Open(directory) // #nosec G304 -- parent of the canonical managed registry
	if err != nil {
		return err
	}
	return errors.Join(parent.Sync(), parent.Close())
}

func resetDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// validResetSegment accepts only a plain, visible, single path component. Plugin
// and skill names come from external manifests and directory listings and are
// otherwise unvalidated, so this is the boundary that keeps a corrupt or hostile
// record from naming a location outside its owned root.
func validResetSegment(name string) bool {
	if name == "" || len(name) > maxResetNameBytes || !utf8.ValidString(name) {
		return false
	}
	if strings.ContainsAny(name, "/\\:") || strings.ContainsRune(name, 0) {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.TrimSpace(name) != name {
		return false
	}
	if filepath.Clean(name) != name || filepath.IsAbs(name) || !filepath.IsLocal(name) {
		return false
	}
	for _, ch := range name {
		if ch < 0x20 || ch == 0x7f {
			return false
		}
	}
	return true
}

// validResetServerName requires the recorded registration to be namespaced to
// this plugin, which is what Register always produces. A record that claims an
// unnamespaced or foreign entry cannot authorize its removal.
func validResetServerName(plugin, server string) bool {
	prefix := NamespacedServerName(plugin, "")
	if !strings.HasPrefix(server, prefix) || len(server) <= len(prefix) || len(server) > maxResetPathBytes {
		return false
	}
	suffix := server[len(prefix):]
	return utf8.ValidString(suffix) && !strings.ContainsRune(suffix, 0) && strings.TrimSpace(suffix) == suffix
}

func boundedResetText(value string) string {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) {
		return ""
	}
	if len(value) > maxResetNameBytes {
		return value[:maxResetNameBytes]
	}
	return value
}

func containsResetPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(relative)
}
