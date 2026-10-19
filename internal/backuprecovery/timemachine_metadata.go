package backuprecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Time Machine metadata is read as data: destination and status answers from
// tmutil's two fixed read verbs, the preferences property list, and each
// source's exclusion attribute. Labels such as destination names are dropped
// here; only fixed states, identities and dates reach the private state.

const (
	maxTimeMachineDestinations = 8
	maxTimeMachineExclusions   = 256

	timeMachineIncluded = "included"
	timeMachineExcluded = "source_excluded"
	timeMachineUnknown  = "unknown"

	// timeMachineExcludeAttribute is the sticky per-item exclusion attribute.
	timeMachineExcludeAttribute = "com.apple.metadata:com_apple_backup_excludeItem"
)

type tmDestination struct {
	ID      string
	Network bool
	Mounted bool
	Current bool
}

type tmPreferenceDestination struct {
	completed []time.Time
	capacity  *localCapacity
}

type tmPreferences struct {
	destinations map[string]tmPreferenceDestination
	exclusions   []string
	// unresolved: an exclusion entry is not an absolute path (for example a
	// home-relative one), so no folder's inclusion can be decided from them.
	unresolved bool
}

// canonicalTimeMachineID lower-cases a Time Machine identity and accepts only
// the canonical UUID shape.
func canonicalTimeMachineID(value string) (string, bool) {
	id := strings.ToLower(value)
	return id, validStorageUUID(id)
}

// parseTimeMachineDestinations reads `tmutil destinationinfo -X`. Names and
// mount points are untrusted labels: only kind and mount presence survive.
func parseTimeMachineDestinations(raw []byte) ([]tmDestination, error) {
	root, err := decodeTimeMachinePlist(raw)
	if err != nil {
		return nil, err
	}
	list, present := root.fields["Destinations"]
	if !present {
		if len(root.fields) != 0 {
			return nil, errTimeMachinePlist
		}
		return []tmDestination{}, nil
	}
	if list.kind != "array" || len(list.items) > maxTimeMachineDestinations {
		return nil, errTimeMachinePlist
	}
	out := []tmDestination{}
	seen := map[string]bool{}
	for _, item := range list.items {
		if item.kind != "dict" {
			return nil, errTimeMachinePlist
		}
		idValue, kindValue := item.fields["ID"], item.fields["Kind"]
		id, ok := canonicalTimeMachineID(idValue.text)
		if idValue.kind != "string" || !ok || seen[id] || kindValue.kind != "string" || (kindValue.text != "Local" && kindValue.text != "Network") {
			return nil, errTimeMachinePlist
		}
		seen[id] = true
		mount, mounted := item.fields["MountPoint"]
		if mounted && (mount.kind != "string" || !strings.HasPrefix(mount.text, "/")) {
			return nil, errTimeMachinePlist
		}
		last := item.fields["LastDestination"]
		out = append(out, tmDestination{ID: id, Network: kindValue.text == "Network", Mounted: mounted, Current: last.kind == "integer" && last.text == "1"})
	}
	return out, nil
}

// parseTimeMachineStatus reads `tmutil status -X`. An answer without Percent
// is what a denied backupd connection produces, so it is unavailable.
func parseTimeMachineStatus(raw []byte) (running bool, err error) {
	root, err := decodeTimeMachinePlist(raw)
	if err != nil {
		return false, err
	}
	percent, ok := root.fields["Percent"]
	value := root.fields["Running"]
	if !ok || (percent.kind != "real" && percent.kind != "integer") || (value.kind != "true" && value.kind != "false") {
		return false, errTimeMachinePlist
	}
	return value.kind == "true", nil
}

