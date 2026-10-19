package backuprecovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	localSchemaVersion   = 1
	maxPrivateStateBytes = 2 << 20
	maxSettingsBytes     = 128 << 10
	maxReceiptBytes      = 16 << 10
	maxAttemptHistory    = 100
	maxRestoreReports    = 20
	maxOperationOutcomes = 64
	maxSafeRevision      = 1<<53 - 1
)

var (
	errLocalState    = errors.New("backup local state is unavailable")
	errLocalConflict = errors.New("backup configuration changed; review it again")
	errLocalScope    = errors.New("backup workspace is unavailable")
)

// All of these types are private persistence DTOs, NOT HTTP or model payloads.
// Their exported fields exist solely for the authenticated encrypted envelope.
// Configuration intent, observed evidence, and access authority stay separate.
// No password, command, provider diagnostic, hostname, or freeform provider label
// has a field here. None of these fields alone authorizes a filesystem read.
type localState struct {
	Version       int                  `json:"version"`
	Generation    string               `json:"generation"`
	Revision      uint64               `json:"revision"`
	Sequence      uint64               `json:"sequence"`
	Configuration *localConfiguration  `json:"configuration,omitempty"`
	Monitoring    localMonitoring      `json:"monitoring"`
	Observation   *localObservation    `json:"observation,omitempty"`
	Attempts      []localAttempt       `json:"attempts,omitempty"`
	Reports       []localRestoreReport `json:"reports,omitempty"`
	Operations    []localOperation     `json:"operations,omitempty"`
}

type localConfiguration struct {
	Provider         string          `json:"provider"`
	Sources          []localSource   `json:"sources"`
	VolumePath       string          `json:"volume_path"`
	RepositoryPath   string          `json:"repository_path"`
	RepositoryID     string          `json:"repository_id,omitempty"`
	RepositoryFileID uint64          `json:"repository_file_id,omitempty"`
	Destination      *localStorage   `json:"destination,omitempty"`
	Credential       localCredential `json:"credential"`
}

type localSource struct {
	Path            string        `json:"path"`
	DirectoryFileID uint64        `json:"directory_file_id,omitempty"`
	Storage         *localStorage `json:"storage,omitempty"`
}

// Device numbers/current whole-disk mappings are deliberately not durable state.
// Native checks must additionally establish those transient facts for each read.
type localStorage struct {
	VolumeID    string   `json:"volume_id"`
	PartitionID string   `json:"partition_id"`
	StoreIDs    []string `json:"store_ids"`
}

type localCredential struct {
	VaultID  string `json:"vault_id"`
	RecordID string `json:"record_id"`
	Revision string `json:"revision"`
}

type localMonitoring struct {
	Intent     string           `json:"intent"` // manual_only, active, paused; NOT authority
	Revision   uint64           `json:"revision"`
	Policy     monitoringPolicy `json:"policy"`
	NextDueAt  *time.Time       `json:"next_due_at,omitempty"`
	LastSlotAt *time.Time       `json:"last_slot_at,omitempty"`
	RetryAt    *time.Time       `json:"retry_at,omitempty"`
	RetryUsed  bool             `json:"retry_used,omitempty"`
}

type monitoringPolicy struct {
	CadenceSeconds           int64 `json:"cadence_seconds"`
	ExpectedAgeSeconds       int64 `json:"expected_age_seconds"`
	FreshnessSeconds         int64 `json:"freshness_seconds"`
	DisconnectedGraceSeconds int64 `json:"disconnected_grace_seconds"`
	LowSpacePercent          int   `json:"low_space_percent"`
}

func proposedMonitoringPolicy() monitoringPolicy {
	return monitoringPolicy{3600, 48 * 3600, 3 * 3600, 24 * 3600, 10}
}

type localObservation struct {
	Generation  string            `json:"generation"`
	Sequence    uint64            `json:"sequence"`
	ObservedAt  time.Time         `json:"observed_at"`
	Snapshots   []localSnapshot   `json:"snapshots"` // empty is known empty, not failed
	Capacity    *localCapacity    `json:"capacity,omitempty"`
	TimeMachine *localTimeMachine `json:"time_machine,omitempty"`
}

