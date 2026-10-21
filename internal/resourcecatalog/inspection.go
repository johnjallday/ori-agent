package resourcecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// pendingReview is one disclosed scope or withdrawal, held in memory: a
// restart or ten minutes expires it, and committing uses it up.
type pendingReview struct {
	kind         string
	owner        string
	studioID     string
	linkID       string
	linkRevision int64
	catalog      CatalogRef
	installation Installation
	sources      []Source
	digest       string
	sourceID     string
	expiresAt    time.Time
}

const (
	reviewScope    = "scope"
	reviewWithdraw = "withdraw"
)

// runningOperation is the one inspection in flight for a Studio.
type runningOperation struct {
	op        Operation
	cancel    context.CancelFunc
	cancelled bool
	done      chan struct{}
}

// InstallationView is the reviewed installation, as the owner sees it.
type InstallationView struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Layout       string `json:"layout"`
	AppInstalled bool   `json:"app_installed"`
	AppVersion   string `json:"app_version,omitempty"`
}

// ReviewSource is one source the review discloses before anything is read.
type ReviewSource struct {
	SourceID      string `json:"source_id"`
	Category      string `json:"category"`
	CategoryLabel string `json:"category_label"`
	Label         string `json:"label"`
	Location      string `json:"location"`
	Access        string `json:"access"`
	State         string `json:"state"`
	Detail        string `json:"detail,omitempty"`
	Approved      bool   `json:"approved"`
	Withdrawn     bool   `json:"withdrawn"`
}

// SourceReview is the scope review: the installation, every source and what
// is already approved on this computer.
type SourceReview struct {
	Token                string           `json:"review_token"`
	ExpiresAt            time.Time        `json:"expires_at"`
	Installation         InstallationView `json:"installation"`
	ReplacesInstallation bool             `json:"replaces_installation"`
	Sources              []ReviewSource   `json:"sources"`
}

// ReviewSources discloses the Studio's sources. It calls only the plugin's
// sources operation, which reads no resource; nothing is approved or read.
func (s *Service) ReviewSources(ctx context.Context, owner, studioID string) (SourceReview, error) {
	ref, status, err := s.attachedStudio(owner, studioID)
	if err != nil {
		return SourceReview{}, err
	}
	answer, err := s.readSources(ctx, ref, status)
	if err != nil {
		return SourceReview{}, err
	}
	grants, hasGrants, err := readGrants(ref.paths)
	if err != nil {
		return SourceReview{}, err
	}
	review := pendingReview{
		kind: reviewScope, owner: normalizedOwner(owner), studioID: ref.workspace.ID,
		linkID: ref.link.ID, linkRevision: ref.link.StateRevision, catalog: status.Catalog,
		installation: answer.Installation, sources: slices.Clone(answer.Sources),
		digest: sourcesDigest(answer), expiresAt: s.now().Add(ReviewTTL),
	}
	token := s.putReview(review)
	view := SourceReview{
		Token: token, ExpiresAt: review.expiresAt,
		Installation: InstallationView{
			ID: answer.Installation.ID, Label: answer.Installation.Label, Layout: answer.Installation.Layout,
			AppInstalled: answer.Installation.AppInstalled != nil && *answer.Installation.AppInstalled, AppVersion: answer.Installation.AppVersion,
		},
		ReplacesInstallation: hasGrants && grants.InstallationID != answer.Installation.ID,
		Sources:              make([]ReviewSource, 0, len(answer.Sources)),
	}
	for _, source := range answer.Sources {
		category, _ := status.Declaration.Category(source.Category)
		item := ReviewSource{
			SourceID: source.ID, Category: source.Category, CategoryLabel: category.Label, Label: source.Label,
			Location: source.Location, Access: source.Access, State: source.State, Detail: source.Detail,
		}
		if hasGrants && grants.InstallationID == answer.Installation.ID {
			if granted := grantSource(grants, source.ID); granted != nil {
				item.Withdrawn = !granted.Active()
				item.Approved = granted.Active() && sameSourceScope(*granted, source)
			}
		}
		view.Sources = append(view.Sources, item)
	}
	return view, nil
}

