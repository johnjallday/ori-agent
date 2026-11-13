package projectlibrary

import (
	"sort"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// App-plugin readers (assistant-reads-projects). An app plugin may declare one
// read-only operation that reads project files for the formats it lists
// (plugin.ProjectFactsRef). The host calls only readers of reviewed plugins:
// there is no operating-system sandbox on the plugin process, so the reviewed
// list is the gate (FR21). projectReader's fields are unexported and the only
// way to one is projectReadersFrom, so no other path reaches a call.

// projectReader is one reviewed, enabled plugin's reader.
type projectReader struct {
	plugin plugin.InstalledPlugin
	// batch is how many files one call may carry.
	batch int
}

// projectReadersFrom are the readers the host may call, by format: installed
// plugins that are enabled, declare a reader, and are on the reviewed list.
// When two declare one format the first by plugin name reads it, so a song is
// read by one reader (FR39).
func projectReadersFrom(installed []plugin.InstalledPlugin) map[string]projectReader {
	sorted := append([]plugin.InstalledPlugin(nil), installed...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	readers := map[string]projectReader{}
	for _, candidate := range sorted {
		if !plugin.DeclaresProjectFacts(candidate) {
			continue
		}
		if _, reviewed := reviewedintegration.ForPlugin(candidate.Name); !reviewed {
			continue
		}
		batch := plugin.ProjectFactsBatchFiles(candidate)
		if batch < 1 {
			continue
		}
		for _, format := range plugin.ProjectFactsFormats(candidate) {
			if _, taken := readers[format]; !taken {
				readers[format] = projectReader{plugin: candidate, batch: batch}
			}
		}
	}
	return readers
}

// ReaderDisplayName is the name, as the reviewed list gives it, of the app
// plugin that would read songs for the installed plugins: the first reviewed
// plugin by name that declares a reader. Empty when none does. The approval's
// words use it, so a person is told which plugin reads their project files
// (assistant-reads-projects FR44, FR45).
func ReaderDisplayName(installed []plugin.InstalledPlugin) string {
	readers := projectReadersFrom(installed)
	best := ""
	for _, reader := range readers {
		if best == "" || reader.plugin.Name < best {
			best = reader.plugin.Name
		}
	}
	if best == "" {
		return ""
	}
	if entry, ok := reviewedintegration.ForPlugin(best); ok && entry.DisplayName != "" {
		return entry.DisplayName
	}
	return ""
}

// What ReaderStatus says about one project format.
const (
	// ReaderReady: a reviewed plugin's reader covers it.
	ReaderReady = "ready"
	// ReaderUpdatePlugin: the format's reviewed app plugin is installed and
	// enabled but has no reader for it; updating the plugin would add one (FR49).
	ReaderUpdatePlugin = "update_plugin"
	// ReaderNone: no installed plugin can read it.
	ReaderNone = "none"
)

// ReaderFor is ReaderStatus together with, for ReaderUpdatePlugin, the name of
// the plugin to update as the reviewed list gives it, so a sentence can say
// "Update <plugin> to read songs".
func ReaderFor(installed []plugin.InstalledPlugin, format string) (status, name string) {
	status = ReaderStatus(installed, format)
	if status != ReaderUpdatePlugin {
		return status, ""
	}
	key, ok := folderdigest.IntegrationKeyForProjectFormat(format)
	if !ok {
		return status, ""
	}
	entry, ok := reviewedintegration.Get(key)
	if !ok {
		return status, ""
	}
	return status, entry.DisplayName
}

// ReaderUpdateName is the name, as the reviewed list gives it, of an installed,
// enabled reviewed app plugin that declares no reader, so the page can say
// "Update <plugin> to read songs" where a Home's song details would be. It is
// empty when some installed plugin does read songs (nothing to update) and when
// no reviewed app plugin is installed. When several lack a reader, the first by
// plugin name.
func ReaderUpdateName(installed []plugin.InstalledPlugin) string {
	if len(projectReadersFrom(installed)) > 0 {
		return ""
	}
	sorted := append([]plugin.InstalledPlugin(nil), installed...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, candidate := range sorted {
		if !candidate.Enabled {
			continue
		}
		if entry, reviewed := reviewedintegration.ForPlugin(candidate.Name); reviewed && entry.DisplayName != "" {
			return entry.DisplayName
		}
	}
	return ""
}

// ReaderStatus says whether the songs of format can be read through an
// installed app plugin.
func ReaderStatus(installed []plugin.InstalledPlugin, format string) string {
	if _, ok := projectReadersFrom(installed)[format]; ok {
		return ReaderReady
	}
	key, ok := folderdigest.IntegrationKeyForProjectFormat(format)
	if !ok {
		return ReaderNone
	}
	entry, ok := reviewedintegration.Get(key)
	if !ok {
		return ReaderNone
	}
	for _, candidate := range installed {
		if candidate.Name == entry.PluginID && candidate.Enabled {
			return ReaderUpdatePlugin
		}
	}
	return ReaderNone
}
