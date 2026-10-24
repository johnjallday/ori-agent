package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors of an inbox hire report's Retry.
var (
	// ErrFirstBatchNotFailed is a Retry on a first batch whose scan completed.
	ErrFirstBatchNotFailed = errors.New("personal assistant: the first scan did not fail")
	// ErrFirstBatchRetryBusy is a Retry while another is still running.
	ErrFirstBatchRetryBusy = errors.New("personal assistant: the first scan is already being retried")
)

// FolderInboxIntent is everything a reviewed inbox-folder setup promised, in a
// form the run checks before each effect: which folder, which workspace name,
// and the exact File Janitor team plan the creation is held to. It is part of
// what the plan digest covers and never leaves the server. It holds no path
// and no model witness: the first batch is a metadata-only scan, so nothing
// about it waits on a model.
type FolderInboxIntent struct {
	WorkspaceName string `json:"workspace_name"`
	// SubjectKey and SubjectIdentity witness the one directory the review was
	// about: its canonical path's key and the directory itself.
	SubjectKey      string `json:"subject_key"`
	SubjectIdentity string `json:"subject_identity"`
	// The reviewed File Janitor team plan, as the strict creator compares it.
	BlueprintID      string           `json:"blueprint_id"`
	BlueprintVersion int              `json:"blueprint_version"`
	BlueprintDigest  string           `json:"blueprint_digest"`
	PlanRevision     string           `json:"plan_revision"`
	Agent            FolderInboxAgent `json:"agent"`
	// Conversation is the conversation revision the review belongs to.
	Conversation string `json:"conversation,omitempty"`
}