// CommitSources approves the chosen reviewed sources on this computer and
// starts reading them. A changed source list, installation or link since
// the review refuses the commit before anything is written.
func (s *Service) CommitSources(ctx context.Context, owner, studioID, token, idempotencyKey string, sourceIDs []string) (Operation, error) {
	review, err := s.takeReview(token, reviewScope, owner, studioID)
	if err != nil {
		return Operation{}, err
	}
	if !validIdempotencyKey(idempotencyKey) || len(sourceIDs) == 0 || len(sourceIDs) > MaxSources {
		return Operation{}, ErrInvalidRequest
	}
	ref, status, err := s.attachedStudio(owner, studioID)
	if err != nil {
		return Operation{}, err
	}
	if ref.link.ID != review.linkID || ref.link.StateRevision != review.linkRevision || status.Catalog != review.catalog {
		return Operation{}, ErrReviewStale
	}
	answer, err := s.readSources(ctx, ref, status)
	if err != nil {
		return Operation{}, err
	}
	if sourcesDigest(answer) != review.digest {
		return Operation{}, ErrReviewStale
	}
	seen := map[string]bool{}
	for _, id := range sourceIDs {
		if _, ok := answer.Source(id); !ok || seen[id] {
			return Operation{}, ErrInvalidRequest
		}
		seen[id] = true
	}
	var grants Grants
	err = withLock(ref.paths.dataDir, func() error {
		current, hasGrants, readErr := readGrants(ref.paths)
		if readErr != nil {
			return readErr
		}
		// Another installation or another provider catalog starts over: its
		// approvals never carry to a different resource folder.
		if !hasGrants || current.InstallationID != answer.Installation.ID || current.Provider != status.Catalog || current.LinkID != ref.link.ID {
			current = Grants{
				SchemaVersion: DocumentSchemaVersion, StudioWorkspaceID: ref.workspace.ID, LinkID: ref.link.ID,
				OwnerUserID: normalizedOwner(owner), Provider: status.Catalog, InstallationID: answer.Installation.ID,
				ApprovalRevision: current.ApprovalRevision, Sources: []GrantSource{},
			}
		}
		current.ApprovalRevision++
		now := s.now()
		for _, id := range sourceIDs {
			source, _ := answer.Source(id)
			approved := GrantSource{
				SourceID: source.ID, Category: source.Category, Label: source.Label, Location: source.Location,
				Access: source.Access, ApprovedAt: now, ApprovedRevision: current.ApprovalRevision,
			}
			if existing := grantSource(current, id); existing != nil {
				*existing = approved
			} else {
				current.Sources = append(current.Sources, approved)
			}
		}
		sort.Slice(current.Sources, func(i, j int) bool { return current.Sources[i].SourceID < current.Sources[j].SourceID })
		grants = current
		return writeDocument(ref.paths.grants(), current)
	})
	if err != nil {
		return Operation{}, err
	}
	return s.startInspection(ref, status, answer, grants, idempotencyKey, ModeInspect, sourceIDs)
}

// StartInspection reads approved sources again (refresh) or continues the
// ones a bound stopped (continue). It needs no new review while the
// installation and every source's scope still match their approval.
func (s *Service) StartInspection(ctx context.Context, owner, studioID, idempotencyKey, mode string, sourceIDs []string) (Operation, error) {
	if (mode != ModeRefresh && mode != ModeContinue) || !validIdempotencyKey(idempotencyKey) || len(sourceIDs) > MaxSources {
		return Operation{}, ErrInvalidRequest
	}
	ref, status, err := s.attachedStudio(owner, studioID)
	if err != nil {
		return Operation{}, err
	}
	if existing, found := s.operationByKey(ref, idempotencyKey); found {
		return existing, nil
	}
	grants, hasGrants, err := readGrants(ref.paths)
	if err != nil {
		return Operation{}, err
	}
	if !hasGrants || grants.LinkID != ref.link.ID || grants.Provider != status.Catalog {
		return Operation{}, ErrScopeReviewRequired
	}
	answer, err := s.readSources(ctx, ref, status)
	if err != nil {
		return Operation{}, err
	}
	if answer.Installation.ID != grants.InstallationID {
		return Operation{}, ErrScopeReviewRequired
	}
	wanted := map[string]bool{}
	for _, id := range sourceIDs {
		wanted[id] = true
	}
	observations, err := readObservations(ref.paths)
	if err != nil {
		return Operation{}, err
	}
	var targets []string
	for _, granted := range grants.Sources {
		if !granted.Active() || (len(wanted) > 0 && !wanted[granted.SourceID]) {
			continue
		}
		source, ok := answer.Source(granted.SourceID)
		if !ok || !sameSourceScope(granted, source) {
			return Operation{}, ErrScopeReviewRequired
		}
		if mode == ModeContinue {
			record := sourceRecord(observations, grants.InstallationID, granted.SourceID)
			if record == nil || record.Cursor == "" {
				continue
			}
		}
		targets = append(targets, granted.SourceID)
	}
	if len(targets) == 0 {
		if mode == ModeContinue {
			return Operation{}, ErrInvalidRequest
		}
		return Operation{}, ErrScopeReviewRequired
	}
	return s.startInspection(ref, status, answer, grants, idempotencyKey, mode, targets)
}

