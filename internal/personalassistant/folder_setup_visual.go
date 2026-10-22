package personalassistant

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// FolderSetupVisual is an optional, display-only snapshot of facts already used
// to word a reviewed plan. It carries no IDs, paths, permissions or actions the
// server can execute. Confirmation still re-derives the plan and checks its
// digest; neither this projection nor a client's rendering supplies authority.
// Older saved runs may have no snapshot and must render unknown facts neutrally.
type FolderSetupVisual struct {
	Entity  *FolderSetupEntity  `json:"entity,omitempty"`
	Plugins []FolderSetupPlugin `json:"plugins,omitempty"`
}

// FolderSetupEntity describes the proposed result, not a completed resource.
// Actual creation/membership is proved separately by canonical run/receipt data.
type FolderSetupEntity struct {
	Kind      string `json:"kind"` // workspace, group, home
	Name      string `json:"name"`
	Operation string `json:"operation"` // create, reuse
}

// FolderSetupPlugin describes an existing reviewed plugin operation. Empty
// versions mean unknown; an installed-from version must never be guessed from
// the target release or the prototype. Kind matches the original plan line.
type FolderSetupPlugin struct {
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	Operation        string `json:"operation"`
	Version          string `json:"version,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
}

// CloneFolderSetupVisual bounds untrusted/legacy display data on every outward
// projection. Invalid facts are omitted rather than repaired into invented facts.
// It intentionally does not validate or modify the executable plan's intent.
func CloneFolderSetupVisual(value *FolderSetupVisual) *FolderSetupVisual {
	if value == nil {
		return nil
	}
	out := &FolderSetupVisual{}
	if entity := value.Entity; entity != nil && visualText(entity.Name, 120) && entity.Name != "" &&
		(entity.Kind == "workspace" || entity.Kind == "group" || entity.Kind == "home") &&
		(entity.Operation == FolderSoftwareCreate || entity.Operation == FolderSoftwareReuse) {
		copied := *entity
		out.Entity = &copied
	}
	seen := map[string]bool{}
	for _, plugin := range value.Plugins {
		if (plugin.Kind != FolderPlanIntegration && plugin.Kind != FolderPlanProvider) || seen[plugin.Kind] ||
			!visualText(plugin.Name, 120) || plugin.Name == "" {
			continue
		}
		switch plugin.Operation {
		case FolderInstallReady, FolderInstallInstall, FolderInstallEnable, FolderInstallUpdate:
		default:
			continue
		}
		if !visualText(plugin.Version, 80) {
			plugin.Version = ""
		}
		if !visualText(plugin.InstalledVersion, 80) {
			plugin.InstalledVersion = ""
		}
		seen[plugin.Kind] = true
		out.Plugins = append(out.Plugins, plugin)
	}
	if out.Entity == nil && len(out.Plugins) == 0 {
		return nil
	}
	return out
}

func visualText(value string, maxRunes int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes &&
		!looksLikeFilesystemPath(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}
