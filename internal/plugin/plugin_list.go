package plugin

// Plugins.json is the Workspace Directory's list of the plugins the user wants,
// at which exact version, from which source. It travels with the Workspace
// Directory; the plugins themselves do not. Another machine compares the list
// with what it has installed and offers each difference as a reviewed,
// one-click change. The list is untrusted input: nothing is ever installed,
// updated, or uninstalled from it without the user's click.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PluginListFileName is the plugin list at the top of the Workspace Directory.
const PluginListFileName = "Plugins.json"

const pluginListSchemaVersion = 1

// PluginList is the content of Plugins.json.
type PluginList struct {
	SchemaVersion int                  `json:"schema_version"`
	Plugins       []PluginListEntry    `json:"plugins"`
	Removed       []RemovedPluginEntry `json:"removed"`
}

// PluginListEntry is one plugin the user wants.
type PluginListEntry struct {
	Name      string       `json:"name"`
	Version   string       `json:"version,omitempty"`
	Source    string       `json:"source"`
	Format    SourceFormat `json:"format,omitempty"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// RemovedPluginEntry is a plugin the user uninstalled; other machines are
// offered its uninstall.
type RemovedPluginEntry struct {
	Name      string    `json:"name"`
	RemovedAt time.Time `json:"removed_at"`
}

// PluginListPath is <root>/Plugins.json.
func PluginListPath(root string) string {
	return filepath.Join(root, PluginListFileName)
}

// ReadPluginList reads a Workspace Directory's plugin list. A missing file is
// an empty list with found=false. A file that cannot be read or parsed is an
// error that names the reason, and nothing about it is guessed.
func ReadPluginList(root string) (list PluginList, found bool, err error) {
	data, err := os.ReadFile(PluginListPath(root)) // #nosec G304 -- the fixed list file at the top of the Workspace Directory
	if errors.Is(err, os.ErrNotExist) {
		return PluginList{SchemaVersion: pluginListSchemaVersion}, false, nil
	}
	if err != nil {
		return PluginList{}, true, err
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return PluginList{}, true, fmt.Errorf("it is not valid JSON (%v)", err)
	}
	if list.SchemaVersion > pluginListSchemaVersion {
		return PluginList{}, true, fmt.Errorf("it was written by a newer version of Ori (schema %d)", list.SchemaVersion)
	}
	return list, true, nil
}

// WritePluginList writes the list in one step (a temp file renamed into
// place), and only when its bytes change, so a sync service never sees a
// rewrite that changed nothing. It reports whether it wrote.
func WritePluginList(root string, list PluginList) (bool, error) {
	list.SchemaVersion = pluginListSchemaVersion
	if list.Plugins == nil {
		list.Plugins = []PluginListEntry{}
	}
	if list.Removed == nil {
		list.Removed = []RemovedPluginEntry{}
	}
	sort.Slice(list.Plugins, func(i, j int) bool { return list.Plugins[i].Name < list.Plugins[j].Name })
	sort.Slice(list.Removed, func(i, j int) bool { return list.Removed[i].Name < list.Removed[j].Name })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	path := PluginListPath(root)
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) { // #nosec G304 -- see ReadPluginList
		return false, nil
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return false, err
	}
	temp, err := os.CreateTemp(root, ".Plugins-*.json.tmp")
	if err != nil {
		return false, err
	}
	tempPath := temp.Name()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return false, err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return false, err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return false, err
	}
	return true, nil
}

// entryFor is the list entry that records an installed plugin.
func entryFor(record InstalledPlugin, now time.Time) PluginListEntry {
	return PluginListEntry{Name: record.Name, Version: record.Version, Source: record.Source, Format: record.Format, UpdatedAt: now.UTC()}
}

// sameEntry reports whether two entries name the same plugin version.
func sameEntry(a, b PluginListEntry) bool {
	return a.Name == b.Name && a.Version == b.Version && a.Source == b.Source && a.Format == b.Format
}

// RecordInstalled adds or replaces an installed or updated plugin's entry and
// drops any removed entry with its name. An unchanged entry keeps its
// updated_at, so a no-op update changes nothing.
func (list *PluginList) RecordInstalled(record InstalledPlugin, now time.Time) {
	entry := entryFor(record, now)
	replaced := false
	for index := range list.Plugins {
		if list.Plugins[index].Name != record.Name {
			continue
		}
		if !sameEntry(list.Plugins[index], entry) {
			list.Plugins[index] = entry
		}
		replaced = true
	}
	if !replaced {
		list.Plugins = append(list.Plugins, entry)
	}
	kept := list.Removed[:0]
	for _, removed := range list.Removed {
		if removed.Name != record.Name {
			kept = append(kept, removed)
		}
	}
	list.Removed = kept
}

// RecordUninstalled moves a plugin from plugins to removed.
func (list *PluginList) RecordUninstalled(name string, now time.Time) {
	kept := list.Plugins[:0]
	for _, entry := range list.Plugins {
		if entry.Name != name {
			kept = append(kept, entry)
		}
	}
	list.Plugins = kept
	for index := range list.Removed {
		if list.Removed[index].Name == name {
			list.Removed[index].RemovedAt = now.UTC()
			return
		}
	}
	list.Removed = append(list.Removed, RemovedPluginEntry{Name: name, RemovedAt: now.UTC()})
}

// AddUnlisted adds every installed plugin that appears in neither list. It is
// how the list is first filled from the plugins a machine already has. It
// reports whether anything was added.
func (list *PluginList) AddUnlisted(installed []InstalledPlugin, now time.Time) bool {
	listed := map[string]bool{}
	for _, entry := range list.Plugins {
		listed[entry.Name] = true
	}
	for _, removed := range list.Removed {
		listed[removed.Name] = true
	}
	added := false
	for _, record := range installed {
		if listed[record.Name] || strings.TrimSpace(record.Source) == "" {
			continue
		}
		list.Plugins = append(list.Plugins, entryFor(record, now))
		listed[record.Name] = true
		added = true
	}
	return added
}

// PendingKind is what a pending change would do on this machine.
type PendingKind string

const (
	PendingInstall   PendingKind = "install"
	PendingSwitch    PendingKind = "switch"
	PendingUninstall PendingKind = "uninstall"
)

// PendingChange is one difference between the plugin list and this machine.
type PendingChange struct {
	Kind             PendingKind  `json:"kind"`
	Name             string       `json:"name"`
	ListedVersion    string       `json:"listed_version,omitempty"`
	InstalledVersion string       `json:"installed_version,omitempty"`
	Source           string       `json:"source,omitempty"`
	Format           SourceFormat `json:"format,omitempty"`
	// Direction labels a switch: "update" when the listed version is newer,
	// "change" otherwise.
	Direction   string `json:"direction,omitempty"`
	Installable bool   `json:"installable"`
	Reason      string `json:"reason,omitempty"`
	// Fingerprint identifies the list entry the change came from, so a skip
	// stops applying once that entry changes.
	Fingerprint string `json:"fingerprint"`
}

// LocalFolderNotHereReason is shown for an entry installed from a local
// folder this machine does not have.
const LocalFolderNotHereReason = "Can't install on this Mac: it was installed from a local folder."

// Pending compares the plugin list with this machine's installed plugins:
//
//	listed, not installed here             install
//	listed, installed at another commit    switch (update or change)
//	removed, still installed here          uninstall
//	installed here, in neither list        nothing (added automatically)
func Pending(list PluginList, installed []InstalledPlugin) []PendingChange {
	byName := make(map[string]InstalledPlugin, len(installed))
	for _, record := range installed {
		byName[record.Name] = record
	}
	listed := map[string]bool{}
	changes := []PendingChange{}
	for _, entry := range list.Plugins {
		if strings.TrimSpace(entry.Name) == "" || listed[entry.Name] {
			continue
		}
		listed[entry.Name] = true
		change := PendingChange{
			Name: entry.Name, ListedVersion: entry.Version, Source: entry.Source, Format: entry.Format,
			Fingerprint: EntryFingerprint(entry),
		}
		change.Installable, change.Reason = installableHere(entry.Source)
		record, isInstalled := byName[entry.Name]
		switch {
		case !isInstalled:
			change.Kind = PendingInstall
		case !sameInstalledVersion(entry, record):
			change.Kind = PendingSwitch
			change.InstalledVersion = record.Version
			change.Direction = "change"
			if order, ok := compareVersions(entry.Version, record.Version); ok && order > 0 {
				change.Direction = "update"
			}
		default:
			continue
		}
		changes = append(changes, change)
	}
	for _, removed := range list.Removed {
		if listed[removed.Name] {
			continue
		}
		record, isInstalled := byName[removed.Name]
		if !isInstalled {
			continue
		}
		changes = append(changes, PendingChange{
			Kind: PendingUninstall, Name: removed.Name, InstalledVersion: record.Version, Installable: true,
			Fingerprint: removedFingerprint(removed),
		})
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return changes
}

// EntryFingerprint identifies a list entry by its source and version.
func EntryFingerprint(entry PluginListEntry) string {
	return fingerprintOf("plugin", entry.Name, entry.Source, entry.Version)
}

func removedFingerprint(removed RemovedPluginEntry) string {
	return fingerprintOf("removed", removed.Name, removed.RemovedAt.UTC().Format(time.RFC3339Nano))
}

func fingerprintOf(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

// sameInstalledVersion compares the pinned commits of two sources when either
// has one, and the sources themselves otherwise.
func sameInstalledVersion(entry PluginListEntry, record InstalledPlugin) bool {
	listedCommit, installedCommit := commitOf(entry.Source), commitOf(record.Source)
	if listedCommit != "" || installedCommit != "" {
		return strings.EqualFold(listedCommit, installedCommit) && repositoryOf(entry.Source) == repositoryOf(record.Source)
	}
	return strings.TrimSpace(entry.Source) == strings.TrimSpace(record.Source) && entry.Version == record.Version
}

// commitOf returns the #sha= commit of a source, or "".
func commitOf(source string) string {
	if parsed, ok := parseGitSubdir(strings.TrimSpace(source)); ok {
		return parsed.Sha
	}
	return ""
}

// repositoryOf returns a source without its #fragment.
func repositoryOf(source string) string {
	base, _, _ := strings.Cut(strings.TrimSpace(source), "#")
	return strings.TrimSuffix(base, ".git")
}

// installableHere reports whether this machine can fetch a source. A git
// source can always be fetched (the review step reports a network failure);
// a local folder must exist here.
func installableHere(source string) (bool, string) {
	source = strings.TrimSpace(source)
	if source == "" {
		return false, "The plugin list does not say where this plugin comes from."
	}
	if _, ok := parseGitSubdir(source); ok || isGitURL(source) {
		return true, ""
	}
	if info, err := os.Stat(source); err == nil && info.IsDir() {
		return true, ""
	}
	return false, LocalFolderNotHereReason
}

// compareVersions orders dotted numeric versions ("1.2.10" > "1.2.9"), with
// a leading "v" and any prerelease or build suffix ignored. ok is false when
// either is not such a version.
func compareVersions(left, right string) (int, bool) {
	parse := func(value string) ([]int, bool) {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		value = strings.SplitN(strings.SplitN(value, "+", 2)[0], "-", 2)[0]
		if value == "" {
			return nil, false
		}
		var numbers []int
		for _, part := range strings.Split(value, ".") {
			number, err := strconv.Atoi(part)
			if err != nil {
				return nil, false
			}
			numbers = append(numbers, number)
		}
		return numbers, true
	}
	a, okA := parse(left)
	b, okB := parse(right)
	if !okA || !okB {
		return 0, false
	}
	for index := 0; index < len(a) || index < len(b); index++ {
		var x, y int
		if index < len(a) {
			x = a[index]
		}
		if index < len(b) {
			y = b[index]
		}
		if x != y {
			if x > y {
				return 1, true
			}
			return -1, true
		}
	}
	return 0, true
}
