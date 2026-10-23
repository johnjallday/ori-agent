package personalassistant

import (
	"context"
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
	Placement string              `json:"placement,omitempty"` // grouped, standalone; no destination authority
	Entity    *FolderSetupEntity  `json:"entity,omitempty"`
	Plugins   []FolderSetupPlugin `json:"plugins,omitempty"`
	// RequirementsChecked is true only for a complete, checked plugin set.
	// Legacy/partial rows cannot imply zero downloads.
	RequirementsChecked bool `json:"requirements_checked,omitempty"`
}

// FolderSetupReadinessReader is an optional observational display seam for
// explicit review. It supplies no executable plan or authorization.
type FolderSetupReadinessReader interface {
	ReadSetupReadiness(context.Context, FolderSetupRequest) *FolderSetupVisual
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
	Source           string `json:"source,omitempty"`
	Purpose          string `json:"purpose,omitempty"`
}

// CloneFolderSetupVisual bounds untrusted/legacy display data on every outward
// projection. Invalid facts are omitted rather than repaired into invented facts.
// It intentionally does not validate or modify the executable plan's intent.
func CloneFolderSetupVisual(value *FolderSetupVisual) *FolderSetupVisual {
	if value == nil {
		return nil
	}
	out := &FolderSetupVisual{RequirementsChecked: value.RequirementsChecked}
	if value.Placement == "grouped" || value.Placement == "standalone" {
		out.Placement = value.Placement
	}
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
			out.RequirementsChecked = false
			continue
		}
		switch plugin.Operation {
		case FolderInstallReady, FolderInstallInstall, FolderInstallEnable, FolderInstallUpdate:
		case "not_checked", "unavailable", "unverified":
			out.RequirementsChecked = false
		default:
			out.RequirementsChecked = false
			continue
		}
		if !visualText(plugin.Version, 80) {
			plugin.Version = ""
		}
		if !visualText(plugin.InstalledVersion, 80) {
			plugin.InstalledVersion = ""
		}
		if !visualText(plugin.Source, 160) {
			plugin.Source = ""
		}
		if !visualText(plugin.Purpose, 240) {
			plugin.Purpose = ""
		}
		seen[plugin.Kind] = true
		out.Plugins = append(out.Plugins, plugin)
	}
	if out.Entity == nil && len(out.Plugins) == 0 && out.Placement == "" && !out.RequirementsChecked {
		return nil
	}
	return out
}

// FolderReceiptModel exposes only a bounded, path-free recorded model label.
// Omission must not make an otherwise successful setup fail or rewrite its model.
func FolderReceiptModel(value string) string {
	if !visualText(value, 128) {
		return ""
	}
	return value
}

func visualText(value string, maxRunes int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes &&
		!looksLikeFilesystemPath(value) && !strings.Contains(value, "../") &&
		!strings.HasPrefix(value, "./") && !strings.Contains(value, " ./") &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}
