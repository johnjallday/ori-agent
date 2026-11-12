package sessionhttp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

// folderAgentScanAttempts is how often a scan review is retried when the
// Home's library revision moved under it (a song-facts pass or the Library
// Manager's turn writes to the Home after every scan).
const folderAgentScanAttempts = 4

// maxFolderAgentScopePages bounds one listing of a root's known scopes.
const maxFolderAgentScopePages = 64

// FolderAgentScopes lists the scopes the library itself issued for a root
// (its own scans' known folders), relative to the root. Scope ids never come
// from a client path.
//
// The listing is bound to one revision of the Home's library, and the Library
// Manager's turn writes to the Home right after every scan commits, which is
// exactly when a listing asks for the scopes. A moved revision is a busy moment,
// not a failure, so the whole listing is read again (a few times, a beat apart)
// before it is given up; giving up on the first conflict left "Learn them" with
// nothing to scan and a reading that looked finished.
func (h *Handler) FolderAgentScopes(ownerUserID, homeID, rootID string) ([]projectlibrary.ScanScopeChoice, error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil || h.assistantLibraryRoots == nil {
		return nil, ErrPortfolioSetupUnavailable
	}
	return retryLibraryRead(folderAgentScopeAttempts, folderAgentScopePause, func() ([]projectlibrary.ScanScopeChoice, error) {
		doc, err := h.assistantLibraryStore().Read(scope)
		if err != nil {
			return nil, err
		}
		var out []projectlibrary.ScanScopeChoice
		offset := 0
		for range maxFolderAgentScopePages {
			page, err := h.assistantLibraryRoots.ListScanScopes(scope, rootID, doc.Revision, offset)
			if err != nil {
				return out, err
			}
			out = append(out, page.Rows...)
			if page.NextOffset <= offset || len(page.Rows) == 0 {
				break
			}
			offset = page.NextOffset
		}
		return out, nil
	})
}

// Reading a Home's scopes again after its revision moved: how often, and how
// long a beat to leave (the writer that moved it is a short write).
const (
	folderAgentScopeAttempts = 6
	folderAgentScopePause    = 150 * time.Millisecond
)

// retryLibraryRead runs read until it succeeds or fails with something other
// than a busy Home (a moved revision, or the fenced state briefly unavailable),
// at most attempts times with a growing pause between them. The last error is
// returned when every attempt met a busy Home.
func retryLibraryRead[T any](attempts int, pause time.Duration, read func() (T, error)) (T, error) {
	var value T
	var err error
	for attempt := range attempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * pause)
		}
		value, err = read()
		if !retryableLibraryError(err) {
			return value, err
		}
	}
	return value, err
}

// FolderAgentSongs lists the songs the library holds under one root with what
// was learned from each (numbers, and names under a consent that covers them),
// for the folder agent's own turns. Nothing else is given this: it is not a
// route, and the Manager's tools never reach it. The Home is resolved for the
// owner, as for every other folder-agent library call.
func (h *Handler) FolderAgentSongs(ownerUserID, homeID, rootID string) ([]projectlibrary.LearnedSong, error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil {
		return nil, ErrPortfolioSetupUnavailable
	}
	return h.assistantLibraryStore().LearnedSongsInRoot(scope, rootID)
}

// ScanFolderAgentRoot reviews and commits one whole-root listing under the
// owner's Set it up, re-reading the library's revision before each attempt.
// It returns how many projects the library lists under the root.
func (h *Handler) ScanFolderAgentRoot(ctx context.Context, ownerUserID, homeID, rootID, key string) (int, bool, error) {
	var lastErr error
	for attempt := range folderAgentScanAttempts {
		if !folderAgentBackoff(ctx, attempt) {
			return 0, false, ctx.Err()
		}
		review, err := h.ReviewPortfolioScan(ownerUserID, homeID, rootID)
		if retryableLibraryError(err) {
			lastErr = err
			continue
		}
		if err != nil {
			return 0, false, err
		}
		listed, partial, err := h.CommitPortfolioScan(ctx, ownerUserID, homeID, rootID, review.Token, fmt.Sprintf("%s-%d", key, attempt))
		if !retryableLibraryError(err) {
			return listed, partial, err
		}
		lastErr = err
	}
	return 0, false, lastErr
}

// retryableLibraryError is a moment the Home is busy: its revision moved (a
// song-facts pass or the Manager's turn wrote after the last scan), or its
// fenced state is briefly unavailable while that write runs.
func retryableLibraryError(err error) bool {
	return errors.Is(err, projectlibrary.ErrConflict) || errors.Is(err, projectlibrary.ErrUnavailable)
}

// folderAgentBackoff waits before a retry (none before the first attempt).
func folderAgentBackoff(ctx context.Context, attempt int) bool {
	if attempt == 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(time.Duration(attempt) * 750 * time.Millisecond):
		return true
	}
}

// ScanFolderAgentScope reviews and commits one scoped listing for a scope the
// library issued, with the same revision handling. It returns how many
// projects the library lists under the root afterwards.
func (h *Handler) ScanFolderAgentScope(ctx context.Context, ownerUserID, homeID, rootID, scopeID, key string) (int, bool, error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil || h.assistantLibraryRoots == nil {
		return 0, false, ErrPortfolioSetupUnavailable
	}
	var lastErr error
	for attempt := range folderAgentScanAttempts {
		if !folderAgentBackoff(ctx, attempt) {
			return 0, false, ctx.Err()
		}
		doc, err := h.assistantLibraryStore().Read(scope)
		if err != nil {
			return 0, false, err
		}
		review, err := h.assistantLibraryRoots.ReviewScopedScan(scope, rootID, scopeID, doc.Revision)
		if retryableLibraryError(err) {
			lastErr = err
			continue
		}
		if err != nil {
			return 0, false, err
		}
		scan, _, err := h.assistantLibraryRoots.CommitScopedScan(ctx, scope, rootID, scopeID, review.Token, fmt.Sprintf("%s-%d", key, attempt))
		if retryableLibraryError(err) {
			lastErr = err
			continue
		}
		if err != nil {
			return 0, false, err
		}
		if scan.Status != "complete" && scan.Status != "partial" {
			return 0, false, fmt.Errorf("%w: the listing ended %s", ErrPortfolioSetupUnavailable, scan.Status)
		}
		after, err := h.assistantLibraryStore().Read(scope)
		if err != nil {
			return 0, false, err
		}
		return listedUnder(after, rootID), listingPartial(scan), nil
	}
	return 0, false, lastErr
}