func (s *Service) startInspection(ref studioRef, status ProviderStatus, answer SourcesAnswer, grants Grants, key, mode string, sourceIDs []string) (Operation, error) {
	if existing, found := s.operationByKey(ref, key); found {
		return existing, nil
	}
	observations, err := readObservations(ref.paths)
	if err != nil {
		return Operation{}, err
	}
	s.mu.Lock()
	if active := s.running[ref.workspace.ID]; active != nil {
		op := cloneOperation(active.op)
		s.mu.Unlock()
		return op, ErrInspectionActive
	}
	op := Operation{
		ID: "rcop_" + s.deps.NewID(), IdempotencyKey: key, Mode: mode, State: OperationRunning, StartedAt: s.now(),
		InstallationID: answer.Installation.ID, BaseRevision: observations.Revision, ApprovalRevision: grants.ApprovalRevision,
	}
	for _, id := range sourceIDs {
		granted := grantSource(grants, id)
		source, _ := answer.Source(id)
		op.Sources = append(op.Sources, OperationSource{
			SourceID: id, Category: source.Category, State: ProgressPending,
			ApprovedRevision: granted.ApprovedRevision, Continued: mode == ModeContinue,
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), InspectionDeadline)
	// The running copy owns its progress; op stays the caller's own value.
	run := &runningOperation{op: cloneOperation(op), cancel: cancel, done: make(chan struct{})}
	s.running[ref.workspace.ID] = run
	s.mu.Unlock()
	if err := s.saveOperation(ref, op); err != nil {
		s.mu.Lock()
		delete(s.running, ref.workspace.ID)
		s.mu.Unlock()
		cancel()
		return Operation{}, err
	}
	go s.run(ctx, run, ref, status, answer, observations)
	return cloneOperation(op), nil
}

// cloneOperation copies an operation so a caller never shares the progress
// a running inspection is still writing.
func cloneOperation(op Operation) Operation {
	op.Sources = slices.Clone(op.Sources)
	if op.FinishedAt != nil {
		finished := *op.FinishedAt
		op.FinishedAt = &finished
	}
	return op
}

// sourceResult is what one source's read produced before publication.
type sourceResult struct {
	outcome    string
	reason     string
	entries    map[string]InventoryEntry
	order      []string
	skipped    map[string]int
	cursor     string
	chainStart time.Time
	publish    bool
	complete   bool
}

func (s *Service) run(ctx context.Context, run *runningOperation, ref studioRef, status ProviderStatus, answer SourcesAnswer, observations Observations) {
	defer close(run.done)
	defer run.cancel()
	results := make([]sourceResult, len(run.op.Sources))
	total := 0
	stopAll := false
	for index := range run.op.Sources {
		source := run.op.Sources[index]
		result := &results[index]
		result.entries, result.skipped = map[string]InventoryEntry{}, map[string]int{}
		result.chainStart = s.now()
		cursor := ""
		if source.Continued {
			if record := sourceRecord(observations, run.op.InstallationID, source.SourceID); record != nil && record.Cursor != "" {
				cursor = record.Cursor
				if record.ChainStartedAt != nil {
					result.chainStart = *record.ChainStartedAt
				}
			}
		}
		if stopAll {
			result.outcome, result.reason = OutcomeCancelled, "stopped"
			s.progress(run, index, result.outcome, 0, 0, result.reason)
			continue
		}
		s.progress(run, index, ProgressReading, 0, 0, "")
		stopAll = s.readSource(ctx, run, ref, status, answer, index, cursor, &total, result)
		s.progress(run, index, result.outcome, -1, len(result.entries), result.reason)
	}
	s.publish(run, ref, status, answer, results)
}

// readSource reads one source page by page. It re-checks the source's
// approval before every page and after every answer, so a withdrawal stops
// the next request and discards anything not yet accepted. It returns true
// when the whole inspection must stop.
func (s *Service) readSource(ctx context.Context, run *runningOperation, ref studioRef, status ProviderStatus, answer SourcesAnswer, index int, cursor string, total *int, result *sourceResult) bool {
	opSource := run.op.Sources[index]
	source, _ := answer.Source(opSource.SourceID)
	folder, kept := s.folderRef(ref, opSource.SourceID)
	if !kept {
		// The owner removed the folder, which withdrew it.
		result.outcome, result.reason, result.publish = OutcomeWithdrawn, "", false
		return false
	}
	resetOnce := false
	for page := 0; ; page++ {
		if ctx.Err() != nil {
			s.stopped(ctx, run, result, cursor)
			return run.isCancelled(s)
		}
		if page >= MaxPagesPerSource || len(result.entries) >= MaxEntriesPerSource || *total >= MaxEntriesPerCatalog {
			result.outcome, result.reason, result.cursor, result.publish = OutcomePartial, "limit", cursor, true
			return false
		}
		if !s.stillApproved(ref, run.op.InstallationID, opSource) {
			result.outcome, result.reason, result.publish = OutcomeWithdrawn, "", false
			return false
		}
		request, err := NewInventoryRequest(run.op.InstallationID, opSource.SourceID, cursor, PageLimit, folder)
		if err != nil {
			result.outcome, result.reason = OutcomeFailed, "invalid_cursor"
			return false
		}
		input, _ := json.Marshal(request)
		raw, err := s.deps.Provider.Inventory(ctx, ref.workspace, input)
		if ctx.Err() != nil {
			s.stopped(ctx, run, result, cursor)
			return run.isCancelled(s)
		}
		if err != nil {
			result.outcome, result.reason = OutcomeFailed, "read_failed"
			return false
		}
		answered, err := DecodeInventoryAnswer(raw, status.Declaration, source, request)
		if err != nil {
			result.outcome, result.reason = OutcomeFailed, "invalid_answer"
			return false
		}
		if !s.stillApproved(ref, run.op.InstallationID, opSource) {
			result.outcome, result.reason, result.publish = OutcomeWithdrawn, "", false
			return false
		}
		switch answered.State {
		case InventoryOK:
		case InventoryCursorReset:
			if resetOnce {
				result.outcome, result.reason = OutcomeFailed, "cursor_reset"
				return false
			}
			resetOnce, cursor = true, ""
			*total -= len(result.entries)
			result.entries, result.order = map[string]InventoryEntry{}, nil
			continue
		case InventoryInstallationChanged:
			result.outcome, result.reason = OutcomeFailed, InventoryInstallationChanged
			return true
		case SourceAbsent:
			result.outcome = OutcomeAbsent
			return false
		case SourceDenied:
			result.outcome = OutcomeDenied
			return false
		case SourceUnsupported:
			result.outcome = OutcomeUnsupported
			return false
		default:
			result.outcome, result.reason = OutcomeUnavailable, answered.State
			return false
		}
		for _, entry := range answered.Entries {
			id := EntryID(run.op.InstallationID, opSource.SourceID, entry.NativeID)
			if _, seen := result.entries[id]; !seen {
				result.order = append(result.order, id)
				*total++
			}
			result.entries[id] = entry
		}
		for _, skip := range answered.Coverage.Skipped {
			result.skipped[skip.Reason] += skip.Count
		}
		s.progress(run, index, ProgressReading, page+1, len(result.entries), "")
		cursor = *answered.NextCursor
		if cursor == "" {
			result.outcome, result.publish, result.complete = OutcomeComplete, true, true
			return false
		}
	}
}

// stopped records how a context ended a source: a cancellation discards the
// source's unfinished pages, the deadline keeps them as partial with the
// cursor so a continuation can pick up.
func (s *Service) stopped(ctx context.Context, run *runningOperation, result *sourceResult, cursor string) {
	if run.isCancelled(s) || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.outcome, result.reason, result.publish = OutcomeCancelled, "", false
		return
	}
	result.outcome, result.reason, result.cursor, result.publish = OutcomePartial, "deadline", cursor, len(result.entries) > 0
}

