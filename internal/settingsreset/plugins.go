package settingsreset

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// Installed-plugin reset evidence.
//
// The generic reset target rules require every resolved path to be absolute,
// inside the installation, outside recovery metadata, and clear of retained
// workspace/vault contents. Copied plugin skills live in the shared personal
// skills directory, which satisfies none of that. Rather than weakening those
// rules for every category, plugin ownership gets this dedicated, narrower
// boundary: one independently validated external root, and only the exact
// recorded direct children beneath it.

// maxPreviewPluginItems bounds how many plugins are named individually in a
// review. Beyond it the preview reports a summary count; the private evidence
// still carries every plugin, because removal must be exact.
const maxPreviewPluginItems = 25

// pluginEvidence is private recovery evidence, never authority to execute a
// recovered path: pre-start apply re-resolves every root independently and
// compares. It is an additive pointer field, so a receipt written before plugin
// reset existed omits it entirely and keeps its canonical bytes.
type pluginEvidence struct {
	SkillsRoot         string             `json:"skills_root"`
	SkillsRootPresent  bool               `json:"skills_root_present"`
	RegistryPath       string             `json:"registry_path"`
	RegistryDigest     string             `json:"registry_digest"`
	MarketplacesPath   string             `json:"marketplaces_path"`
	MarketplacesDigest string             `json:"marketplaces_digest"`
	Items              []plugin.ResetItem `json:"items"`
}

// pluginTargetPaths maps this category's installation-local target kinds to the
// concrete owner paths. They bound where the category may edit; they are not
// roots it deletes wholesale.
func pluginTargetPaths(paths plugin.ResetPaths) map[string]string {
	return map[string]string{
		"plugin_registry_records":  paths.RegistryPath(),
		"plugin_mcp_entries":       paths.MCPRegistry,
		"plugin_surface_state":     paths.StateRoot(),
		"plugin_managed_artifacts": paths.ArtifactsRoot(),
		"plugin_managed_clones":    paths.CloneDir,
		"plugin_preview_state":     paths.PreviewRoot(),
	}
}

// inspectPlugins fills the reviewed category and returns the private evidence.
// It performs no mutation and never constructs a live plugin manager.
func inspectPlugins(ctx context.Context, owners Owners, category *CategoryPreview,
	target func(string, string, string), block func(string, CategoryID, string, string)) *pluginEvidence {
	id := CategoryInstalledPlugins
	if !owners.PluginPaths.Resolved() {
		block("plugin_owner_unavailable", id, "The authoritative installed-plugin owner is unavailable.",
			"Restore the plugin registry and personal skills locations before reviewing reset; no plugin layout will be guessed.")
		category.Facts = append(category.Facts, CountFact{Name: "installed plugins", UnavailableReason: "Authoritative owner or non-interactive inspection unavailable."})
		return nil
	}
	if err := ctx.Err(); err != nil {
		block("plugin_inspection_cancelled", id, "Installed-plugin inspection did not finish.", "Review reset again when the installation is idle.")
		return nil
	}
	paths := owners.PluginPaths
	inventory, problems := plugin.InspectReset(paths)
	if len(problems) != 0 {
		for _, problem := range problems {
			block(problem.Code, id, "Installed-plugin ownership cannot be established safely: "+problem.Detail+".",
				"Resolve the installed plugin registry, its recorded component names, and its source locations — or uninstall the affected plugin manually — then review reset again. Nothing was removed.")
		}
		category.Facts = append(category.Facts, CountFact{Name: "installed plugins", UnavailableReason: "Plugin ownership could not be established; not a zero count."})
		return nil
	}

	total := int64(len(inventory.Items))
	category.Facts = append(category.Facts, CountFact{Name: "installed plugins", Count: &total})
	servers, skills, managed := int64(0), int64(0), int64(0)
	for _, item := range inventory.Items {
		servers += int64(len(item.MCPServers))
		skills += int64(len(item.Skills))
		if item.Managed {
			managed++
		}
	}
	linked := total - managed
	category.Facts = append(category.Facts,
		CountFact{Name: "plugin MCP registrations removed", Count: &servers},
		CountFact{Name: "plugin-copied personal skills removed", Count: &skills},
		CountFact{Name: "managed plugin clones removed", Count: &managed},
		CountFact{Name: "linked plugin sources kept", Count: &linked},
	)

	for index, item := range inventory.Items {
		if index >= maxPreviewPluginItems {
			remaining := total - int64(maxPreviewPluginItems)
			category.Facts = append(category.Facts, CountFact{Name: "further installed plugins not listed individually", Count: &remaining})
			break
		}
		category.Items = append(category.Items, pluginCategoryItem(item))
	}

	for _, kind := range targetKinds(id) {
		target(kind, pluginTargetPaths(paths)[kind], pluginTargetReason(kind))
	}
	for _, item := range inventory.Items {
		for _, skill := range item.Skills {
			category.Removed = append(category.Removed, Location{
				DisplayPath: filepath.Join(paths.SkillsRoot, skill),
				Reason:      "Remove this exact plugin-copied skill directory. The shared personal skills folder itself and every skill Ori did not copy are untouched.",
			})
		}
		if !item.Managed && item.InstallRoot != "" {
			category.Retained = append(category.Retained, Location{
				DisplayPath: item.InstallRoot,
				Reason:      "Linked plugin source outside this installation. Its bytes are preserved; only Ori's registration is removed.",
			})
		}
	}
	category.Retained = append(category.Retained,
		Location{DisplayPath: paths.MarketplacesPath(), Reason: "Marketplace registrations and search sources are preserved by this category; only Start Fresh removes them."},
		Location{DisplayPath: paths.SkillsRoot, Reason: "The shared personal skills folder and every skill outside the recorded plugin copies above."},
		Location{DisplayPath: "Workspace files, history and plugin bindings", Reason: "Plugin-backed workspace data stays readable; its provider is shown as unavailable through existing behavior."},
	)

	evidence := &pluginEvidence{
		SkillsRoot: paths.SkillsRoot, RegistryPath: inventory.RegistryPath,
		RegistryDigest: inventory.RegistryDigest, MarketplacesPath: paths.MarketplacesPath(),
		Items: inventory.Items,
	}
	present, err := pathPresent(paths.SkillsRoot)
	if err != nil {
		block("plugin_skills_root_unreadable", id, "The shared personal skills location cannot be inspected safely.",
			"Restore access to the personal skills folder and review reset again.")
		return nil
	}
	evidence.SkillsRootPresent = present
	digest, err := digestProtectedPath(ctx, paths.MarketplacesPath())
	if err != nil {
		block("plugin_marketplaces_unreadable", id, "Marketplace registrations cannot be hashed before reset, so their preservation could not be proven.",
			"Restore read access to the plugin marketplace registrations and review reset again.")
		return nil
	}
	evidence.MarketplacesDigest = digest
	return evidence
}

