package externalmemory

import (
	"path/filepath"
	"sort"
	"strings"
)

// liveEntries returns the active entries outside the exclusions. It is the
// set every reader sees for an enabled source; a disabled source contributes
// nothing, without a sync (FR17).
func liveEntries(entries []Entry, exclusions []string, home string) []Entry {
	prefixes := normalizeExclusions(exclusions, home)
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Active() && !excluded(entry.Folder, prefixes) {
			out = append(out, entry)
		}
	}
	return out
}

// LiveEntries returns a source's active entries outside its exclusions,
// whether or not the source is enabled. The Profile card counts these; readers
// use VisibleEntries.
func LiveEntries(store *Store, source, home string) []Entry {
	doc := store.LoadSources()
	return liveEntries(store.LoadEntries(source), doc.Source(source).Exclusions, home)
}

// VisibleEntries returns every entry a reader may see: active, from an
// enabled source, outside that source's exclusions, in SourceIDs order.
func VisibleEntries(store *Store, home string) []Entry {
	doc := store.LoadSources()
	var out []Entry
	for _, id := range SourceIDs {
		state := doc.Source(id)
		if !state.Enabled {
			continue
		}
		out = append(out, liveEntries(store.LoadEntries(id), state.Exclusions, home)...)
	}
	return out
}

// FolderCount is how many entries apply to one folder.
type FolderCount struct {
	Folder  string `json:"folder"`
	Display string `json:"display"`
	Count   int    `json:"count"`
}

// FolderCounts groups entries by folder, most entries first. Entries with no
// folder are left out: they are about the user in general.
func FolderCounts(entries []Entry, home string) []FolderCount {
	counts := map[string]int{}
	for _, entry := range entries {
		if entry.Folder != "" {
			counts[entry.Folder]++
		}
	}
	out := make([]FolderCount, 0, len(counts))
	for folder, n := range counts {
		out = append(out, FolderCount{Folder: folder, Display: ShortFolder(folder, home), Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Folder < out[j].Folder
	})
	return out
}

// ShortFolder shows a folder under home as ~/… (the Profile browser's short
// form). Other folders are returned as they are.
func ShortFolder(folder, home string) string {
	if folder == "" || home == "" {
		return folder
	}
	home = filepath.Clean(home)
	if folder == home {
		return "~"
	}
	if strings.HasPrefix(folder, home+string(filepath.Separator)) {
		return "~" + folder[len(home):]
	}
	return folder
}
