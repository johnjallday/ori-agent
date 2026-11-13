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