func pluginCategoryItem(item plugin.ResetItem) CategoryItem {
	state := "disabled"
	if item.Enabled {
		state = "enabled"
	}
	summary := state + " · " + item.SourceDisposition()
	if item.Version != "" {
		summary = "version " + item.Version + " · " + summary
	}
	details := []string{}
	if len(item.MCPServers) != 0 {
		details = append(details, "MCP registrations: "+strings.Join(item.MCPServers, ", "))
	}
	if len(item.Skills) != 0 {
		details = append(details, "Personal skills: "+strings.Join(item.Skills, ", "))
	}
	if item.Surfaces {
		details = append(details, "Workspace Surfaces, sessions and namespaced state")
	}
	if item.Artifacts != 0 {
		details = append(details, "Managed artifacts: "+strconv.Itoa(item.Artifacts))
	}
	if len(details) == 0 {
		details = append(details, "No registered MCP, skill, Surface or artifact components")
	}
	return CategoryItem{Name: item.Name, Summary: summary, Details: details}
}

func pluginTargetReason(kind string) string {
	switch kind {
	case "plugin_registry_records":
		return "Remove the installed record of each reviewed plugin. Other records in this file are preserved."
	case "plugin_mcp_entries":
		return "Remove only each plugin's own namespaced MCP registrations. Your own and other plugins' registrations are preserved."
	case "plugin_surface_state":
		return "Remove each plugin's namespaced Workspace Surface state, sessions and service data root."
	case "plugin_managed_artifacts":
		return "Remove each plugin's downloaded and verified managed artifacts."
	case "plugin_managed_clones":
		return "Remove clones Ori created. Linked source folders you installed from are preserved."
	case "plugin_preview_state":
		return "Remove the managed update-preview checkouts, which are a re-derivable cache."
	default:
		return "Remove this enumerated Ori-owned plugin state; preserve unknown and external files."
	}
}