// FolderInboxAgent is the File Curator role of the reviewed team plan: created
// from the blueprint (create) or the user's saved agent of that name (reuse).
type FolderInboxAgent struct {
	RoleID          string `json:"role_id"`
	Name            string `json:"name"`
	Action          string `json:"action"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	ModelConfigured bool   `json:"model_configured,omitempty"`
	ConfigDigest    string `json:"config_digest"`
	Warning         string `json:"warning,omitempty"`
}

// Validate checks an inbox intent's shape: the File Janitor blueprint, one
// agent created or reused, bounded text and no path. It does not check the
// intent against anything on disk.
func (i FolderInboxIntent) Validate() error {
	if i.BlueprintID != "file-janitor" || i.BlueprintVersion < 1 || strings.TrimSpace(i.BlueprintDigest) == "" || strings.TrimSpace(i.PlanRevision) == "" {
		return fmt.Errorf("%w: inbox blueprint", errFolderDigestInvalid)
	}
	switch i.Agent.Action {
	case FolderSoftwareCreate, FolderSoftwareReuse:
	default:
		return fmt.Errorf("%w: inbox agent action", errFolderDigestInvalid)
	}
	if strings.TrimSpace(i.Agent.Name) == "" || strings.TrimSpace(i.Agent.RoleID) == "" || strings.TrimSpace(i.Agent.ConfigDigest) == "" {
		return fmt.Errorf("%w: inbox agent", errFolderDigestInvalid)
	}
	if validateFolderKey(i.SubjectKey) != nil || strings.TrimSpace(i.SubjectIdentity) == "" || validateFolderName(i.WorkspaceName) != nil {
		return fmt.Errorf("%w: inbox subject", errFolderDigestInvalid)
	}
	for _, text := range []string{i.BlueprintDigest, i.PlanRevision, i.SubjectIdentity, i.Agent.RoleID, i.Agent.Name,
		i.Agent.Provider, i.Agent.Model, i.Agent.ConfigDigest, i.Conversation} {
		if len(text) > 200 || strings.ContainsAny(text, "\\\x00\r\n") || strings.HasPrefix(text, "/") || strings.HasPrefix(text, "~") {
			return fmt.Errorf("%w: inbox intent text", errFolderDigestInvalid)
		}
	}
	if len(i.Agent.Warning) > 500 || strings.ContainsAny(i.Agent.Warning, "\x00\r\n") {
		return fmt.Errorf("%w: inbox agent warning", errFolderDigestInvalid)
	}
	return nil
}

// Outcomes of an inbox hire's first batch: a batch to review, a scan that found
// nothing to file, or a scan that did not complete.
const (
	FolderFirstBatchReady  = "batch"
	FolderFirstBatchEmpty  = "no_eligible"
	FolderFirstBatchFailed = "failed"
)

// FolderFirstBatch is an inbox hire's first look: the first metadata-only
// scan's batch, read back from File Janitor's own record. Route opens the
// review surface on that batch. None of it is a path.
type FolderFirstBatch struct {
	Outcome     string `json:"outcome"`
	BatchID     string `json:"batch_id,omitempty"`
	Eligible    int    `json:"eligible"`
	NeedsReview int    `json:"needs_review"`
	Ineligible  int    `json:"ineligible"`
	Route       string `json:"route,omitempty"`
	// Attempts counts the report's Retry presses; each runs under a scan
	// operation of its own.
	Attempts int `json:"attempts,omitempty"`
	// DismissedAt is when the report's Not now was pressed: it leaves Needs
	// you and Today, and the batch stays in File Janitor.
	DismissedAt *time.Time `json:"dismissed_at,omitempty"`
}

// folderFirstBatchMaxAttempts bounds the report's Retry presses.
const folderFirstBatchMaxAttempts = 20

func (b *FolderFirstBatch) validate() error {
	if b == nil {
		return nil
	}
	switch b.Outcome {
	case FolderFirstBatchReady, FolderFirstBatchEmpty, FolderFirstBatchFailed:
	default:
		return fmt.Errorf("%w: first batch outcome", errFolderDigestInvalid)
	}
	if b.Attempts < 0 || b.Attempts > folderFirstBatchMaxAttempts {
		return fmt.Errorf("%w: first batch attempts", errFolderDigestInvalid)
	}
	if b.Eligible < 0 || b.NeedsReview < 0 || b.Ineligible < 0 || b.NeedsReview > b.Eligible ||
		len(b.BatchID) > 200 || strings.ContainsAny(b.BatchID, "/\\\x00\r\n") ||
		len(b.Route) > 512 || (b.Route != "" && !strings.HasPrefix(b.Route, "/workspaces/")) || strings.ContainsAny(b.Route, "\\\x00\r\n") {
		return fmt.Errorf("%w: first batch", errFolderDigestInvalid)
	}
	return nil
}

// IsInboxOffer reports an offer about an inbox folder that the host's own
// File Janitor setup answers: the folder's root, reviewed as the inbox shape.
func IsInboxOffer(offer FolderOffer) bool { return isInboxOffer(offer) }

// resolveInbox settles a finished inbox run. The run saying it is done is not
// enough: the host re-reads the workspace, the folder's grant and the first
// batch from canonical state, for this owner, this exact directory and the
// intent that was confirmed. The offer's receipt and first look are what that
// read returned. No model task is started: the batch is the first look.
func (s *FolderDigestService) resolveInbox(ctx context.Context, userID, offerID string) error {
	if s.deps.InboxProject == nil {
		return ErrFolderOutcomeUnavailable
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return err
	}
	offer := doc.Offer(offerID)
	if offer == nil || offer.Setup == nil || offer.Setup.Status != FolderSetupDone || offer.Status != FolderOfferAwaitingOutcome ||
		offer.Setup.Intent.Inbox == nil || !isInboxOffer(*offer) {
		return ErrFolderOfferDecided
	}
	path, identity, err := s.softwareSubject(*offer)
	if err != nil {
		return err
	}
	verified, err := s.deps.InboxProject(ctx, userID, *offer, path, identity)
	if err != nil || strings.TrimSpace(verified.WorkspaceID) == "" || !strings.HasPrefix(verified.Route, "/workspaces/") {
		return ErrFolderWorkspaceRefused
	}
	if err := verified.FirstBatch.validate(); err != nil {
		return err
	}
	now := s.now()
	requestID := "setup-" + offerID + "-inbox"
	var resolved FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil || item.Status != FolderOfferAwaitingOutcome || item.Choice != FolderChoiceProject {
			return ErrFolderOfferDecided
		}
		blueprint, _, _ := s.blueprintForOffer(*item)
		item.Status, item.ResolvedAt = FolderOfferResolved, &now
		item.Outcome = &FolderOutcome{
			Kind: FolderChoiceProject, WorkspaceID: verified.WorkspaceID, Route: verified.Route,
			Blueprint: blueprint, Existing: !verified.Created,
			Receipt: append([]FolderReceiptRow(nil), verified.Receipt...),
		}
		if batch := verified.FirstBatch; batch != nil {
			copied := *batch
			item.Outcome.FirstLook = &FolderFirstLookRecord{Batch: &copied, At: now}
		}
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: requestID, OfferID: offerID, Action: "inbox", At: now})
		pruneFolderDigest(d)
		resolved = *item
		return nil
	})
	if err != nil {
		return err
	}
	s.afterOutcome(ctx, userID, resolved)
	return nil
}

type folderSubjectPathKey struct{}

// withFolderSubjectPath carries the folder a review is about to a plan read,
// for a review that is only being described and is not stored yet: the held
// selection names it. The path never leaves the server.
func withFolderSubjectPath(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, folderSubjectPathKey{}, path)
}

// planRequest is setupRequest for a plan read. An inbox folder's plan checks the
// folder against File Janitor's other workspaces and its root rules, so the
// request carries the folder's path (never sent to the browser).
func (s *FolderDigestService) planRequest(ctx context.Context, userID string, offer FolderOffer) FolderSetupRequest {
	req := s.setupRequest(ctx, userID, offer)
	if !isInboxOffer(offer) {
		return req
	}
	if path, _ := ctx.Value(folderSubjectPathKey{}).(string); strings.TrimSpace(path) != "" {
		req.Path = path
	} else if path, err := s.subjectPathOf(offer); err == nil {
		req.Path = path
	}
	return req
}

// inboxHire is the user's resolved inbox hire with a first batch, found by its
// offer among their own folder setups: never from a browser's word alone.
func inboxHire(doc FolderDigestDocument, offerID string) (*FolderOffer, error) {
	offer := doc.Offer(strings.TrimSpace(offerID))
	if offer == nil || offer.Status != FolderOfferResolved || offer.Outcome == nil || offer.Outcome.FirstLook == nil ||
		offer.Outcome.FirstLook.Batch == nil || strings.TrimSpace(offer.Outcome.WorkspaceID) == "" || !isInboxOffer(*offer) {
		return nil, ErrFirstLookNotFound
	}
	return offer, nil
}

// DismissFirstBatch is Not now on an inbox hire's report: it leaves Needs you
// and Today. The batch itself stays in File Janitor, where it is reviewed.
func (s *FolderDigestService) DismissFirstBatch(ctx context.Context, userID, offerID string) error {
	if s == nil || s.store == nil {
		return ErrFolderOutcomeUnavailable
	}
	now := s.now()
	_, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		offer, err := inboxHire(*d, offerID)
		if err != nil {
			return err
		}
		item := d.Offer(offer.ID)
		if item.Outcome.FirstLook.Batch.DismissedAt != nil {
			return errFolderReplay
		}
		at := now
		item.Outcome.FirstLook.Batch.DismissedAt = &at
		return nil
	})
	if errors.Is(err, errFolderReplay) {
		return nil
	}
	return err
}

// RetryFirstBatch is Retry on an inbox hire whose first scan did not complete:
// the setup's step 4 again, alone, under an attempt of its own. The workspace,
// File Curator and the folder link were kept and are not touched. The attempt
// is recorded before it runs, and its result replaces the report.
func (s *FolderDigestService) RetryFirstBatch(ctx context.Context, userID, offerID string) (FolderFirstBatch, error) {
	if s == nil || s.store == nil || s.deps.InboxRetry == nil {
		return FolderFirstBatch{}, ErrFolderOutcomeUnavailable
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderFirstBatch{}, err
	}
	offer, err := inboxHire(doc, offerID)
	if err != nil {
		return FolderFirstBatch{}, err
	}
	if offer.Outcome.FirstLook.Batch.Outcome != FolderFirstBatchFailed {
		return *offer.Outcome.FirstLook.Batch, ErrFirstBatchNotFailed
	}
	claim := "first-batch:" + offer.ID
	if !s.claimRun(claim) {
		return *offer.Outcome.FirstLook.Batch, ErrFirstBatchRetryBusy
	}
	defer s.releaseRun(claim)
	attempt := offer.Outcome.FirstLook.Batch.Attempts + 1
	if attempt > folderFirstBatchMaxAttempts {
		return *offer.Outcome.FirstLook.Batch, ErrFolderOutcomeUnavailable
	}
	if _, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		current, err := inboxHire(*d, offer.ID)
		if err != nil {
			return err
		}
		batch := d.Offer(current.ID).Outcome.FirstLook.Batch
		if batch.Outcome != FolderFirstBatchFailed || batch.Attempts != attempt-1 {
			return ErrFirstBatchNotFailed
		}
		batch.Attempts = attempt
		return nil
	}); err != nil {
		return FolderFirstBatch{}, err
	}
	result, err := s.deps.InboxRetry(ctx, userID, *offer, attempt)
	if err != nil {
		return FolderFirstBatch{}, err
	}
	result.Attempts, result.DismissedAt = attempt, nil
	if err := result.validate(); err != nil {
		return FolderFirstBatch{}, err
	}
	now := s.now()
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		current, err := inboxHire(*d, offer.ID)
		if err != nil {
			return err
		}
		look := d.Offer(current.ID).Outcome.FirstLook
		replaced := result
		look.Batch, look.At = &replaced, now
		return nil
	})
	if err != nil {
		return FolderFirstBatch{}, err
	}
	return result, nil
}
