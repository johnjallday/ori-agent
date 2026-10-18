package backuprecovery

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

var (
	ErrWorkspaceUnavailable  = errLocalScope
	ErrConfigurationChanged  = errLocalConflict
	ErrUnavailable           = errLocalState
	ErrInvalidRequest        = errors.New("backup request is invalid")
	ErrReviewExpired         = errors.New("backup review expired; review the selection again")
	ErrBusy                  = errors.New("a backup metadata check is already in progress")
	ErrRemovalPending        = errors.New("backup capability removal is pending")
	ErrLocalReviewRequired   = errors.New("backup metadata access requires local review")
	ErrSelectionCancelled    = errors.New("folder selection was cancelled")
	ErrCredentialUnavailable = errCredentialUnavailable
)

// Host dependencies, never fields accepted from JSON/model text. The selected
// Vault API is deliberately the pre-decryption workspace-scoped seam only.
type BackupCredentials interface {
	DescribeBackupSecret(context.Context, vault.BackupSecretReference, vault.BackupSecretAuthorization) (vault.BackupSecretReference, error)
	UseBackupSecret(context.Context, vault.BackupSecretReference, vault.BackupSecretAuthorization, func([]byte) error) error
}
type FolderPicker interface {
	Available() bool
	Choose(context.Context, string) (string, bool, error)
}
type ServiceOptions struct {
	Database       *database.DB
	Secrets        vault.SecretStore
	DataDir        string
	WorkGate       *resetstate.WorkGate
	Credentials    BackupCredentials
	Selections     *pathselection.Store
	Picker         FolderPicker
	ExecutablePath string // operator/server composition only; never workspace data
}
type CredentialReference struct {
	VaultID  string `json:"vault_id"`
	RecordID string `json:"record_id"`
	Revision string `json:"record_revision"`
}
type StateVersion struct {
	Generation string `json:"generation"`
	Revision   uint64 `json:"revision"`
	Sequence   uint64 `json:"sequence"`
}
type CapacityEvidence struct {
	TotalBytes     uint64    `json:"total_bytes"`
	AvailableBytes uint64    `json:"available_bytes"`
	ObservedAt     time.Time `json:"observed_at"`
}
type CoverageEvidence struct {
	DeclaredPathCount int    `json:"declared_path_count"`
	ExclusionCount    int    `json:"exclusion_count"`
	Kind              string `json:"kind"`
}
type Status struct {
	StateVersion
	WorkspaceID         string            `json:"workspace_id"`
	ServerTime          time.Time         `json:"server_time"`
	Connection          string            `json:"connection"`
	RuntimeAvailable    bool              `json:"runtime_available"`
	Destination         string            `json:"destination"`
	Monitoring          string            `json:"monitoring"`
	InProgress          bool              `json:"in_progress"`
	LastAttempt         *Attempt          `json:"last_attempt,omitempty"`
	LastSuccessfulRead  *time.Time        `json:"last_successful_read,omitempty"`
	SnapshotCount       *int              `json:"snapshot_count,omitempty"`
	LastCompletedAt     *time.Time        `json:"last_completed_at,omitempty"`
	EvidenceCurrent     bool              `json:"evidence_current_generation"`
	RemovalPending      bool              `json:"removal_pending"`
	IntendedSourceCount int               `json:"intended_source_count"`
	Capacity            *CapacityEvidence `json:"capacity,omitempty"`
	Coverage            *CoverageEvidence `json:"coverage,omitempty"`
	Health              EvidenceHealth    `json:"health"`
}
type Attempt struct {
	Generation string    `json:"generation"`
	Sequence   uint64    `json:"sequence"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Outcome    string    `json:"outcome"`
}
type Settings struct {
	StateVersion
	Configured      bool                 `json:"configured"`
	SourcePaths     []string             `json:"source_paths"`
	VolumePath      string               `json:"volume_path,omitempty"`
	RepositoryPath  string               `json:"repository_path,omitempty"`
	Credential      *CredentialReference `json:"credential,omitempty"`
	PickerAvailable bool                 `json:"picker_available"`
	Monitoring      MonitoringSettings   `json:"monitoring"`
}
type ReviewRequest struct {
	Generation          string              `json:"generation"`
	Revision            uint64              `json:"revision"`
	SourceSelections    []string            `json:"source_selections"`
	VolumeSelection     string              `json:"volume_selection"`
	RepositorySelection string              `json:"repository_selection"`
	UseSavedSelection   bool                `json:"use_saved_selection"`
	Credential          CredentialReference `json:"credential"`
}
type Review struct {
	Generation        string              `json:"generation"`
	Revision          uint64              `json:"revision"`
	Token             string              `json:"review_token"`
	ExpiresAt         time.Time           `json:"expires_at"`
	SourcePaths       []string            `json:"source_paths"`
	VolumePath        string              `json:"volume_path"`
	RepositoryPath    string              `json:"repository_path"`
	Credential        CredentialReference `json:"credential"`
	MonitoringEnabled bool                `json:"monitoring_enabled"` // always false: separate consent
}
type pendingReview struct {
	expected           localRevision
	configuration      localConfiguration
	monitoring         *monitoringPolicy
	monitoringRevision uint64
	expires            time.Time
}
type workspaceOperationSlot struct {
	serial chan struct{}
	active *activeMetadataOperation
}

// Service owns reviewed one-read permits, single-flight work and stored-only
// projections. Constructing/reading it grants nothing and schedules no checks.
type Service struct {
	store            *localStore
	work             *resetstate.WorkGate
	credentials      BackupCredentials
	selections       *pathselection.Store
	picker           FolderPicker
	provider         localMetadataProvider
	clock            func() time.Time
	ctx              context.Context
	cancel           context.CancelFunc
	releaseLifetime  func()
	mu               sync.Mutex
	closing, stopped bool
	reviews          map[string]pendingReview
	slots            map[string]*workspaceOperationSlot
	capacity         chan struct{}
	operations       sync.WaitGroup
	closeDone        chan struct{}
	monitoringBound  bool
	monitorCursor    string
	monitorJobs      chan struct{}
}

func NewService(options ServiceOptions) (*Service, error) { return newService(options, nil, time.Now) }
func newService(options ServiceOptions, provider localMetadataProvider, clock func() time.Time) (*Service, error) {
	if options.WorkGate == nil || !options.WorkGate.Snapshot().Known || options.WorkGate.Snapshot().Fenced || clock == nil {
		return nil, ErrUnavailable
	}
	releaseConstruction, err := options.WorkGate.Enter()
	if err != nil {
		return nil, ErrUnavailable
	}
	defer releaseConstruction()
	store, err := newLocalStore(options.Database, options.Secrets, options.DataDir, options.WorkGate)
	if err != nil {
		return nil, err
	}
	release, err := options.WorkGate.EnterLifetime()
	if err != nil {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{store: store, work: options.WorkGate, credentials: options.Credentials, selections: options.Selections, picker: options.Picker, provider: provider, clock: clock, ctx: ctx, cancel: cancel, releaseLifetime: release, reviews: map[string]pendingReview{}, slots: map[string]*workspaceOperationSlot{}, capacity: make(chan struct{}, 2), closeDone: make(chan struct{}), monitorJobs: make(chan struct{}, 2)}
	if service.provider == nil && options.ExecutablePath != "" {
		constructionCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		service.provider, _ = newPlatformProvider(constructionCtx, options.ExecutablePath, options.DataDir)
		stop() // missing/unsupported tool leaves stored setup usable, never ready
	}
	return service, nil
}

func (s *Service) enter() (func(), error) {
	if s == nil || s.work == nil {
		return nil, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, ErrUnavailable
	}
	release, err := s.work.Enter()
	if err != nil {
		return nil, ErrUnavailable
	}
	s.operations.Add(1)
	var once sync.Once
	return func() { once.Do(func() { release(); s.operations.Done() }) }, nil
}
func (s *Service) record(ctx context.Context, owner, workspace string) (localRecord, error) {
	if s == nil || owner != "local" || !localID(workspace) {
		return localRecord{}, ErrWorkspaceUnavailable
	}
	return s.store.load(ctx, owner, workspace)
}
func revisionOf(record localRecord) StateVersion {
	if record.state == nil {
		return StateVersion{}
	}
	return StateVersion{record.state.Generation, record.state.Revision, record.state.Sequence}
}
func expectedRequest(record localRecord, generation string, revision uint64) bool {
	current := revisionOf(record)
	return current.Generation == generation && current.Revision == revision
}
func (s *Service) authorize(ctx context.Context, expected localRevision, checkRevision bool) error {
	return s.store.transaction(ctx, func(tx *sql.Tx) error {
		actual, _, err := readLocalAuthority(ctx, tx, expected.authority.Owner, expected.authority.Workspace, s.store.root.fingerprint)
		if err != nil || actual != expected.authority {
			return errLocalScope
		}
		var generation string
		var revision uint64
		err = tx.QueryRowContext(ctx, `SELECT generation,revision FROM backup_recovery_local WHERE workspace_id=?`, actual.Workspace).Scan(&generation, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			generation, revision = "", 0
		} else if err != nil {
			return errLocalState
		}
		if generation != expected.generation || (checkRevision && revision != expected.revision) {
			return errLocalConflict
		}
		return nil
	})
}
func (s *Service) slot(workspace string) *workspaceOperationSlot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slots[workspace] == nil {
		s.slots[workspace] = &workspaceOperationSlot{serial: make(chan struct{}, 1)}
	}
	return s.slots[workspace]
}
func lockSlot(ctx context.Context, slot *workspaceOperationSlot) (func(), error) {
	select {
	case slot.serial <- struct{}{}:
		return func() { <-slot.serial }, nil
	case <-ctx.Done():
		return nil, ErrUnavailable
	default:
		return nil, ErrBusy
	}
}
func selectionScope(owner, workspace, kind string) string {
	return "backup-recovery:" + owner + ":" + workspace + ":" + kind
}

// Pick is a user-control action, not a grant or a metadata/provider read. It
// neither creates a directory reference nor binds an agent filesystem tool.
func (s *Service) Pick(ctx context.Context, owner, workspace, kind string) (string, error) {
	release, err := s.enter()
	if err != nil {
		return "", err
	}
	defer release()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return "", err
	}
	prompt := ""
	switch kind {
	case "source":
		prompt = "Choose a source folder for Backup & Recovery"
	case "volume":
		prompt = "Choose the external backup volume root"
	case "repository":
		prompt = "Choose the existing restic repository folder"
	default:
		return "", ErrInvalidRequest
	}
	if s.picker == nil || !s.picker.Available() || s.selections == nil {
		return "", ErrUnavailable
	}
	pickCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	path, chosen, err := s.picker.Choose(pickCtx, prompt)
	if err != nil {
		return "", ErrUnavailable
	}
	if !chosen {
		return "", ErrSelectionCancelled
	}
	path = strings.TrimRight(path, "/")
	if path != filepath.Clean(path) {
		return "", ErrInvalidRequest
	}
	// The shared token store trims whitespace; reject names it would silently
	// reinterpret rather than granting a different folder from the native choice.
	if path != strings.TrimSpace(path) || !localPath(path) {
		return "", ErrInvalidRequest
	}
	if err := s.authorize(ctx, record.expected(), true); err != nil {
		return "", err
	}
	token, err := s.selections.IssueFor(path, selectionScope(owner, workspace, kind))
	if err != nil {
		return "", ErrUnavailable
	}
	return token, nil
}

func (s *Service) DescribeCredential(ctx context.Context, owner, workspace, vaultID, recordID string) (CredentialReference, error) {
	release, err := s.enter()
	if err != nil {
		return CredentialReference{}, err
	}
	defer release()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return CredentialReference{}, err
	}
	if !localID(vaultID) || !localID(recordID) || s.credentials == nil {
		return CredentialReference{}, ErrInvalidRequest
	}
	ref, err := s.credentials.DescribeBackupSecret(ctx, vault.BackupSecretReference{VaultID: vaultID, RecordID: recordID, WorkspaceID: workspace}, func(ctx context.Context) error { return s.authorize(ctx, record.expected(), true) })
	if err != nil {
		return CredentialReference{}, errCredentialUnavailable
	}
	return CredentialReference{ref.VaultID, ref.RecordID, ref.Revision}, nil
}

// Review resolves only chosen tokens and selected credential metadata. No
// source/repository syscall, secret decryption or provider invocation occurs.
func (s *Service) Review(ctx context.Context, owner, workspace string, request ReviewRequest) (Review, error) {
	release, err := s.enter()
	if err != nil {
		return Review{}, err
	}
	defer release()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return Review{}, err
	}
	if err := metadataAdmission(record); err != nil {
		return Review{}, err
	}
	if !expectedRequest(record, request.Generation, request.Revision) {
		return Review{}, ErrConfigurationChanged
	}
	if !localID(request.Credential.VaultID) || !localID(request.Credential.RecordID) || !metadataID(request.Credential.Revision) {
		return Review{}, ErrInvalidRequest
	}
	configuration := localConfiguration{Provider: "restic", Credential: localCredential{request.Credential.VaultID, request.Credential.RecordID, request.Credential.Revision}}
	if request.UseSavedSelection {
		if record.state == nil || record.state.Configuration == nil || len(request.SourceSelections) != 0 || request.VolumeSelection != "" || request.RepositorySelection != "" {
			return Review{}, ErrInvalidRequest
		}
		configuration = *record.state.Configuration
		configuration.Credential = localCredential{request.Credential.VaultID, request.Credential.RecordID, request.Credential.Revision}
	} else {
		if len(request.SourceSelections) < 1 || len(request.SourceSelections) > maxSelectedSources || s.selections == nil {
			return Review{}, ErrInvalidRequest
		}
		resolve := func(token, kind string) (string, error) {
			if len(token) < 1 || len(token) > 256 {
				return "", ErrInvalidRequest
			}
			value, err := s.selections.ResolveFor(token, selectionScope(owner, workspace, kind))
			if err != nil {
				return "", ErrReviewExpired
			}
			return value, nil
		}
		configuration.VolumePath, err = resolve(request.VolumeSelection, "volume")
		if err != nil {
			return Review{}, err
		}
		configuration.RepositoryPath, err = resolve(request.RepositorySelection, "repository")
		if err != nil {
			return Review{}, err
		}
		for _, token := range request.SourceSelections {
			path, err := resolve(token, "source")
			if err != nil {
				return Review{}, err
			}
			configuration.Sources = append(configuration.Sources, localSource{Path: path})
		}
		// Preserve known same-path bindings; a same-name replacement drive must
		// not be accepted merely because a fresh picker returned that spelling.
		if record.state != nil && record.state.Configuration != nil {
			old := record.state.Configuration
			if old.VolumePath == configuration.VolumePath {
				configuration.Destination = old.Destination
			}
			if old.RepositoryPath == configuration.RepositoryPath {
				configuration.RepositoryID = old.RepositoryID
				configuration.RepositoryFileID = old.RepositoryFileID
			}
			for i, source := range configuration.Sources {
				for _, prior := range old.Sources {
					if source.Path == prior.Path {
						configuration.Sources[i] = prior
					}
				}
			}
		}
	}
	if !configuration.valid(false) {
		return Review{}, ErrInvalidRequest
	}
	if s.credentials == nil {
		return Review{}, errCredentialUnavailable
	}
	ref, err := s.credentials.DescribeBackupSecret(ctx, vault.BackupSecretReference{VaultID: configuration.Credential.VaultID, RecordID: configuration.Credential.RecordID, WorkspaceID: workspace, Revision: configuration.Credential.Revision}, func(ctx context.Context) error { return s.authorize(ctx, record.expected(), true) })
	if err != nil || ref.Revision != configuration.Credential.Revision {
		return Review{}, errCredentialUnavailable
	}
	if err := s.authorize(ctx, record.expected(), true); err != nil {
		return Review{}, err
	}
	now := s.clock().UTC()
	token, expires, err := s.addReview(workspace, pendingReview{expected: record.expected(), configuration: configuration, expires: now.Add(5 * time.Minute)}, now)
	if err != nil {
		return Review{}, err
	}
	paths := make([]string, 0, len(configuration.Sources))
	for _, source := range configuration.Sources {
		paths = append(paths, source.Path)
	}
	return Review{Generation: request.Generation, Revision: request.Revision, Token: token, ExpiresAt: expires, SourcePaths: paths, VolumePath: configuration.VolumePath, RepositoryPath: configuration.RepositoryPath, Credential: request.Credential}, nil
}

func (s *Service) GetStatus(ctx context.Context, owner, workspace string) (Status, error) {
	release, err := s.enter()
	if err != nil {
		return Status{}, err
	}
	defer release()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return Status{}, err
	}
	status := Status{StateVersion: revisionOf(record), WorkspaceID: workspace, ServerTime: s.clock().UTC(), Connection: "not_configured", RuntimeAvailable: s.provider != nil, Destination: "not_checked", Monitoring: "manual_only"}
	s.mu.Lock()
	monitoringBound := s.monitoringBound
	if slot := s.slots[workspace]; slot != nil {
		status.InProgress = slot.active != nil
	}
	s.mu.Unlock()
	if record.state == nil {
		status.Health = evaluateHealth(status, nil, status.ServerTime)
		return status, nil
	}
	state := record.state
	status.RemovalPending = pendingRemoval(record)
	if state.Monitoring.Intent == "paused" {
		status.Monitoring = "paused"
		if currentAttemptNeedsReview(state) {
			status.Monitoring = "review_required"
		}
	} else if state.Monitoring.Intent == "active" {
		status.Monitoring = "review_required"
		if periodicReadAdmissible(record) {
			status.Monitoring = "unavailable"
			if monitoringBound && s.provider != nil {
				status.Monitoring = "active"
			}
		}
	}
	if state.Configuration != nil {
		status.Connection = "review_required"
		status.IntendedSourceCount = len(state.Configuration.Sources)
	}
	if validLocalReceipt(record) {
		status.Connection = "configured"
	}
	if len(state.Attempts) > 0 {
		attempt := state.Attempts[0]
		status.LastAttempt = &Attempt{attempt.Generation, attempt.Sequence, attempt.StartedAt, attempt.FinishedAt, attempt.Outcome}
		if attempt.Generation == state.Generation {
			switch attempt.Outcome {
			case "read_ok":
				status.Destination = "present"
			case "destination_absent":
				status.Destination = "absent"
			case "identity_mismatch":
				status.Destination = "identity_mismatch"
			default:
				status.Destination = "unavailable"
			}
		}
	}
	if state.Observation != nil {
		observation := state.Observation
		at := observation.ObservedAt
		status.LastSuccessfulRead = &at
		status.EvidenceCurrent = validLocalReceipt(record) && observation.Generation == state.Generation
		if status.EvidenceCurrent {
			count := len(observation.Snapshots)
			status.SnapshotCount = &count
			if observation.Capacity != nil {
				status.Capacity = &CapacityEvidence{observation.Capacity.TotalBytes, observation.Capacity.AvailableBytes, observation.ObservedAt}
			}
			var latest *localSnapshot
			for i := range observation.Snapshots {
				snapshot := &observation.Snapshots[i]
				if latest == nil || snapshot.StartedAt.After(latest.StartedAt) {
					latest = snapshot
				}
			}
			if latest != nil {
				status.Coverage = &CoverageEvidence{len(latest.DeclaredPaths), len(latest.Exclusions), "provider_declared_historical"}
			}
			for _, snapshot := range observation.Snapshots {
				if snapshot.CompletedAt != nil && (status.LastCompletedAt == nil || snapshot.CompletedAt.After(*status.LastCompletedAt)) {
					value := *snapshot.CompletedAt
					status.LastCompletedAt = &value
				}
			}
		}
	}
	status.Health = evaluateHealth(status, state, status.ServerTime)
	return status, nil
}
func (s *Service) GetSettings(ctx context.Context, owner, workspace string) (Settings, error) {
	release, err := s.enter()
	if err != nil {
		return Settings{}, err
	}
	defer release()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return Settings{}, err
	}
	settings := Settings{StateVersion: revisionOf(record), SourcePaths: []string{}, PickerAvailable: s.picker != nil && s.picker.Available(), Monitoring: storedMonitoring(record)}
	if record.state != nil && record.state.Configuration != nil {
		configuration := record.state.Configuration
		settings.Configured = true
		settings.VolumePath = configuration.VolumePath
		settings.RepositoryPath = configuration.RepositoryPath
		settings.Credential = &CredentialReference{configuration.Credential.VaultID, configuration.Credential.RecordID, configuration.Credential.Revision}
		for _, source := range configuration.Sources {
			settings.SourcePaths = append(settings.SourcePaths, source.Path)
		}
	}
	return settings, nil
}

func (s *Service) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	if !s.closing {
		s.closing = true
		s.reviews = map[string]pendingReview{}
		s.cancel()
		for _, slot := range s.slots {
			if slot.active != nil {
				slot.active.cancel()
			}
		}
		go func() { s.operations.Wait(); close(s.closeDone) }()
	}
	done := s.closeDone
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return ErrBusy
	case <-done:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		if s.provider != nil {
			s.provider.close()
		}
		s.releaseLifetime()
		s.stopped = true
	}
	return nil
}