func (run *runningOperation) isCancelled(s *Service) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return run.cancelled
}

// stillApproved re-reads this computer's grant under its lock: the source
// must still be active with the approval the read started from.
func (s *Service) stillApproved(ref studioRef, installationID string, source OperationSource) bool {
	approved := false
	_ = withLock(ref.paths.dataDir, func() error {
		grants, hasGrants, err := readGrants(ref.paths)
		if err != nil || !hasGrants || grants.InstallationID != installationID {
			return nil
		}
		granted := grantSource(grants, source.SourceID)
		approved = granted != nil && granted.Active() && granted.ApprovedRevision == source.ApprovedRevision
		return nil
	})
	return approved
}

func (s *Service) progress(run *runningOperation, index int, state string, pages, entries int, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source := &run.op.Sources[index]
	source.State = state
	if pages >= 0 {
		source.Pages = max(source.Pages, pages)
	}
	source.Entries, source.Reason = entries, reason
}

// publish writes one coherent revision under the catalog lock and then the
// grants lock, the same order a withdrawal's read takes, so no result of a
// withdrawn source is ever written. An older revision than the one this
// inspection started from means another writer won: nothing is written.
func (s *Service) publish(run *runningOperation, ref studioRef, status ProviderStatus, answer SourcesAnswer, results []sourceResult) {
	finished := s.now()
	err := withLock(ref.paths.catalogDir, func() error {
		return withLock(ref.paths.dataDir, func() error {
			observations, err := readObservations(ref.paths)
			if err != nil {
				return err
			}
			if observations.Revision != run.op.BaseRevision {
				s.finish(run, OperationSuperseded, 0, finished)
				return nil
			}
			grants, hasGrants, err := readGrants(ref.paths)
			if err != nil {
				return err
			}
			annotations, err := readAnnotations(ref.paths)
			if err != nil {
				return err
			}
			revision := observations.Revision + 1
			upsertInstallation(&observations, answer.Installation, finished)
			for index, opSource := range run.op.Sources {
				result := results[index]
				granted := (*GrantSource)(nil)
				if hasGrants && grants.InstallationID == run.op.InstallationID {
					granted = grantSource(grants, opSource.SourceID)
				}
				if granted == nil || !granted.Active() || granted.ApprovedRevision != opSource.ApprovedRevision {
					result = sourceResult{outcome: OutcomeWithdrawn, chainStart: result.chainStart}
					s.progress(run, index, OutcomeWithdrawn, -1, 0, "")
				}
				source, _ := answer.Source(opSource.SourceID)
				mergeSource(&observations, run.op, source, result, revision, finished)
			}
			pruneTombstones(&observations, annotations)
			observations.SchemaVersion = DocumentSchemaVersion
			observations.Catalog, observations.DeclaredBy = status.Catalog, status.Declaration
			observations.Revision, observations.PublishedAt = revision, &finished
			sort.Slice(observations.Entries, func(i, j int) bool { return observations.Entries[i].EntryID < observations.Entries[j].EntryID })
			if err := writeDocument(ref.paths.observations(), observations); err != nil {
				return err
			}
			state := OperationPublished
			if run.isCancelled(s) {
				state = OperationCancelled
			}
			s.finish(run, state, revision, finished)
			return nil
		})
	})
	if err != nil {
		s.finish(run, OperationFailed, 0, finished)
	}
	s.mu.Lock()
	op := cloneOperation(run.op)
	delete(s.running, ref.workspace.ID)
	s.mu.Unlock()
	_ = s.saveOperation(ref, op)
}

