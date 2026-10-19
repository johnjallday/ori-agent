package backuprecovery

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func timeMachineFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "timemachine", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTimeMachinePreferencesDecodeFromTheBinaryPlist(t *testing.T) {
	preferences, err := parseTimeMachinePreferences(timeMachineFixture(t, "invented_preferences.bplist"))
	if err != nil {
		t.Fatal(err)
	}
	current := preferences.destinations["11111111-2222-4333-8444-555555555555"]
	if len(current.completed) != 3 || !current.completed[2].Equal(time.Date(2026, 10, 18, 19, 0, 0, 0, time.UTC)) {
		t.Fatal(current.completed)
	}
	if current.capacity == nil || current.capacity.AvailableBytes != 400e9 || current.capacity.TotalBytes != 1000e9 {
		t.Fatal(current.capacity)
	}
	if network := preferences.destinations["66666666-7777-4888-8999-000000000000"]; len(network.completed) != 0 || network.capacity != nil {
		t.Fatal(network)
	}
	if strings.Join(preferences.exclusions, ",") != "/Invented/Users/demo/Downloads,/Invented/Users/demo/Music/Scratch" || preferences.unresolved {
		t.Fatal(preferences.exclusions)
	}
	relative, err := parseTimeMachinePreferences(timeMachineFixture(t, "invented_preferences_relative_exclusion.bplist"))
	if err != nil || !relative.unresolved {
		t.Fatal("a home-relative exclusion must leave inclusion undecided", relative, err)
	}
	empty, err := parseTimeMachinePreferences(timeMachineFixture(t, "invented_preferences_no_destination.bplist"))
	if err != nil || len(empty.destinations) != 0 {
		t.Fatal(empty, err)
	}
}

func TestTimeMachineBinaryPlistRejectsMalformedInput(t *testing.T) {
	good := timeMachineFixture(t, "invented_preferences.bplist")
	corrupt := func(edit func([]byte)) []byte {
		raw := append([]byte(nil), good...)
		edit(raw)
		return raw
	}
	for name, raw := range map[string][]byte{
		"empty":            {},
		"short":            []byte("bplist00"),
		"bad header":       corrupt(func(b []byte) { b[7] = '1' }),
		"offset size zero": corrupt(func(b []byte) { b[len(b)-26] = 0 }),
		"top out of range": corrupt(func(b []byte) { b[len(b)-9] = 0xFF }),
		"table past end":   corrupt(func(b []byte) { b[len(b)-1] = 0xFF }),
		"truncated":        good[:len(good)-40],
		"oversized":        append(append([]byte("bplist00"), make([]byte, maxTimeMachinePlistBytes)...), good[len(good)-32:]...),
	} {
		if _, err := decodeTimeMachinePlist(raw); !errors.Is(err, errTimeMachinePlist) {
			t.Fatal(name, "was accepted")
		}
	}
	if _, err := decodeTimeMachinePlist(timeMachineFixture(t, "invented_exclude_item_attribute.bplist")); err == nil {
		t.Fatal("a string root is not a preferences dictionary")
	}
}

func TestTimeMachineVerbsKeepIdentityAndDropLabels(t *testing.T) {
	destinations, err := parseTimeMachineDestinations(timeMachineFixture(t, "invented_destinationinfo.plist"))
	if err != nil || len(destinations) != 2 {
		t.Fatal(destinations, err)
	}
	if local := destinations[0]; local.ID != "11111111-2222-4333-8444-555555555555" || local.Network || !local.Mounted || !local.Current {
		t.Fatal(local)
	}
	if network := destinations[1]; !network.Network || network.Mounted || network.Current {
		t.Fatal(network)
	}
	none, err := parseTimeMachineDestinations(timeMachineFixture(t, "destinationinfo_none.plist"))
	if err != nil || len(none) != 0 {
		t.Fatal(none, err)
	}
	if running, err := parseTimeMachineStatus(timeMachineFixture(t, "status_idle.plist")); err != nil || running {
		t.Fatal(running, err)
	}
	if running, err := parseTimeMachineStatus(timeMachineFixture(t, "invented_status_running.plist")); err != nil || !running {
		t.Fatal(running, err)
	}
	// What a denied backupd connection returns is not an idle answer.
	if _, err := parseTimeMachineStatus(timeMachineFixture(t, "status_ipc_denied.plist")); err == nil {
		t.Fatal("a status without Percent was trusted")
	}
}

func TestTimeMachineStickyStateWalksAncestorsInProcess(t *testing.T) {
	excluded := timeMachineFixture(t, "invented_exclude_item_attribute.bplist")
	attributes := map[string][]byte{}
	read := func(p string) ([]byte, bool, error) {
		value, ok := attributes[p]
		if p == "/Invented/Unreadable" {
			return nil, false, errors.New("PRIVATE")
		}
		return value, ok, nil
	}
	if state := timeMachineStickyState("/Invented/Music/Song", read); state != timeMachineIncluded {
		t.Fatal(state)
	}
	attributes["/Invented/Music"] = excluded
	if state := timeMachineStickyState("/Invented/Music/Song", read); state != timeMachineExcluded {
		t.Fatal("an excluded ancestor did not exclude the folder", state)
	}
	attributes["/Invented/Other"] = []byte("not a plist")
	if state := timeMachineStickyState("/Invented/Other/Song", read); state != timeMachineUnknown {
		t.Fatal(state)
	}
	if state := timeMachineStickyState("/Invented/Unreadable/Song", read); state != timeMachineUnknown {
		t.Fatal(state)
	}
	if state := timeMachineStickyState("relative/song", read); state != timeMachineUnknown {
		t.Fatal(state)
	}
}