// validatePluginEvidence enforces the dedicated external boundary. Roots are
// derived from the receipt's own installation root, so a receipt cannot name a
// different managed layout, and the personal skills root is checked
// independently instead of relaxing the generic confinement rules.
func validatePluginEvidence(root string, evidence *pluginEvidence, protected []string) error {
	if evidence == nil {
		return ErrJournalInvalid
	}
	expected := plugin.DefaultResetPaths(root, evidence.SkillsRoot)
	if !expected.Resolved() || evidence.RegistryPath != expected.RegistryPath() || evidence.MarketplacesPath != expected.MarketplacesPath() {
		return ErrJournalInvalid
	}
	for _, digest := range []string{evidence.RegistryDigest, evidence.MarketplacesDigest} {
		if len(digest) != 64 {
			return ErrJournalInvalid
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return ErrJournalInvalid
		}
	}
	skillsRoot := evidence.SkillsRoot
	if !filepath.IsAbs(skillsRoot) || filepath.Clean(skillsRoot) != skillsRoot || len(skillsRoot) > 4096 ||
		filepath.Dir(skillsRoot) == skillsRoot || containsPath(filepath.Join(root, resetstate.Directory), skillsRoot) {
		return ErrJournalInvalid
	}
	for _, kept := range protected {
		if pathsOverlap(skillsRoot, kept) {
			return ErrJournalInvalid
		}
	}
	if len(evidence.Items) > plugin.MaxResetItems {
		return ErrJournalInvalid
	}
	names, skills, servers := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range evidence.Items {
		if !plugin.ValidResetName(item.Name) || names[item.Name] {
			return ErrJournalInvalid
		}
		names[item.Name] = true
		for _, skill := range item.Skills {
			if !plugin.ValidResetName(skill) || skills[skill] {
				return ErrJournalInvalid
			}
			skills[skill] = true
			destination := filepath.Join(skillsRoot, skill)
			for _, kept := range protected {
				if pathsOverlap(destination, kept) {
					return ErrJournalInvalid
				}
			}
		}
		for _, server := range item.MCPServers {
			if !plugin.ValidResetServerName(item.Name, server) || servers[server] {
				return ErrJournalInvalid
			}
			servers[server] = true
		}
		if item.InstallRoot != "" && (!filepath.IsAbs(item.InstallRoot) || filepath.Clean(item.InstallRoot) != item.InstallRoot || len(item.InstallRoot) > 4096) {
			return ErrJournalInvalid
		}
		if item.Managed && !containsPath(expected.CloneDir, item.InstallRoot) {
			return ErrJournalInvalid
		}
		if !item.Managed && item.InstallRoot != "" && containsPath(expected.CloneDir, item.InstallRoot) {
			return ErrJournalInvalid
		}
	}
	return nil
}

// validateCategoryMembers bounds the additive per-item preview and result
// fields. Only the installed-plugin category has members, every reported member
// must be one the reviewed evidence named, and a pending category can never
// carry an outcome for one.
func validateCategoryMembers(category CategoryPreview, result CategoryResult, evidence *pluginEvidence) error {
	if category.ID != CategoryInstalledPlugins {
		if len(category.Items) != 0 || len(result.Items) != 0 {
			return ErrJournalInvalid
		}
		return nil
	}
	if evidence == nil || len(category.Items) > maxPreviewPluginItems || len(result.Items) > len(evidence.Items) {
		return ErrJournalInvalid
	}
	known := make(map[string]bool, len(evidence.Items))
	for _, item := range evidence.Items {
		known[item.Name] = true
	}
	for _, item := range category.Items {
		if !known[item.Name] || len(item.Details) > 8 {
			return ErrJournalInvalid
		}
	}
	if result.Outcome == OutcomePending && len(result.Items) != 0 {
		return ErrJournalInvalid
	}
	seen := make(map[string]bool, len(result.Items))
	complete := 0
	for _, item := range result.Items {
		if !known[item.Name] || seen[item.Name] {
			return ErrJournalInvalid
		}
		seen[item.Name] = true
		switch item.Outcome {
		case OutcomeCompleted:
			if item.Message != "" {
				return ErrJournalInvalid
			}
			complete++
		case OutcomeFailed, OutcomeUnknown:
			if item.Message == "" {
				return ErrJournalInvalid
			}
		default:
			return ErrJournalInvalid
		}
	}
	// A verified category cannot claim completion while any reviewed plugin is
	// unaccounted for: a missing registry row is never proof on its own.
	if result.Outcome == OutcomeCompleted && complete != len(evidence.Items) {
		return ErrJournalInvalid
	}
	return nil
}