// localTimeMachine is the private Time Machine part of one observation. With
// PreferencesReadable false (no Full Disk Access) the snapshots are unknown,
// not empty. Sources holds each configuration source's inclusion state in
// order. Exclusions are private paths, never projected.
type localTimeMachine struct {
	PreferencesReadable  bool                          `json:"preferences_readable"`
	Running              bool                          `json:"running"`
	Destinations         []localTimeMachineDestination `json:"destinations"`
	Exclusions           []string                      `json:"exclusions,omitempty"`
	ExclusionsUnresolved bool                          `json:"exclusions_unresolved,omitempty"`
	Sources              []string                      `json:"sources"`
}

type localTimeMachineDestination struct {
	ID      string `json:"id"`
	Network bool   `json:"network,omitempty"`
	Mounted bool   `json:"mounted"`
}

func (t localTimeMachine) valid(sources int, snapshots []localSnapshot) bool {
	if t.Destinations == nil || len(t.Destinations) > maxTimeMachineDestinations || len(t.Sources) != sources || len(t.Exclusions) > maxTimeMachineExclusions ||
		(!t.PreferencesReadable && (len(snapshots) != 0 || len(t.Exclusions) != 0 || t.ExclusionsUnresolved)) {
		return false
	}
	seen := map[string]bool{}
	for _, destination := range t.Destinations {
		if !validStorageUUID(destination.ID) || seen[destination.ID] {
			return false
		}
		seen[destination.ID] = true
	}
	for _, state := range t.Sources {
		if state != timeMachineIncluded && state != timeMachineExcluded && state != timeMachineUnknown {
			return false
		}
	}
	for _, exclusion := range t.Exclusions {
		if !localPath(exclusion) {
			return false
		}
	}
	return true
}

type localSnapshot struct {
	ID             string     `json:"id"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	DeclaredPaths  []string   `json:"declared_paths"`
	Exclusions     []string   `json:"exclusions,omitempty"`
	FilesProcessed *uint64    `json:"files_processed,omitempty"`
	BytesProcessed *uint64    `json:"bytes_processed,omitempty"`
}

type localCapacity struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

type localAttempt struct {
	Generation string    `json:"generation"`
	Sequence   uint64    `json:"sequence"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Outcome    string    `json:"outcome"` // fixed read outcome, never backup success
}

type localRestoreReport struct {
	ID         string    `json:"id"`
	Generation string    `json:"generation"`
	SnapshotID string    `json:"snapshot_id,omitempty"`
	Supersedes string    `json:"supersedes,omitempty"`
	ReportedAt time.Time `json:"reported_at"`
	TestedAt   time.Time `json:"tested_at"`
	Result     string    `json:"result"`         // user_reported_passed / user_reported_failed
	Note       string    `json:"note,omitempty"` // encrypted, excluded from summaries
}

