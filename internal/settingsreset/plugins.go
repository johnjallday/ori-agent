package settingsreset

import (
	"context"
	"encoding/hex"
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

// resolvedPluginPaths derives the plugin layout from the installation root the
// planner already resolved, plus the personal skills root resolved through the
// same helper the pre-store recovery resolver uses.
//
// Both sides must agree byte-for-byte or a reviewed operation looks like a
// changed scope and refuses to apply. Storing an unresolved root here and
// resolving it at recovery is exactly that bug: on macOS a temporary or
// symlinked HOME resolves to a different absolute path, and the receipt is
// stranded through no fault of the user.
func resolvedPluginPaths(installationRoot, skillsRoot string) (plugin.ResetPaths, bool) {
	if strings.TrimSpace(installationRoot) == "" || strings.TrimSpace(skillsRoot) == "" {
		return plugin.ResetPaths{}, false
	}
	paths := plugin.DefaultResetPaths(installationRoot, resolveOwnedRoot(skillsRoot))
	return paths, paths.Resolved()
}

// resolveOwnedRoot canonicalizes a location that may not exist yet. An absent
// personal skills folder is ordinary — nothing was ever copied into it.
func resolveOwnedRoot(path string) string {
	if resolved, err := resolvePath(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
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
//
// It serves both owners of plugin removal. The selective category passes a
// target function and takes the six installation-local scopes; Start Fresh
// passes none, because its broader `integrations` targets already cover them —
// but it needs the same evidence so its plugin portion can run the identical
// exact cleanup before those broader roots are deleted.
func inspectPlugins(ctx context.Context, owners Owners, id CategoryID, installationRoot string, category *CategoryPreview,
	target func(string, string, string), block func(string, CategoryID, string, string)) *pluginEvidence {
	paths, ok := resolvedPluginPaths(installationRoot, owners.PluginPaths.SkillsRoot)
	if !ok {
		block("plugin_owner_unavailable", id, "The authoritative installed-plugin owner is unavailable.",
			"Restore the plugin registry and personal skills locations before reviewing reset; no plugin layout will be guessed.")
		category.Facts = append(category.Facts, CountFact{Name: "installed plugins", UnavailableReason: "Authoritative owner or non-interactive inspection unavailable."})
		return nil
	}
	if err := ctx.Err(); err != nil {
		block("plugin_inspection_cancelled", id, "Installed-plugin inspection did not finish.", "Review reset again when the installation is idle.")
		return nil
	}
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

	if target != nil {
		for _, kind := range targetKinds(id) {
			target(kind, pluginTargetPaths(paths)[kind], pluginTargetReason(kind))
		}
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
	if id == CategoryInstalledPlugins {
		category.Retained = append(category.Retained, Location{
			DisplayPath: paths.MarketplacesPath(),
			Reason:      "Marketplace registrations and search sources are preserved by this category; only Start Fresh removes them.",
		})
	}
	category.Retained = append(category.Retained,
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
	ownsPlugins := category.ID == CategoryInstalledPlugins ||
		(category.ID == CategoryIntegrations && evidence != nil)
	if !ownsPlugins {
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
	paths, ok := resolvedPluginPaths(root, skillsRoot)
	if !ok || paths.SkillsRoot != evidence.SkillsRoot || paths.RegistryPath() != evidence.RegistryPath {
		return plugin.ResetPaths{}, ErrScopeChanged
	}
	return paths, nil
}

// applyPluginRecovery removes every reviewed plugin exactly and idempotently.
// Items this operation already verified are re-checked, not replayed, and a
// plugin installed after the review is never touched because only the recorded
// evidence items are acted on.
func applyPluginRecovery(ctx context.Context, result CategoryResult, evidence *pluginEvidence, paths plugin.ResetPaths) CategoryResult {
	if !removePluginItems(ctx, &result, evidence, paths) {
		return result
	}
	completeResultCheck(&result, "plugin_components_absent")

	if empty, err := plugin.ResetRegistryEmpty(paths); err == nil {
		// Plugins installed after the review legitimately remain. The reviewed
		// set is verified absent above, which is what this category promised.
		_ = empty
		completeResultCheck(&result, "installed_plugins_absent")
	}
	// The preview cache is shared and re-derivable, so it is cleared once, only
	// after every reviewed plugin is verifiably gone.
	if err := plugin.RemoveResetPreviewCache(paths); err != nil {
		result.Message = "Managed plugin preview state could not be cleared."
		return result
	}
	if pluginPreservationVerified(ctx, evidence, paths, true) {
		completeResultCheck(&result, "unrelated_integrations_preserved")
	} else {
		result.Message = "Preserved marketplace, linked source or personal skills evidence changed during plugin removal."
	}
	return result
}

// applyFreshPluginRemoval is Start Fresh's plugin portion. It runs the identical
// exact removal owner, and must complete before the broader integration targets
// delete the installed registry and MCP document that are its authority.
//
// Start Fresh legitimately removes marketplaces afterwards, so preservation
// here covers linked sources and the shared personal skills root only.
func applyFreshPluginRemoval(ctx context.Context, result *CategoryResult, evidence *pluginEvidence, paths plugin.ResetPaths) bool {
	if evidence == nil {
		// A receipt written before plugin reset existed keeps its original raw
		// managed-root semantics; there is no exact pass to run.
		return true
	}
	if !removePluginItems(ctx, result, evidence, paths) {
		return false
	}
	if !pluginPreservationVerified(ctx, evidence, paths, false) {
		result.Message = "Linked plugin sources or the shared personal skills folder changed during plugin removal."
		return false
	}
	return true
}

// removePluginItems removes every reviewed plugin and records a bounded
// per-plugin outcome. It reports whether all of them are verifiably gone.
func removePluginItems(ctx context.Context, result *CategoryResult, evidence *pluginEvidence, paths plugin.ResetPaths) bool {
	if err := ctx.Err(); err != nil {
		result.Message = "Plugin removal stopped before it could be verified."
		return false
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
		return false
	}
	return true
}

// pluginPreservationVerified proves the named preservation postcondition with
// the evidence bound at review time. The shared personal skills root is checked
// for presence only: enumerating it is exactly what this feature must not do.
//
// marketplaces is false for Start Fresh, which legitimately removes marketplace
// registrations under its own broader policy after this exact pass completes.
func pluginPreservationVerified(ctx context.Context, evidence *pluginEvidence, paths plugin.ResetPaths, marketplaces bool) bool {
	if marketplaces {
		digest, err := digestProtectedPath(ctx, paths.MarketplacesPath())
		if err != nil || digest != evidence.MarketplacesDigest {
			return false
		}
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

// pluginEvidenceRequired reports whether a selection must carry plugin
// evidence. The selective category cannot function without it.
func pluginEvidenceRequired(selected []CategoryID) bool {
	return slices.Contains(selected, CategoryInstalledPlugins)
}

// pluginEvidencePermitted reports whether a selection may carry plugin
// evidence. Start Fresh may, but must not require it: every Start Fresh receipt
// written before this feature existed has none, and must keep recovering under
// its original raw managed-root semantics rather than being stranded.
func pluginEvidencePermitted(selected []CategoryID) bool {
	return pluginEvidenceRequired(selected) || slices.Contains(selected, CategoryIntegrations)
}