func (s *Service) finish(run *runningOperation, state string, revision int64, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run.op.State, run.op.PublishedRevision, run.op.FinishedAt = state, revision, &at
}

// mergeSource applies one source's result. Only a complete read may mark an
// entry it did not see as no longer found; partial, failed or cancelled reads
// never remove anything.
func mergeSource(observations *Observations, op Operation, source Source, result sourceResult, revision int64, finished time.Time) {
	record := sourceRecord(*observations, op.InstallationID, source.ID)
	if record == nil {
		observations.Sources = append(observations.Sources, SourceRecord{InstallationID: op.InstallationID, SourceID: source.ID})
		record = &observations.Sources[len(observations.Sources)-1]
	}
	record.Category, record.Label, record.Access = source.Category, source.Label, source.Access
	attempt := &Attempt{OperationID: op.ID, StartedAt: op.StartedAt, FinishedAt: finished, Outcome: result.outcome, Reason: result.reason, EntriesRead: len(result.entries)}
	for reason, count := range result.skipped {
		attempt.Skipped = append(attempt.Skipped, Skip{Reason: reason, Count: count})
	}
	sort.Slice(attempt.Skipped, func(i, j int) bool { return attempt.Skipped[i].Reason < attempt.Skipped[j].Reason })
	record.LastAttempt = attempt
	if !result.publish {
		return
	}
	index := make(map[string]int, len(observations.Entries))
	for position, entry := range observations.Entries {
		index[entry.EntryID] = position
	}
	for _, id := range result.order {
		observed := result.entries[id]
		entry := EntryRecord{
			EntryID: id, InstallationID: op.InstallationID, SourceID: source.ID, Category: source.Category,
			Kind: observed.Kind, NativeID: observed.NativeID, Name: observed.Name, Facts: slices.Clone(observed.Facts),
			Description: observed.Description, ModifiedAt: parseModified(observed.ModifiedAt),
			FirstObservedAt: finished, LastObservedAt: finished, ObservedRevision: revision, State: EntryPresent,
		}
		if position, found := index[id]; found {
			entry.FirstObservedAt = observations.Entries[position].FirstObservedAt
			observations.Entries[position] = entry
			continue
		}
		index[id] = len(observations.Entries)
		observations.Entries = append(observations.Entries, entry)
	}
	if result.complete {
		for position := range observations.Entries {
			entry := &observations.Entries[position]
			if entry.InstallationID == op.InstallationID && entry.SourceID == source.ID && entry.State == EntryPresent &&
				entry.LastObservedAt.Before(result.chainStart) {
				entry.State, entry.NoLongerFoundAt = EntryNoLongerFound, &finished
			}
		}
		record.LastCompleteAt, record.Cursor, record.ChainStartedAt = &finished, "", nil
		return
	}
	if result.cursor != "" {
		chainStart := result.chainStart
		record.Cursor, record.ChainStartedAt = result.cursor, &chainStart
	}
}