type localOperation struct {
	ID               string    `json:"id"`
	Generation       string    `json:"generation"`
	RequestDigest    string    `json:"request_digest"`
	RemovalAuthority string    `json:"removal_authority,omitempty"`
	Action           string    `json:"action"`
	Status           string    `json:"status"` // pending, complete, interrupted
	Outcome          string    `json:"outcome,omitempty"`
	Revision         uint64    `json:"revision"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func (s localState) valid() bool {
	if s.Version != localSchemaVersion || !validStorageUUID(s.Generation) || s.Revision == 0 || s.Revision > maxSafeRevision || s.Sequence > maxSafeRevision || !s.Monitoring.valid() ||
		len(s.Attempts) > maxAttemptHistory || len(s.Reports) > maxRestoreReports || len(s.Operations) > maxOperationOutcomes {
		return false
	}
	if s.Configuration != nil && !s.Configuration.valid(false) {
		return false
	}
	if s.Observation != nil {
		o := s.Observation
		if !validStorageUUID(o.Generation) || o.Sequence == 0 || o.Sequence > s.Sequence || !localTime(o.ObservedAt) || o.Snapshots == nil || len(o.Snapshots) > maxSnapshotCount {
			return false
		}
		if o.TimeMachine != nil {
			sources := len(o.TimeMachine.Sources)
			if s.Configuration != nil && o.Generation == s.Generation {
				if s.Configuration.Provider != providerTimeMachine {
					return false
				}
				sources = len(s.Configuration.Sources)
			}
			if !o.TimeMachine.valid(sources, o.Snapshots) {
				return false
			}
		}
		seen := map[string]bool{}
		for _, snapshot := range o.Snapshots {
			// A Time Machine backup declares no paths; a restic snapshot must.
			declared := len(snapshot.DeclaredPaths) > 0
			if !metadataID(snapshot.ID) || seen[snapshot.ID] || !localTime(snapshot.StartedAt) || !optionalLocalTime(snapshot.CompletedAt) || declared == (o.TimeMachine != nil) || len(snapshot.DeclaredPaths) > maxDeclaredSources || len(snapshot.Exclusions) > maxDeclaredExcludes {
				return false
			}
			seen[snapshot.ID] = true
			paths := map[string]bool{}
			for _, value := range snapshot.DeclaredPaths {
				if !localPath(value) || paths[value] {
					return false
				}
				paths[value] = true
			}
			for _, value := range snapshot.Exclusions {
				if !localText(value, maxExcludeBytes, false) {
					return false
				}
			}
		}
		if o.Capacity != nil && (o.Capacity.TotalBytes == 0 || o.Capacity.AvailableBytes > o.Capacity.TotalBytes) {
			return false
		}
	}
	previous := s.Sequence + 1
	for _, attempt := range s.Attempts {
		if !validStorageUUID(attempt.Generation) || attempt.Sequence == 0 || attempt.Sequence >= previous || !localTime(attempt.StartedAt) || !localTime(attempt.FinishedAt) || !readOutcome(attempt.Outcome) {
			return false
		}
		previous = attempt.Sequence
	}
	seen := map[string]bool{}
	for _, report := range s.Reports {
		if report.Supersedes != "" && (!validStorageUUID(report.Supersedes) || report.Supersedes == report.ID) {
			return false
		}
		if !validStorageUUID(report.ID) || seen[report.ID] || !validStorageUUID(report.Generation) || (report.SnapshotID != "" && !metadataID(report.SnapshotID)) || !localTime(report.ReportedAt) || !localTime(report.TestedAt) || !localText(report.Note, 512, true) ||
			(report.Result != "user_reported_passed" && report.Result != "user_reported_failed") {
			return false
		}
		seen[report.ID] = true
	}
	seen = map[string]bool{}
	for _, operation := range s.Operations {
		if operation.Action == "uninstall" && !metadataID(operation.RemovalAuthority) || operation.Action != "uninstall" && operation.RemovalAuthority != "" {
			return false
		}
		if !validStorageUUID(operation.ID) || seen[operation.ID] || !validStorageUUID(operation.Generation) || !metadataID(operation.RequestDigest) || operation.Revision == 0 || operation.Revision > s.Revision || !localTime(operation.CreatedAt) || !localTime(operation.ExpiresAt) || !operation.ExpiresAt.After(operation.CreatedAt) || operation.ExpiresAt.Sub(operation.CreatedAt) > 24*time.Hour {
			return false
		}
		seen[operation.ID] = true
		switch operation.Action {
		case "connect", "refresh", "monitor", "monitoring", "revoke", "delete", "uninstall", "recovery_report":
		default:
			return false
		}
		switch operation.Status {
		case "pending":
			if operation.Outcome != "" {
				return false
			}
		case "complete", "interrupted":
			if operation.Outcome != "applied" && !readOutcome(operation.Outcome) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (c localConfiguration) valid(requireEvidence bool) bool {
	if c.Provider == providerTimeMachine {
		return c.validTimeMachine()
	}
	if c.Provider != providerRestic || len(c.Sources) == 0 || len(c.Sources) > maxSelectedSources || !localPath(c.VolumePath) || !localPath(c.RepositoryPath) ||
		(c.RepositoryPath != c.VolumePath && !strings.HasPrefix(c.RepositoryPath, c.VolumePath+"/")) || !localID(c.Credential.VaultID) || !localID(c.Credential.RecordID) || !metadataID(c.Credential.Revision) {
		return false
	}
	if c.RepositoryID != "" && !metadataID(c.RepositoryID) {
		return false
	}
	if c.Destination != nil && !c.Destination.valid() {
		return false
	}
	if requireEvidence && (c.RepositoryID == "" || c.RepositoryFileID == 0 || c.Destination == nil) {
		return false
	}
	seen := map[string]bool{}
	for _, source := range c.Sources {
		if !localPath(source.Path) || seen[source.Path] || source.Path == c.VolumePath || strings.HasPrefix(source.Path, c.VolumePath+"/") || strings.HasPrefix(c.RepositoryPath, source.Path+"/") {
			return false
		}
		seen[source.Path] = true
		if source.Storage != nil && !source.Storage.valid() {
			return false
		}
		if requireEvidence && (source.Storage == nil || source.DirectoryFileID == 0) {
			return false
		}
		if source.Storage != nil && c.Destination != nil {
			if source.Storage.VolumeID == c.Destination.VolumeID || source.Storage.PartitionID == c.Destination.PartitionID {
				return false
			}
			for _, a := range source.Storage.StoreIDs {
				for _, b := range c.Destination.StoreIDs {
					if a == b {
						return false
					}
				}
			}
		}
	}
	encoded, err := json.Marshal(c)
	return err == nil && len(encoded) <= maxSettingsBytes
}

// validTimeMachine: sources only. Time Machine owns its destination and keeps
// no repository, volume selection or credential Ori could hold; the
// destination identity is observation evidence, not configuration.
func (c localConfiguration) validTimeMachine() bool {
	if len(c.Sources) == 0 || len(c.Sources) > maxSelectedSources || c.VolumePath != "" || c.RepositoryPath != "" || c.RepositoryID != "" ||
		c.RepositoryFileID != 0 || c.Destination != nil || c.Credential != (localCredential{}) {
		return false
	}
	seen := map[string]bool{}
	for _, source := range c.Sources {
		if !localPath(source.Path) || seen[source.Path] || source.Storage != nil || source.DirectoryFileID != 0 {
			return false
		}
		seen[source.Path] = true
	}
	encoded, err := json.Marshal(c)
	return err == nil && len(encoded) <= maxSettingsBytes
}

func (s localStorage) valid() bool {
	if !validStorageUUID(s.VolumeID) || !validStorageUUID(s.PartitionID) || len(s.StoreIDs) == 0 || len(s.StoreIDs) > maxPhysicalStores {
		return false
	}
	previous := ""
	for _, id := range s.StoreIDs {
		if !validStorageUUID(id) || id <= previous {
			return false
		}
		previous = id
	}
	return true
}

func (m localMonitoring) valid() bool {
	if m.Revision > maxSafeRevision || !optionalLocalTime(m.NextDueAt) || !optionalLocalTime(m.LastSlotAt) || !optionalLocalTime(m.RetryAt) {
		return false
	}
	switch m.Intent {
	case "manual_only":
	case "active", "paused":
		if m.Revision == 0 {
			return false
		}
	default:
		return false
	}
	p := m.Policy
	switch p.CadenceSeconds {
	case 900, 3600, 21600, 86400:
	default:
		return false
	}
	return p.ExpectedAgeSeconds >= 3600 && p.ExpectedAgeSeconds <= 30*86400 && p.FreshnessSeconds >= p.CadenceSeconds && p.FreshnessSeconds <= 7*86400 && p.DisconnectedGraceSeconds >= 0 && p.DisconnectedGraceSeconds <= 30*86400 && p.LowSpacePercent >= 1 && p.LowSpacePercent <= 50
}

func readOutcome(value string) bool {
	switch value {
	case "read_ok", "read_unavailable", "destination_absent", "identity_mismatch", "credential_unavailable", "provider_unavailable", "limits_exceeded", "cancelled", "state_unavailable", "clock_suspect":
		return true
	default:
		return false
	}
}

func localTime(value time.Time) bool {
	_, offset := value.Zone()
	return !value.IsZero() && value.Year() > 0 && value.Year() <= 9999 && offset == 0
}
func optionalLocalTime(value *time.Time) bool { return value == nil || localTime(*value) }
func localText(value string, limit int, empty bool) bool {
	return (empty || value != "") && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, utf8.RuneError) && !strings.ContainsFunc(value, unicode.IsControl)
}
func localPath(value string) bool {
	return localText(value, maxSourcePathBytes, false) && value != "/" && path.IsAbs(value) && path.Clean(value) == value
}
func localID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// Private state is written only by this schema's canonical serializer, not an
// import/editor. Exact round-trip rejects unknown fields, case aliases, nulls in
// place of required values, and future representations without repairing them.
// The bounded token pass also rejects duplicate keys and invalid Unicode.
func decodeLocalJSON[T any](plain []byte, limit int) (T, error) {
	var result T
	if _, err := decodeMetadata(plain, nil, 0, limit); err != nil {
		return result, errLocalState
	}
	if err := json.Unmarshal(plain, &result); err != nil {
		return result, errLocalState
	}
	canonical, err := json.Marshal(result)
	if err != nil || !bytes.Equal(plain, canonical) {
		var zero T
		return zero, errLocalState
	}
	return result, nil
}
