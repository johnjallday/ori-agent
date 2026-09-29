package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ScanReview authorizes exactly one bounded, metadata-only root scan. A new
// explicit review is required even when the same root was scanned before.
type ScanReview struct {
	Token               string    `json:"token"`
	RootID              string    `json:"root_id"`
	RootPath            string    `json:"root_path"` // Authenticated human review only.
	Scope               string    `json:"scope"`
	ScopeID             string    `json:"scope_id,omitempty"`
	RelativeFolder      string    `json:"relative_folder,omitempty"`
	MaxEntries          int       `json:"max_entries"`
	IncludesScan        bool      `json:"includes_scan"`
	InterruptsPriorScan bool      `json:"interrupts_prior_scan"`
	ExpiresAt           time.Time `json:"expires_at"`
	Revision            int64     `json:"revision"`
}

func scanDigest(scope Scope, root Root, expected int64, selected *ScanScope) string {
	action := "scan_root:" + strconv.FormatInt(expected, 10)
	if selected != nil {
		action += ":scope:" + selected.ID + ":" + selected.RelativeFolder + ":" + selected.FileIdentity
	}
	return rootDigest(scope, action, root.ID, root.FileIdentity, root.Revision)
}

func findScanScope(doc Document, rootID, scopeID string, rootRevision int64) (*ScanScope, bool) {
	if scopeID == "" {
		return nil, true
	}
	for _, scan := range doc.Scans {
		if scan.RootID != rootID || scan.RootRevision != rootRevision ||
			(scan.Status != "complete" && scan.Status != "partial") {
			continue
		}
		for _, known := range scan.KnownScopes {
			if known.ID == scopeID {
				copy := known
				return &copy, true
			}
		}
	}
	return nil, false
}

// ScanScopePage exposes only verified, previously observed folder names to an
// authenticated Home UI. It neither reads the filesystem nor grants a scan;
// every choice still requires a fresh reviewed scope ID at commit time.
type ScanScopeChoice struct {
	ID             string `json:"id"`
	RelativeFolder string `json:"relative_folder"`
}

type ScanScopePage struct {
	RootID     string            `json:"root_id"`
	Revision   int64             `json:"revision"`
	Total      int               `json:"total"`
	Rows       []ScanScopeChoice `json:"rows"`
	NextOffset int               `json:"next_offset"`
}

// ListScanScopes returns a revision-bound page of the latest known identity
// for each relative folder. Earlier observations stay in history but must not
// appear as a second choice when a later scan saw a replacement at that name.
func (r *Roots) ListScanScopes(scope Scope, rootID string, expected int64, offset int) (ScanScopePage, error) {
	if r == nil || r.library == nil || rootID == "" || offset < 0 || offset > maxScans*maxKnownScopes {
		return ScanScopePage{}, ErrLimit
	}
	doc, state, err := r.library.readSnapshot(scope)
	if err != nil {
		return ScanScopePage{}, err
	}
	if expected != doc.Revision {
		return ScanScopePage{}, ErrConflict
	}
	var root *Root
	for i := range doc.Roots {
		if doc.Roots[i].ID == rootID && doc.Roots[i].RevokedAt == nil {
			root = &doc.Roots[i]
			break
		}
	}
	if root == nil {
		return ScanScopePage{}, ErrUnavailable
	}
	for _, inactive := range state.ProjectLibraryInactiveRoots {
		if inactive == rootID {
			return ScanScopePage{}, ErrUnavailable
		}
	}
	seen := make(map[string]bool)
	choices := make([]ScanScopeChoice, 0)
	for i := len(doc.Scans) - 1; i >= 0; i-- {
		scan := doc.Scans[i]
		if scan.RootID != rootID || scan.RootRevision != root.Revision ||
			(scan.Status != "complete" && scan.Status != "partial") {
			continue
		}
		for _, known := range scan.KnownScopes {
			if !seen[known.RelativeFolder] {
				seen[known.RelativeFolder] = true
				choices = append(choices, ScanScopeChoice{ID: known.ID, RelativeFolder: known.RelativeFolder})
			}
		}
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].RelativeFolder < choices[j].RelativeFolder })
	page := ScanScopePage{RootID: rootID, Revision: doc.Revision, Total: len(choices), Rows: []ScanScopeChoice{}}
	if offset < len(choices) {
		end := offset + 20
		if end > len(choices) {
			end = len(choices)
		}
		page.Rows = append(page.Rows, choices[offset:end]...)
		if end < len(choices) {
			page.NextOffset = end
		}
	}
	return page, nil
}