// parseTimeMachinePreferences reads the preferences property list: completed
// backup dates and capacity per destination, and the exclusion lists.
func parseTimeMachinePreferences(raw []byte) (tmPreferences, error) {
	root, err := decodeTimeMachinePlist(raw)
	if err != nil {
		return tmPreferences{}, err
	}
	out := tmPreferences{destinations: map[string]tmPreferenceDestination{}}
	if list, present := root.fields["Destinations"]; present {
		if list.kind != "array" || len(list.items) > maxTimeMachineDestinations {
			return tmPreferences{}, errTimeMachinePlist
		}
		for _, item := range list.items {
			idValue := item.fields["DestinationID"]
			id, ok := canonicalTimeMachineID(idValue.text)
			if item.kind != "dict" || idValue.kind != "string" || !ok {
				return tmPreferences{}, errTimeMachinePlist
			}
			if _, duplicate := out.destinations[id]; duplicate {
				return tmPreferences{}, errTimeMachinePlist
			}
			destination := tmPreferenceDestination{}
			if dates, present := item.fields["SnapshotDates"]; present {
				if dates.kind != "array" || len(dates.items) > 4*maxSnapshotCount {
					return tmPreferences{}, errTimeMachinePlist
				}
				for _, value := range dates.items {
					at, err := time.Parse(time.RFC3339, value.text)
					if value.kind != "date" || err != nil {
						return tmPreferences{}, errTimeMachinePlist
					}
					destination.completed = append(destination.completed, at.UTC())
				}
			}
			available, availableOK := plistUint(item.fields["BytesAvailable"])
			used, usedOK := plistUint(item.fields["BytesUsed"])
			if availableOK && usedOK && available+used >= available && available+used > 0 {
				destination.capacity = &localCapacity{TotalBytes: available + used, AvailableBytes: available}
			}
			out.destinations[id] = destination
		}
	}
	for _, key := range []string{"SkipPaths", "ExcludeByPath"} {
		list, present := root.fields[key]
		if !present {
			continue
		}
		if list.kind != "array" || len(out.exclusions)+len(list.items) > maxTimeMachineExclusions {
			return tmPreferences{}, errTimeMachinePlist
		}
		for _, item := range list.items {
			if item.kind != "string" {
				return tmPreferences{}, errTimeMachinePlist
			}
			if !localPath(item.text) {
				out.unresolved = true
				continue
			}
			out.exclusions = append(out.exclusions, item.text)
		}
	}
	sort.Strings(out.exclusions)
	return out, nil
}

func plistUint(value diskPlistValue) (uint64, bool) {
	if value.kind != "integer" {
		return 0, false
	}
	n, err := strconv.ParseUint(value.text, 10, 64)
	return n, err == nil
}

// timeMachineExcludeItem reports whether an exclusion attribute value marks
// the item excluded: the binary-plist string Time Machine writes.
func timeMachineExcludeItem(value []byte) (excluded, ok bool) {
	decoded, err := decodeTimeMachinePlistString(value)
	if err != nil {
		return false, false
	}
	return decoded == "com.apple.backupd", true
}

// decodeTimeMachinePlistString decodes a small binary property list whose
// root is a string, as an exclusion attribute is.
func decodeTimeMachinePlistString(raw []byte) (string, error) {
	if len(raw) > 4096 {
		return "", errTimeMachinePlist
	}
	p, top, err := openBinaryPlist(raw, 16)
	if err != nil {
		return "", err
	}
	value, err := p.object(top, 0)
	if err != nil || value.kind != "string" {
		return "", errTimeMachinePlist
	}
	return value.text, nil
}

// timeMachineStickyState walks a source and its ancestors up to the root,
// reading only each one's exclusion attribute through attribute (which
// returns present=false when the attribute is absent). Paths never reach a
// child process.
func timeMachineStickyState(source string, attribute func(string) (value []byte, present bool, err error)) string {
	if !localPath(source) || attribute == nil {
		return timeMachineUnknown
	}
	for current := source; ; current = path.Dir(current) {
		value, present, err := attribute(current)
		if err != nil {
			return timeMachineUnknown
		}
		if present {
			excluded, ok := timeMachineExcludeItem(value)
			if !ok {
				return timeMachineUnknown
			}
			if excluded {
				return timeMachineExcluded
			}
		}
		if current == "/" {
			return timeMachineIncluded
		}
	}
}