// pruneTombstones keeps every annotated entry and the newest unannotated
// no-longer-found ones, so a catalog that churns stays bounded.
func pruneTombstones(observations *Observations, annotations Annotations) {
	var tombstones []int
	for position, entry := range observations.Entries {
		if _, annotated := annotations.Annotations[entry.EntryID]; entry.State == EntryNoLongerFound && !annotated {
			tombstones = append(tombstones, position)
		}
	}
	if len(tombstones) <= maxUnannotatedTombstones {
		return
	}
	sort.Slice(tombstones, func(i, j int) bool {
		left, right := observations.Entries[tombstones[i]], observations.Entries[tombstones[j]]
		return left.NoLongerFoundAt.After(*right.NoLongerFoundAt)
	})
	drop := map[int]bool{}
	for _, position := range tombstones[maxUnannotatedTombstones:] {
		drop[position] = true
	}
	kept := observations.Entries[:0]
	for position, entry := range observations.Entries {
		if !drop[position] {
			kept = append(kept, entry)
		}
	}
	observations.Entries = kept
}

func upsertInstallation(observations *Observations, installation Installation, at time.Time) {
	for index := range observations.Installations {
		if observations.Installations[index].ID == installation.ID {
			observations.Installations[index].Label, observations.Installations[index].Layout = installation.Label, installation.Layout
			observations.Installations[index].LastSeenAt = at
			return
		}
	}
	observations.Installations = append(observations.Installations, InstallationRecord{
		ID: installation.ID, Label: installation.Label, Layout: installation.Layout, FirstSeenAt: at, LastSeenAt: at,
	})
}