func timeMachineConfigurationFixture(paths ...string) localConfiguration {
	cfg := localConfiguration{Provider: providerTimeMachine}
	for _, p := range paths {
		cfg.Sources = append(cfg.Sources, localSource{Path: p})
	}
	return cfg
}

func TestTimeMachineReadResultMapsBackupsCoverageAndDegradedMode(t *testing.T) {
	cfg := timeMachineConfigurationFixture("/Invented/Users/demo/Music/Songs", "/Invented/Users/demo/Music/Scratch/Take", "/Invented/Users/demo/Notes")
	observed := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	helper := timeMachineHelperResult{DestinationInfo: timeMachineFixture(t, "invented_destinationinfo.plist"), Status: timeMachineFixture(t, "status_idle.plist"),
		Preferences: timeMachineFixture(t, "invented_preferences.bplist"), Sticky: []string{timeMachineIncluded, timeMachineIncluded, timeMachineExcluded}}
	result, err := timeMachineReadResult(cfg, helper, observed)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.snapshots) != 3 || result.snapshots[0].CompletedAt == nil || !result.snapshots[0].CompletedAt.Equal(time.Date(2026, 10, 18, 19, 0, 0, 0, time.UTC)) || len(result.snapshots[0].DeclaredPaths) != 0 {
		t.Fatal("completed backups did not become completed snapshots without declared paths", result.snapshots)
	}
	if result.capacity == nil || result.capacity.AvailableBytes != 400e9 {
		t.Fatal(result.capacity)
	}
	evidence := result.timeMachine
	if !evidence.PreferencesReadable || len(evidence.Destinations) != 2 || strings.Join(evidence.Sources, ",") != "included,source_excluded,source_excluded" {
		t.Fatal(evidence)
	}
	state := localState{Version: 1, Generation: localUUID(100), Revision: 1, Sequence: 1, Configuration: &cfg, Monitoring: localMonitoring{Intent: "manual_only", Policy: proposedMonitoringPolicy()},
		Observation: &localObservation{Generation: localUUID(100), Sequence: 1, ObservedAt: observed, Snapshots: result.snapshots, Capacity: result.capacity, TimeMachine: evidence}}
	if !state.valid() {
		t.Fatal("a mapped Time Machine observation is not a valid stored state")
	}
	encoded, _ := json.Marshal(result.snapshots)
	if strings.Contains(string(encoded), "UNTRUSTED") || strings.Contains(string(encoded), "/Volumes") {
		t.Fatal("a Time Machine label or mount point reached the snapshots")
	}

	degraded := helper
	degraded.Preferences, degraded.PreferencesDenied = nil, true
	result, err = timeMachineReadResult(cfg, degraded, observed)
	if err != nil || len(result.snapshots) != 0 || result.capacity != nil || result.timeMachine.PreferencesReadable || strings.Join(result.timeMachine.Sources, ",") != "unknown,unknown,source_excluded" {
		t.Fatal("degraded mode claimed completion, capacity or inclusion", result, err)
	}

	for name, broken := range map[string]timeMachineHelperResult{
		"sticky count":         {DestinationInfo: helper.DestinationInfo, Status: helper.Status, Preferences: helper.Preferences, Sticky: []string{timeMachineIncluded}},
		"sticky state":         {DestinationInfo: helper.DestinationInfo, Status: helper.Status, Preferences: helper.Preferences, Sticky: []string{"covered", "covered", "covered"}},
		"denied status":        {DestinationInfo: helper.DestinationInfo, Status: timeMachineFixture(t, "status_ipc_denied.plist"), Preferences: helper.Preferences, Sticky: helper.Sticky},
		"denied with bytes":    {DestinationInfo: helper.DestinationInfo, Status: helper.Status, Preferences: helper.Preferences, PreferencesDenied: true, Sticky: helper.Sticky},
		"garbage destinations": {DestinationInfo: []byte("<plist>"), Status: helper.Status, Preferences: helper.Preferences, Sticky: helper.Sticky},
	} {
		if _, err := timeMachineReadResult(cfg, broken, observed); err == nil {
			t.Fatal(name, "was accepted")
		}
	}
}

func TestTimeMachineConfigurationHoldsSourcesOnly(t *testing.T) {
	cfg := timeMachineConfigurationFixture("/Invented/Songs")
	if !cfg.valid(false) || !cfg.valid(true) {
		t.Fatal("a sources-only Time Machine configuration was refused")
	}
	for name, mutate := range map[string]func(*localConfiguration){
		"credential": func(c *localConfiguration) {
			c.Credential = localCredential{"vault", "record", strings.Repeat("b", 64)}
		},
		"repository": func(c *localConfiguration) { c.RepositoryPath = "/Volumes/Invented/Repository" },
		"volume":     func(c *localConfiguration) { c.VolumePath = "/Volumes/Invented" },
		"destination": func(c *localConfiguration) {
			c.Destination = &localStorage{localUUID(4), localUUID(5), []string{localUUID(6)}}
		},
		"no sources": func(c *localConfiguration) { c.Sources = nil },
		"duplicate":  func(c *localConfiguration) { c.Sources = append(c.Sources, c.Sources[0]) },
		"relative":   func(c *localConfiguration) { c.Sources[0].Path = "Songs" },
	} {
		changed := timeMachineConfigurationFixture("/Invented/Songs")
		mutate(&changed)
		if changed.valid(false) {
			t.Fatal(name, "was accepted")
		}
	}
	// A restic snapshot still needs declared paths; a Time Machine one has none.
	restic := inventedLocalState()
	restic.Observation.Snapshots = []localSnapshot{{ID: strings.Repeat("a", 64), StartedAt: localFixtureTime, DeclaredPaths: []string{}}}
	if restic.valid() {
		t.Fatal("a restic snapshot without declared paths was accepted")
	}
}