// timeMachineFolderState combines a folder's sticky state with the
// preference exclusion lists: excluded when the folder or an ancestor is
// listed. Without readable preferences, an unexcluded folder is unknown.
func timeMachineFolderState(folder, sticky string, preferences *tmPreferences) string {
	if sticky == timeMachineExcluded {
		return timeMachineExcluded
	}
	if preferences == nil || sticky != timeMachineIncluded {
		return timeMachineUnknown
	}
	for _, excluded := range preferences.exclusions {
		if folder == excluded || strings.HasPrefix(folder, excluded+"/") {
			return timeMachineExcluded
		}
	}
	if preferences.unresolved {
		return timeMachineUnknown
	}
	return timeMachineIncluded
}

// timeMachineHelperResult is what the helper returns: the two verbs' raw
// answers, the preferences bytes or a Full Disk Access refusal, and each
// source's sticky state in configuration order. It carries no source path.
type timeMachineHelperResult struct {
	DestinationInfo   []byte   `json:"destination_info"`
	Status            []byte   `json:"status"`
	Preferences       []byte   `json:"preferences,omitempty"`
	PreferencesDenied bool     `json:"preferences_denied"`
	Sticky            []string `json:"sticky"`
}

// timeMachineReadResult maps one helper answer to an observation. Completed
// backups become completed snapshots with no declared paths; capacity comes
// only from the preferences; without Full Disk Access the read still records
// destinations and sticky exclusions but no completion or capacity.
func timeMachineReadResult(cfg localConfiguration, helper timeMachineHelperResult, observedAt time.Time) (providerReadResult, error) {
	if cfg.Provider != providerTimeMachine || len(helper.Sticky) != len(cfg.Sources) || (helper.PreferencesDenied && len(helper.Preferences) != 0) {
		return providerReadResult{}, errTimeMachinePlist
	}
	destinations, err := parseTimeMachineDestinations(helper.DestinationInfo)
	if err != nil {
		return providerReadResult{}, err
	}
	running, err := parseTimeMachineStatus(helper.Status)
	if err != nil {
		return providerReadResult{}, err
	}
	evidence := &localTimeMachine{PreferencesReadable: !helper.PreferencesDenied, Running: running, Destinations: []localTimeMachineDestination{}, Sources: make([]string, len(cfg.Sources))}
	for _, destination := range destinations {
		evidence.Destinations = append(evidence.Destinations, localTimeMachineDestination{ID: destination.ID, Network: destination.Network, Mounted: destination.Mounted})
	}
	result := providerReadResult{configuration: cfg, observedAt: observedAt.UTC(), snapshots: []localSnapshot{}, timeMachine: evidence}
	var preferences *tmPreferences
	if !helper.PreferencesDenied {
		parsed, err := parseTimeMachinePreferences(helper.Preferences)
		if err != nil {
			return providerReadResult{}, err
		}
		preferences = &parsed
		evidence.Exclusions = append([]string(nil), parsed.exclusions...)
		evidence.ExclusionsUnresolved = parsed.unresolved
		// Only destinations Time Machine still lists count; a removed
		// destination's history is not evidence for today.
		for _, destination := range destinations {
			record := parsed.destinations[destination.ID]
			for _, at := range record.completed {
				completed := at
				digest := sha256.Sum256([]byte("ori.time-machine.v1\x00" + destination.ID + "\x00" + at.Format(time.RFC3339)))
				result.snapshots = append(result.snapshots, localSnapshot{ID: hex.EncodeToString(digest[:]), StartedAt: at, CompletedAt: &completed, DeclaredPaths: []string{}})
			}
			if record.capacity != nil && (result.capacity == nil || destination.Current) {
				capacity := *record.capacity
				result.capacity = &capacity
			}
		}
		sort.Slice(result.snapshots, func(i, j int) bool { return result.snapshots[i].StartedAt.After(result.snapshots[j].StartedAt) })
		if len(result.snapshots) > maxSnapshotCount {
			result.snapshots = result.snapshots[:maxSnapshotCount]
		}
	}
	for i, source := range cfg.Sources {
		switch helper.Sticky[i] {
		case timeMachineIncluded, timeMachineExcluded, timeMachineUnknown:
		default:
			return providerReadResult{}, errTimeMachinePlist
		}
		evidence.Sources[i] = timeMachineFolderState(source.Path, helper.Sticky[i], preferences)
	}
	return result, nil
}