// Cancel stops a running inspection. Sources already finished still
// publish; the operation is reported as cancelled, never completed.
func (s *Service) Cancel(owner, studioID, operationID string) (Operation, error) {
	ref, err := s.resolveStudio(owner, studioID)
	if err != nil {
		return Operation{}, err
	}
	s.mu.Lock()
	if run := s.running[ref.workspace.ID]; run != nil && run.op.ID == operationID {
		run.cancelled = true
		op := cloneOperation(run.op)
		s.mu.Unlock()
		run.cancel()
		return op, nil
	}
	s.mu.Unlock()
	return s.Operation(owner, studioID, operationID)
}

// Operation returns one inspection, live while it runs.
func (s *Service) Operation(owner, studioID, operationID string) (Operation, error) {
	ref, err := s.resolveStudio(owner, studioID)
	if err != nil {
		return Operation{}, err
	}
	s.mu.Lock()
	if run := s.running[ref.workspace.ID]; run != nil && run.op.ID == operationID {
		op := cloneOperation(run.op)
		s.mu.Unlock()
		return op, nil
	}
	s.mu.Unlock()
	operations, err := s.operations(ref)
	if err != nil {
		return Operation{}, err
	}
	for _, op := range operations {
		if op.ID == operationID {
			return op, nil
		}
	}
	return Operation{}, ErrNotFound
}

// wait blocks until the Studio's running inspection ends.
func (s *Service) wait(studioID string) {
	s.mu.Lock()
	run := s.running[studioID]
	s.mu.Unlock()
	if run != nil {
		<-run.done
	}
}

// operations reads the Studio's recent inspections. One still marked running
// that this process is not running was cut short by a restart: it is
// interrupted, and nothing it read was published.
func (s *Service) operations(ref studioRef) ([]Operation, error) {
	var operations []Operation
	err := withLock(ref.paths.dataDir, func() error {
		stored, err := readOperations(ref.paths)
		if err != nil {
			return err
		}
		s.mu.Lock()
		active := s.running[ref.workspace.ID]
		s.mu.Unlock()
		changed := false
		for index := range stored {
			if stored[index].State == OperationRunning && (active == nil || active.op.ID != stored[index].ID) {
				now := s.now()
				stored[index].State, stored[index].FinishedAt = OperationInterrupted, &now
				changed = true
			}
		}
		operations = stored
		if changed {
			return writeOperations(ref.paths, stored)
		}
		return nil
	})
	return operations, err
}

func (s *Service) operationByKey(ref studioRef, key string) (Operation, bool) {
	s.mu.Lock()
	if run := s.running[ref.workspace.ID]; run != nil && run.op.IdempotencyKey == key {
		op := cloneOperation(run.op)
		s.mu.Unlock()
		return op, true
	}
	s.mu.Unlock()
	operations, err := s.operations(ref)
	if err != nil {
		return Operation{}, false
	}
	for _, op := range operations {
		if op.IdempotencyKey == key {
			return op, true
		}
	}
	return Operation{}, false
}

func (s *Service) saveOperation(ref studioRef, op Operation) error {
	return withLock(ref.paths.dataDir, func() error {
		stored, err := readOperations(ref.paths)
		if err != nil {
			return err
		}
		for index := range stored {
			if stored[index].ID == op.ID {
				stored[index] = op
				return writeOperations(ref.paths, stored)
			}
		}
		return writeOperations(ref.paths, append(stored, op))
	})
}

// attachedStudio resolves a Studio that can be inspected: attached to its
// Home with an available provider.
func (s *Service) attachedStudio(owner, studioID string) (studioRef, ProviderStatus, error) {
	ref, err := s.resolveStudio(owner, studioID)
	if err != nil {
		return studioRef{}, ProviderStatus{}, err
	}
	if !ref.attached {
		return studioRef{}, ProviderStatus{}, ErrDetached
	}
	observations, err := readObservations(ref.paths)
	if err != nil {
		return studioRef{}, ProviderStatus{}, err
	}
	status := s.provider(ref, observations)
	if !status.Available {
		return studioRef{}, ProviderStatus{}, fmt.Errorf("%w: %s", ErrProviderUnavailable, status.Reason)
	}
	return ref, status, nil
}

