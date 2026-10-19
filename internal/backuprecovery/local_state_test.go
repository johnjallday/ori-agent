package backuprecovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

var localFixtureTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func localUUID(n int) string { return fmt.Sprintf("%08x-1111-4111-8111-111111111111", n) }
func inventedLocalConfiguration() *localConfiguration {
	return &localConfiguration{Provider: "restic", Sources: []localSource{{Path: "/Invented/Project", DirectoryFileID: 10, Storage: &localStorage{localUUID(1), localUUID(2), []string{localUUID(3)}}}},
		VolumePath: "/Volumes/Invented", RepositoryPath: "/Volumes/Invented/Repository", RepositoryID: strings.Repeat("a", 64), RepositoryFileID: 20,
		Destination: &localStorage{localUUID(4), localUUID(5), []string{localUUID(6)}}, Credential: localCredential{"invented-vault", "invented-record", strings.Repeat("b", 64)}}
}
func inventedLocalState() localState {
	return localState{Version: 1, Generation: localUUID(100), Revision: 1, Sequence: 1, Configuration: inventedLocalConfiguration(),
		Monitoring:  localMonitoring{Intent: "manual_only", Policy: proposedMonitoringPolicy()},
		Observation: &localObservation{Generation: localUUID(100), Sequence: 1, ObservedAt: localFixtureTime, Snapshots: []localSnapshot{}},
		Attempts:    []localAttempt{{localUUID(100), 1, localFixtureTime, localFixtureTime, "read_ok"}},
		Reports:     []localRestoreReport{{ID: localUUID(101), Generation: localUUID(100), ReportedAt: localFixtureTime, TestedAt: localFixtureTime, Result: "user_reported_passed"}},
	}
}

func TestLocalBackupStateRejectsUnsafeOrExcessiveValues(t *testing.T) {
	cases := map[string]func(*localState){
		"future schema":               func(s *localState) { s.Version = 2 },
		"missing generation":          func(s *localState) { s.Generation = "" },
		"missing revision":            func(s *localState) { s.Revision = 0 },
		"unsafe integer revision":     func(s *localState) { s.Revision = maxSafeRevision + 1 },
		"unsafe sequence":             func(s *localState) { s.Sequence = maxSafeRevision + 1 },
		"no sources":                  func(s *localState) { s.Configuration.Sources = nil },
		"too many sources":            func(s *localState) { s.Configuration.Sources = make([]localSource, maxSelectedSources+1) },
		"root source":                 func(s *localState) { s.Configuration.Sources[0].Path = "/" },
		"traversing source":           func(s *localState) { s.Configuration.Sources[0].Path = "/Invented/../Other" },
		"overlong source":             func(s *localState) { s.Configuration.Sources[0].Path = "/" + strings.Repeat("x", 4096) },
		"source control":              func(s *localState) { s.Configuration.Sources[0].Path = "/Invented/\nProject" },
		"source invalid unicode":      func(s *localState) { s.Configuration.Sources[0].Path = "/Invented/\xff" },
		"repository outside volume":   func(s *localState) { s.Configuration.RepositoryPath = "/Other/Repository" },
		"source inside destination":   func(s *localState) { s.Configuration.Sources[0].Path = "/Volumes/Invented/Source" },
		"source contains destination": func(s *localState) { s.Configuration.Sources[0].Path = "/Volumes" },
		"shared volume": func(s *localState) {
			s.Configuration.Sources[0].Storage.VolumeID = s.Configuration.Destination.VolumeID
		},
		"shared physical store": func(s *localState) {
			s.Configuration.Sources[0].Storage.StoreIDs = s.Configuration.Destination.StoreIDs
		},
		"duplicate stores":                func(s *localState) { s.Configuration.Destination.StoreIDs = []string{localUUID(6), localUUID(6)} },
		"foreign credential key":          func(s *localState) { s.Configuration.Credential.RecordID = "../another" },
		"invalid credential revision":     func(s *localState) { s.Configuration.Credential.Revision = "old" },
		"provider executable":             func(s *localState) { s.Configuration.Provider = "/bin/sh" },
		"unknown cadence":                 func(s *localState) { s.Monitoring.Policy.CadenceSeconds = 60 },
		"freshness shorter than cadence":  func(s *localState) { s.Monitoring.Policy.FreshnessSeconds = 900 },
		"unbounded freshness":             func(s *localState) { s.Monitoring.Policy.FreshnessSeconds = 7*86400 + 1 },
		"unbounded expected age":          func(s *localState) { s.Monitoring.Policy.ExpectedAgeSeconds = 31 * 86400 },
		"unbounded disconnect grace":      func(s *localState) { s.Monitoring.Policy.DisconnectedGraceSeconds = 31 * 86400 },
		"invalid space threshold":         func(s *localState) { s.Monitoring.Policy.LowSpacePercent = 0 },
		"active without consent revision": func(s *localState) { s.Monitoring.Intent = "active" },
		"nil snapshot list":               func(s *localState) { s.Observation.Snapshots = nil },
		"too many snapshots":              func(s *localState) { s.Observation.Snapshots = make([]localSnapshot, 257) },
		"invalid capacity":                func(s *localState) { s.Observation.Capacity = &localCapacity{100, 101} },
		"missing observation time":        func(s *localState) { s.Observation.ObservedAt = time.Time{} },
		"non UTC time":                    func(s *localState) { s.Observation.ObservedAt = localFixtureTime.In(time.FixedZone("invented", 3600)) },
		"unknown outcome":                 func(s *localState) { s.Attempts[0].Outcome = "backup_succeeded" },
		"future sequence":                 func(s *localState) { s.Attempts[0].Sequence = 2 },
		"oversized attempt history":       func(s *localState) { s.Attempts = make([]localAttempt, 101) },
		"oversized reports":               func(s *localState) { s.Reports = make([]localRestoreReport, 21) },
		"oversized outcomes":              func(s *localState) { s.Operations = make([]localOperation, 65) },
		"oversized note":                  func(s *localState) { s.Reports[0].Note = strings.Repeat("x", 513) },
		"provider verified forgery":       func(s *localState) { s.Reports[0].Result = "ori_verified" },
	}
	if !inventedLocalState().valid() {
		t.Fatal("invalid fixture")
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			state := inventedLocalState()
			change(&state)
			if state.valid() {
				t.Fatal("unsafe state accepted")
			}
		})
	}
}