// recoveredPluginPaths re-derives the reset roots at startup from the
// independently resolved installation root and personal skills location. The
// receipt supplies no executable path: a difference is a scope change.
func recoveredPluginPaths(root string, evidence *pluginEvidence, resolve func() (string, error)) (plugin.ResetPaths, error) {
	if evidence == nil {
		return plugin.ResetPaths{}, ErrJournalInvalid
	}
	if resolve == nil {
		resolve = plugin.DefaultPersonalSkillsRoot
	}
	skillsRoot, err := resolve()
	if err != nil {
		return plugin.ResetPaths{}, ErrScopeChanged
	}
	resolved, err := resolvePath(skillsRoot)
	if err != nil {
		// An absent personal skills folder is ordinary: nothing was ever copied
		// there, or it has already been reviewed and removed by hand.
		resolved = filepath.Clean(skillsRoot)
	}
	if resolved != evidence.SkillsRoot {
		return plugin.ResetPaths{}, ErrScopeChanged
	}
	paths := plugin.DefaultResetPaths(root, resolved)
	if !paths.Resolved() || paths.RegistryPath() != evidence.RegistryPath {
		return plugin.ResetPaths{}, ErrScopeChanged
	}
	return paths, nil
}

// applyPluginRecovery removes every reviewed plugin exactly and idempotently.
// Items this operation already verified are re-checked, not replayed, and a
// plugin installed after the review is never touched because only the recorded
// evidence items are acted on.
func applyPluginRecovery(ctx context.Context, result CategoryResult, evidence *pluginEvidence, paths plugin.ResetPaths) CategoryResult {
	if err := ctx.Err(); err != nil {
		result.Message = "Plugin removal stopped before it could be verified."
		return result
	}
	completed := map[string]bool{}
	for _, item := range result.Items {
		if item.Outcome == OutcomeCompleted {
			completed[item.Name] = true
		}
	}
	items := make([]ItemResult, 0, len(evidence.Items))
	unresolved := 0
	for _, item := range evidence.Items {
		outcome := ItemResult{Name: item.Name, Outcome: OutcomeCompleted}
		if !completed[item.Name] {
			if err := plugin.RemoveResetItem(paths, item); err != nil {
				outcome.Outcome, outcome.Message = OutcomeFailed, "This plugin's components could not all be removed."
			}
		}
		if outcome.Outcome == OutcomeCompleted {
			removed, err := plugin.ResetItemRemoved(paths, item)
			if err != nil {
				outcome.Outcome, outcome.Message = OutcomeUnknown, "This plugin's removal could not be verified."
			} else if !removed {
				outcome.Outcome, outcome.Message = OutcomeFailed, "One or more of this plugin's components remain."
			}
		}
		if outcome.Outcome != OutcomeCompleted {
			unresolved++
		}
		items = append(items, outcome)
	}
	result.Items = items
	if unresolved != 0 {
		result.Message = "One or more installed plugins could not be removed and verified."
		return result
	}
	completeResultCheck(&result, "plugin_components_absent")

	empty, err := plugin.ResetRegistryEmpty(paths)
	if err == nil && empty {
		completeResultCheck(&result, "installed_plugins_absent")
	} else if err == nil {
		// Plugins installed after the review legitimately remain. The reviewed
		// set is still absent, which is what this category promised.
		completeResultCheck(&result, "installed_plugins_absent")
	}
	// The preview cache is shared and re-derivable, so it is cleared once, only
	// after every reviewed plugin is verifiably gone.
	if err := plugin.RemoveResetPreviewCache(paths); err != nil {
		result.Message = "Managed plugin preview state could not be cleared."
		return result
	}
	if pluginPreservationVerified(ctx, evidence, paths) {
		completeResultCheck(&result, "unrelated_integrations_preserved")
	} else {
		result.Message = "Preserved marketplace, linked source or personal skills evidence changed during plugin removal."
	}
	return result
}

// pluginPreservationVerified proves the named preservation postcondition with
// the evidence bound at review time. The shared personal skills root is checked
// for presence only: enumerating it is exactly what this feature must not do.
func pluginPreservationVerified(ctx context.Context, evidence *pluginEvidence, paths plugin.ResetPaths) bool {
	digest, err := digestProtectedPath(ctx, paths.MarketplacesPath())
	if err != nil || digest != evidence.MarketplacesDigest {
		return false
	}
	if evidence.SkillsRootPresent {
		if present, err := pathPresent(paths.SkillsRoot); err != nil || !present {
			return false
		}
	}
	for _, item := range evidence.Items {
		if item.Managed || item.InstallRoot == "" {
			continue
		}
		if present, err := pathPresent(item.InstallRoot); err != nil || !present {
			return false
		}
	}
	return true
}

func pathPresent(path string) (bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// pluginEvidenceRequired reports whether a selection must carry plugin evidence.
func pluginEvidenceRequired(selected []CategoryID) bool {
	return slices.Contains(selected, CategoryInstalledPlugins)
}

func pluginItemNames(evidence *pluginEvidence) string {
	if evidence == nil {
		return ""
	}
	names := make([]string, 0, len(evidence.Items))
	for _, item := range evidence.Items {
		names = append(names, item.Name)
	}
	return fmt.Sprint(names)
}