// readSources asks the provider for its sources, giving it this computer's
// owner folders, and requires every folder back exactly as it was given.
func (s *Service) readSources(ctx context.Context, ref studioRef, status ProviderStatus) (SourcesAnswer, error) {
	folders, err := readFolders(ref.paths)
	if err != nil {
		return SourcesAnswer{}, err
	}
	input, sent := sourcesInput(status.Declaration, folders)
	raw, err := s.deps.Provider.Sources(ctx, ref.workspace, input)
	if err != nil {
		return SourcesAnswer{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	answer, err := DecodeSourcesAnswer(raw, status.Declaration)
	if err == nil {
		err = checkFolderSources(answer, sent)
	}
	if err != nil {
		return SourcesAnswer{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	return answer, nil
}

// folderRef is the owner folder a folder source reads, or nil for the
// provider's own sources. A removed folder is reported as missing.
func (s *Service) folderRef(ref studioRef, sourceID string) (*FolderRef, bool) {
	if !IsFolderSource(sourceID) {
		return nil, true
	}
	folders, err := readFolders(ref.paths)
	if err != nil {
		return nil, false
	}
	folder := ownerFolder(folders, sourceID)
	if folder == nil {
		return nil, false
	}
	return &FolderRef{Category: folder.Category, Path: folder.Path}, true
}

func (s *Service) putReview(review pendingReview) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for token, existing := range s.reviews {
		if !now.Before(existing.expiresAt) {
			delete(s.reviews, token)
		}
	}
	token := "rcrv_" + s.deps.NewID()
	s.reviews[token] = review
	return token
}

// takeReview uses a review up. Another owner's, another Studio's or an
// expired token is stale, exactly like a used one.
func (s *Service) takeReview(token, kind, owner, studioID string) (pendingReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	review, found := s.reviews[strings.TrimSpace(token)]
	if !found {
		return pendingReview{}, ErrReviewStale
	}
	delete(s.reviews, strings.TrimSpace(token))
	if review.kind != kind || review.owner != normalizedOwner(owner) || review.studioID != strings.TrimSpace(studioID) || !s.now().Before(review.expiresAt) {
		return pendingReview{}, ErrReviewStale
	}
	return review, nil
}

// sourcesDigest binds what a review disclosed: the installation and every
// source's identity, category, words, location and access. Current
// availability (open or closed application) is not part of the scope.
func sourcesDigest(answer SourcesAnswer) string {
	type scoped struct {
		ID, Category, Label, Location, Access string
	}
	scope := struct {
		Installation, Layout string
		Sources              []scoped
	}{Installation: answer.Installation.ID, Layout: answer.Installation.Layout}
	for _, source := range answer.Sources {
		scope.Sources = append(scope.Sources, scoped{source.ID, source.Category, source.Label, source.Location, source.Access})
	}
	sort.Slice(scope.Sources, func(i, j int) bool { return scope.Sources[i].ID < scope.Sources[j].ID })
	encoded, _ := json.Marshal(scope)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// sameSourceScope reports whether a source still reads what was approved.
// A new label is fine; another location, category or access is a new scope.
func sameSourceScope(granted GrantSource, source Source) bool {
	return granted.Category == source.Category && granted.Location == source.Location && granted.Access == source.Access
}

func grantSource(grants Grants, sourceID string) *GrantSource {
	for index := range grants.Sources {
		if grants.Sources[index].SourceID == sourceID {
			return &grants.Sources[index]
		}
	}
	return nil
}

func sourceRecord(observations Observations, installationID, sourceID string) *SourceRecord {
	for index := range observations.Sources {
		if observations.Sources[index].InstallationID == installationID && observations.Sources[index].SourceID == sourceID {
			return &observations.Sources[index]
		}
	}
	return nil
}

func parseModified(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func validIdempotencyKey(key string) bool {
	key = strings.TrimSpace(key)
	return key != "" && len(key) <= 120 && validCursor(key, false)
}