func TestLocalBackupStateCanonicalSchemaAndByteBounds(t *testing.T) {
	state := inventedLocalState()
	state.Reports[0].Note = strings.Repeat("é", 256) // bytes, not character count
	state.Configuration.Sources[0].Path = "/" + strings.Repeat("x", 4095)
	if !state.valid() {
		t.Fatal("exact text limits rejected")
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeLocalJSON[localState](encoded, maxPrivateStateBytes)
	if err != nil || !decoded.valid() {
		t.Fatal("canonical round trip failed", err)
	}
	for name, body := range map[string][]byte{
		"unknown command": bytes.Replace(encoded, []byte(`"version":1`), []byte(`"version":1,"command":"restic restore"`), 1),
		"case alias":      bytes.Replace(encoded, []byte(`"version"`), []byte(`"Version"`), 1),
		"duplicate key":   bytes.Replace(encoded, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"trailing value":  append(append([]byte(nil), encoded...), []byte(`{}`)...),
		"null revision":   bytes.Replace(encoded, []byte(`"revision":1`), []byte(`"revision":null`), 1),
		"excessive bytes": bytes.Repeat([]byte(" "), maxPrivateStateBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeLocalJSON[localState](body, maxPrivateStateBytes); err != errLocalState {
				t.Fatal("unsafe private JSON accepted")
			}
		})
	}
	// Escaped JSON expansion counts against settings bytes, not only path length.
	state = inventedLocalState()
	for i := range 16 {
		source := state.Configuration.Sources[0]
		source.Path = fmt.Sprintf("/%02d", i) + strings.Repeat("<", 4000)
		state.Configuration.Sources = append(state.Configuration.Sources, source)
	}
	state.Configuration.Sources = state.Configuration.Sources[1:]
	if state.Configuration.valid(false) {
		t.Fatal("oversized encoded settings accepted")
	}
}

func TestLocalBackupStatePreservesUnknownCountsAndPublishedOnlyEvidence(t *testing.T) {
	state := inventedLocalState()
	zero, large := uint64(0), uint64(math.MaxUint64)
	state.Observation.Snapshots = []localSnapshot{{ID: strings.Repeat("c", 64), StartedAt: localFixtureTime, DeclaredPaths: []string{"/Invented/Project"}, FilesProcessed: &zero, BytesProcessed: &large}}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeLocalJSON[localState](encoded, maxPrivateStateBytes)
	if err != nil || !decoded.valid() {
		t.Fatal(err)
	}
	snapshot := decoded.Observation.Snapshots[0]
	if snapshot.CompletedAt != nil || snapshot.FilesProcessed == nil || *snapshot.FilesProcessed != 0 || *snapshot.BytesProcessed != math.MaxUint64 {
		t.Fatal("unknown completion or exact counts were changed")
	}
	if decoded.Observation.Capacity != nil {
		t.Fatal("unknown capacity became zero")
	}
	// Intent with an enabled flag is serializable but cannot be a receipt.
	state.Monitoring.Intent = "active"
	state.Monitoring.Revision = 1
	if !state.valid() || validLocalReceipt(localRecord{state: &state}) {
		t.Fatal("monitor intent became access authority")
	}
}