func (r *Roots) ReviewScan(scope Scope, rootID string, expected int64) (ScanReview, error) {
	return r.reviewScan(scope, rootID, "", expected)
}

// ReviewScopedScan accepts only a durable server-issued scope ID from a prior
// explicitly reviewed scan, never a browser-supplied relative path.
func (r *Roots) ReviewScopedScan(scope Scope, rootID, scopeID string, expected int64) (ScanReview, error) {
	if scopeID == "" {
		return ScanReview{}, ErrConflict
	}
	return r.reviewScan(scope, rootID, scopeID, expected)
}

func (r *Roots) reviewScan(scope Scope, rootID, scopeID string, expected int64) (ScanReview, error) {
	doc, err := r.writable(scope)
	if err != nil {
		return ScanReview{}, err
	}
	root, err := r.VerifyConnectedRoot(scope, rootID)
	if err != nil {
		return ScanReview{}, err
	}
	selected, found := findScanScope(doc, rootID, scopeID, root.Revision)
	if doc.Revision != expected || !found {
		return ScanReview{}, ErrConflict
	}
	interrupts := false
	for _, prior := range doc.Scans {
		interrupts = interrupts || (prior.RootID == rootID && prior.Status == "running")
	}
	providerRevision, err := r.currentProviderRevision(scope)
	if err != nil {
		return ScanReview{}, err
	}
	at := r.library.now().UTC()
	action := "scan_root"
	relative := ""
	if selected != nil {
		action = "scan_scope"
		relative = selected.RelativeFolder
	}
	review := ReviewReceipt{Token: newID(), Action: action, Digest: scanDigest(scope, root, expected, selected),
		Revision: expected + 1, ProviderRevision: providerRevision, ExpiresAt: at.Add(10 * time.Minute)}
	_, _, err = r.library.mutateWithPolicy(scope, expected,
		operation{key: review.Token, action: "review_scan", digest: review.Digest},
		func(state *workspace.AssistantProgramState) bool {
			return state.PluginAvailable && state.StateRevision == providerRevision
		}, func(current *Document) (string, error) {
			if len(current.Reviews) >= maxReviews {
				return "", ErrLimit
			}
			current.Reviews = append(current.Reviews, review)
			return review.Token, nil
		})
	if err != nil {
		return ScanReview{}, err
	}
	return ScanReview{Token: review.Token, RootID: rootID, RootPath: root.Path, ScopeID: scopeID,
		RelativeFolder: relative,
		Scope:          "selected folder and immediate child names/project markers (no file contents)",
		MaxEntries:     scanEntryLimit, IncludesScan: true, InterruptsPriorScan: interrupts,
		ExpiresAt: review.ExpiresAt, Revision: review.Revision}, nil
}

// CommitScan first records running+receipt in one Home update, then scans
// outside the workspace lock. A crash at either side of the scan leaves a
// visible running record, never a falsely complete inventory. A retry of the
// same operation key returns that record without silently scanning again.
func (r *Roots) CommitScan(ctx context.Context, scope Scope, rootID, token, key string) (Scan, bool, error) {
	return r.commitScan(ctx, scope, rootID, "", token, key)
}

func (r *Roots) CommitScopedScan(ctx context.Context, scope Scope, rootID, scopeID, token, key string) (Scan, bool, error) {
	if scopeID == "" {
		return Scan{}, false, ErrConflict
	}
	return r.commitScan(ctx, scope, rootID, scopeID, token, key)
}

func (r *Roots) commitScan(ctx context.Context, scope Scope, rootID, scopeID, token, key string) (Scan, bool, error) {
	doc, err := r.writable(scope)
	if err != nil {
		return Scan{}, false, err
	}
	action := "scan_root"
	if scopeID != "" {
		action = "scan_scope"
	}
	review, ok := findReview(doc, token, action)
	if !ok || key == "" || !validText(key, 160) {
		return Scan{}, false, ErrConflict
	}
	for _, op := range doc.Operations {
		if op.Key != key {
			continue
		}
		if op.Action != "scan_start" || op.Digest != review.Digest {
			return Scan{}, false, ErrConflict
		}
		for _, prior := range doc.Scans {
			if prior.ID == op.ConsequenceID && prior.RootID == rootID && prior.ScopeID == scopeID {
				return prior, true, nil
			}
		}
		return Scan{}, false, ErrCorrupt
	}
	if err := ctx.Err(); err != nil {
		return Scan{}, false, err
	}
	root, err := r.VerifyConnectedRoot(scope, rootID)
	if err != nil {
		return Scan{}, false, err
	}
	selected, found := findScanScope(doc, rootID, scopeID, root.Revision)
	if !found || review.ConsumedAt != nil ||
		!review.ExpiresAt.After(r.library.now().UTC()) || review.Revision != doc.Revision ||
		review.Digest != scanDigest(scope, root, review.Revision-1, selected) {
		return Scan{}, false, ErrConflict
	}
	if selected != nil {
		if _, _, checkErr := r.readDirectoryWithIdentity(ctx, scope, rootID,
			selected.RelativeFolder, selected.FileIdentity, 1); checkErr != nil {
			return Scan{}, false, ErrUnavailable
		}
	}
	providerRevision, err := r.currentProviderRevision(scope)
	if err != nil || providerRevision != review.ProviderRevision {
		return Scan{}, false, ErrConflict
	}
	scan := Scan{ID: newID(), RootID: rootID, RootRevision: root.Revision,
		RootDigest:       rootDigest(scope, "scan_source", root.Path, root.FileIdentity, root.Revision),
		ProviderRevision: providerRevision, Status: "running", StartedAt: r.library.now().UTC()}
	if selected != nil {
		scan.Scope, scan.ScopeID, scan.ScopeIdentity = selected.RelativeFolder, selected.ID, selected.FileIdentity
	}
	op, replay, err := r.library.mutateWithPolicy(scope, doc.Revision,
		operation{key: key, action: "scan_start", digest: review.Digest},
		func(state *workspace.AssistantProgramState) bool {
			return state.PluginAvailable && state.StateRevision == providerRevision
		}, func(current *Document) (string, error) {
			if len(current.Scans) >= maxScans {
				return "", ErrLimit
			}
			if selected != nil {
				liveScope, found := findScanScope(*current, rootID, scopeID, root.Revision)
				if !found || liveScope == nil || *liveScope != *selected {
					return "", ErrConflict
				}
			}
			active := false
			for _, previous := range current.Scans {
				active = active || (previous.RootID == rootID && previous.Status == "running")
			}
			for i := range current.Reviews {
				item := &current.Reviews[i]
				if item.Token == token && item.Action == action && item.Digest == review.Digest &&
					item.Revision == current.Revision && item.ConsumedAt == nil && item.ExpiresAt.After(r.library.now().UTC()) {
					if !currentRootMatches(current, root) {
						return "", ErrConflict
					}
					if _, liveID, checkErr := r.pickedRoot(root.Path); checkErr != nil || liveID != root.FileIdentity {
						return "", ErrUnavailable
					}
					now := r.library.now().UTC()
					item.ConsumedAt = &now
					if active {
						for j := range current.Scans {
							if current.Scans[j].RootID == rootID && current.Scans[j].Status == "running" {
								current.Scans[j].Status = "interrupted"
								current.Scans[j].FinishedAt = &now
								current.Scans[j].PartialReason = "superseded_by_new_review"
							}
						}
					}
					current.Scans = append(current.Scans, scan)
					return scan.ID, nil
				}
			}
			return "", ErrConflict
		})
	if err != nil {
		return Scan{}, false, err
	}
	if replay {
		for _, prior := range doc.Scans {
			if prior.ID == op.ConsequenceID {
				return prior, true, nil
			}
		}
		return Scan{}, false, ErrConflict
	}
	var observed Discovery
	var scanErr error
	if selected == nil {
		observed, scanErr = r.Discover(ctx, scope, rootID)
	} else {
		observed, scanErr = r.discoverAt(ctx, scope, root, selected.RelativeFolder, selected.FileIdentity)
	}
	status := "complete"
	reason := observed.PartialReason
	if scanErr != nil {
		status = "failed"
		reason = "root_unavailable"
		if errors.Is(scanErr, context.Canceled) || errors.Is(scanErr, context.DeadlineExceeded) {
			status, reason = "cancelled", "cancelled"
		}
	} else if reason != "" {
		status = "partial"
	}
	finished, finishErr := r.finishScan(scope, scan, observed, status, reason)
	if finishErr != nil {
		// A failed finalization leaves a durable running/interrupted record;
		// never claim the client-visible result was persisted.
		return Scan{}, false, finishErr
	}
	return finished, false, nil
}

func currentRootMatches(doc *Document, root Root) bool {
	for _, current := range doc.Roots {
		if current.ID == root.ID && current.RevokedAt == nil && current.Revision == root.Revision &&
			current.Path == root.Path && current.FileIdentity == root.FileIdentity {
			return true
		}
	}
	return false
}

func (r *Roots) finishScan(scope Scope, started Scan, observed Discovery, status, reason string) (Scan, error) {
	if status != "complete" && status != "partial" && status != "failed" && status != "cancelled" {
		return Scan{}, ErrConflict
	}
	if status == "complete" || status == "partial" {
		if observed.RootID != started.RootID || observed.RootRevision != started.RootRevision ||
			observed.Scope != started.Scope || observed.EntriesSeen > scanEntryLimit || observed.EntriesSeen < 0 {
			return Scan{}, ErrCorrupt
		}
	}
	payload, err := json.Marshal(observed)
	if err != nil {
		return Scan{}, ErrCorrupt
	}
	digest := sha256.Sum256(payload)
	encodedDigest := hex.EncodeToString(digest[:])
	doc, err := r.library.Read(scope)
	if err != nil {
		return Scan{}, err
	}
	// The digest's setup count needs the host's installed list. Read it
	// before taking the Home lock; the blueprint match itself is evaluated
	// against the Home state inside the update.
	var installed []plugin.InstalledPlugin
	var listErr error
	if r.library.installed != nil && (status == "complete" || status == "partial") {
		installed, listErr = r.library.installed.List()
	}
	// Finalizing a failed/interrupted scan must remain possible after provider
	// loss; check installed evidence inside the Home update before publishing
	// candidates, not against a snapshot captured ahead of the update.
	var finished Scan
	var completed *LibraryDigest
	var providerUnchanged bool
	var evidence setupEvidence
	_, replay, err := r.library.mutateWithHomePolicy(scope, doc.Revision,
		operation{key: started.ID + ":finish", action: "scan_finish", digest: encodedDigest},
		func(state *workspace.AssistantProgramState, home *workspace.Workspace) bool {
			providerUnchanged = state.StateRevision == started.ProviderRevision && r.library.providerWritable(scope, home)
			evidence = r.library.setupEvidence(scope, state, installed, listErr)
			return true // Record failure/cancellation even after provider loss.
		}, func(current *Document) (string, error) {
			completed = nil
			var before []Entry
			var scan *Scan
			for i := range current.Scans {
				if current.Scans[i].ID == started.ID && current.Scans[i].RootID == started.RootID &&
					current.Scans[i].Status == "running" {
					scan = &current.Scans[i]
					break
				}
			}
			var source *Root
			for i := range current.Roots {
				if current.Roots[i].ID == started.RootID {
					source = &current.Roots[i]
					break
				}
			}
			if scan == nil {
				// A newer explicitly reviewed scan may have marked this one
				// interrupted; late results must never overwrite its outcome.
				return "", ErrConflict
			}
			if status == "complete" || status == "partial" {
				switch {
				case source == nil || source.RevokedAt != nil || source.Revision != started.RootRevision ||
					rootDigest(scope, "scan_source", source.Path, source.FileIdentity, source.Revision) != started.RootDigest:
					status, reason = "failed", "root_changed_during_scan"
				case !providerUnchanged:
					status, reason = "failed", "provider_changed_during_scan"
				default:
					if !directoryMatches(*source, started.Scope, started.ScopeIdentity) {
						status, reason = "failed", "scope_unavailable_during_scan"
					} else {
						prepared := *scan
						if appendKnownScopes(&prepared, observed) {
							scan.SkippedOther++
							if status == "complete" {
								status, reason = "partial", "scope_limit_or_invalid"
							}
						}
						entries, cloneErr := cloneEntries(current.Entries)
						if cloneErr != nil {
							status, reason = "failed", "catalog_evidence_invalid_or_limit"
						} else {
							preparedDoc := *current
							preparedDoc.Entries = entries
							covered := observed
							if status == "partial" {
								covered.PartialReason = reason
							}
							if reconcileErr := reconcileCandidates(&preparedDoc, started, covered, *source,
								r.library.workspaces, scope); reconcileErr != nil {
								status, reason = "failed", "catalog_evidence_invalid_or_limit"
							} else {
								before = current.Entries
								current.Entries = preparedDoc.Entries
								scan.KnownScopes = prepared.KnownScopes
							}
						}
					}
				}
			}
			now := r.library.now().UTC()
			scan.Status, scan.FinishedAt = status, &now
			scan.EntriesSeen, scan.SkippedLinks = observed.EntriesSeen, observed.SkippedLinks
			scan.SkippedOther += observed.SkippedOther
			scan.PartialReason, scan.ResultDigest = reason, encodedDigest
			finished = *scan
			if status == "complete" || status == "partial" {
				// Written in this same fenced update, so the digest can never
				// describe results the Home did not also record.
				digest := buildLibraryDigest(before, current.Entries, *scan, now, status, evidence)
				current.Digest = &digest
				completed = &digest
			}
			return scan.ID, nil
		})
	if err == nil && !replay && completed != nil {
		r.library.publishScanCompleted(scope, *completed)
	}
	return finished, err
}

func appendKnownScopes(scan *Scan, observed Discovery) (skipped bool) {
	seen := make(map[string]bool, len(observed.KnownScopes))
	for _, scope := range observed.KnownScopes {
		if !validRelative(scope.RelativeFolder) || scope.RelativeFolder == "" || scope.FileIdentity == "" ||
			(scan.Scope != "" && !strings.HasPrefix(scope.RelativeFolder, scan.Scope+string(filepath.Separator))) ||
			seen[scope.RelativeFolder] || len(scan.KnownScopes) >= maxKnownScopes {
			skipped = true
			continue
		}
		seen[scope.RelativeFolder] = true
		scan.KnownScopes = append(scan.KnownScopes, ScanScope{
			ID: newID(), RelativeFolder: scope.RelativeFolder, FileIdentity: scope.FileIdentity})
	}
	return skipped
}
