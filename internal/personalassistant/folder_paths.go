package personalassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

const (
	folderPathsVersion    = 1
	folderPathsMaxEntries = 64
	folderPathsMaxBytes   = 1024 * 1024
)

// These types are server-private authority, never part of an offer, view,
// event, or model context. The digest remains path-free.
type folderPathEntry struct {
	RootPath          string    `json:"root_path"`
	RootIdentity      string    `json:"root_identity"`
	CandidatePath     string    `json:"candidate_path"`
	CandidateIdentity string    `json:"candidate_identity"`
	SavedAt           time.Time `json:"saved_at"`
}

type folderPathDocument struct {
	Version int                        `json:"version"`
	Owner   KnowledgeOwner             `json:"owner"`
	Entries map[string]folderPathEntry `json:"entries"`
}

func openFolderReview(offer FolderOffer) bool {
	return offer.Status == FolderOfferPending || offer.Status == FolderOfferAwaitingOutcome
}

func decodeFolderPaths(data []byte, owner KnowledgeOwner) (folderPathDocument, error) {
	if len(data) == 0 {
		return folderPathDocument{Version: folderPathsVersion, Owner: owner, Entries: make(map[string]folderPathEntry)}, nil
	}
	var doc folderPathDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > folderPathsMaxBytes || decoder.Decode(&doc) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		doc.Version != folderPathsVersion || doc.Owner != owner || doc.Entries == nil || len(doc.Entries) > folderPathsMaxEntries {
		return folderPathDocument{}, ErrKnowledgeCorrupt
	}
	for id, entry := range doc.Entries {
		if id == "" || len(id) > 128 || !validFolderPathEntry(entry) {
			return folderPathDocument{}, ErrKnowledgeCorrupt
		}
	}
	return doc, nil
}

func validFolderPathEntry(entry folderPathEntry) bool {
	validPath := func(path string) bool {
		return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsRune(path, 0)
	}
	return validPath(entry.RootPath) && validPath(entry.CandidatePath) &&
		entry.RootIdentity != "" && len(entry.RootIdentity) <= 256 &&
		entry.CandidateIdentity != "" && len(entry.CandidateIdentity) <= 256 && !entry.SavedAt.IsZero()
}

func encodeFolderPaths(doc folderPathDocument) ([]byte, error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, ErrKnowledgeCorrupt
	}
	if _, err := decodeFolderPaths(data, doc.Owner); err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func folderPathMatches(offer FolderOffer, entry folderPathEntry) bool {
	identity := offer.SubjectIdentity
	if offer.ConversationReview != nil {
		identity = offer.ConversationReview.CandidateIdentity
	}
	return openFolderReview(offer) && FolderKey(entry.RootPath) == offer.FolderKey &&
		entry.RootIdentity == offer.RootIdentity && FolderKey(entry.CandidatePath) == offer.Subject.Key &&
		entry.CandidateIdentity == identity
}

// Pruning runs under the digest lock before publishing its next revision. A
// failed digest write can lose a hold, but can never retain revoked authority.
func (doc *folderPathDocument) prune(offers FolderDigestDocument) bool {
	changed := false
	for id, entry := range doc.Entries {
		offer := offers.Offer(id)
		if offer == nil || !folderPathMatches(*offer, entry) {
			delete(doc.Entries, id)
			changed = true
		}
	}
	return changed
}

// put never evicts a held path to admit another review.
func (doc *folderPathDocument) put(id string, entry folderPathEntry) bool {
	if _, exists := doc.Entries[id]; !exists && len(doc.Entries) >= folderPathsMaxEntries {
		return false
	}
	doc.Entries[id] = entry
	return true
}

func (s *FolderDigestStore) saveFolderPath(ctx context.Context, userID, offerID string, entry folderPathEntry) error {
	if !validFolderPathEntry(entry) {
		return ErrKnowledgeCorrupt
	}
	return s.withFolderPaths(ctx, userID, func(offers FolderDigestDocument, paths *folderPathDocument) (bool, error) {
		offer := offers.Offer(offerID)
		if offer == nil || !folderPathMatches(*offer, entry) {
			return false, ErrConflict
		}
		changed := paths.prune(offers)
		if !paths.put(offerID, entry) {
			if _, warned := s.pathWarnings.LoadOrStore(paths.Owner, true); !warned {
				logger.Warn("Folder review path store is full; new reviews retain only their in-memory hold", logger.Fields{})
			}
			return changed, nil
		}
		return true, nil
	})
}

// readFolderPath requires the canonical offer still to own the saved witness.
// An old caller cannot revive a final, removed or re-targeted review.
func (s *FolderDigestStore) readFolderPath(ctx context.Context, userID string, offer FolderOffer) (folderPathEntry, bool, error) {
	var entry folderPathEntry
	var found bool
	err := s.withFolderPaths(ctx, userID, func(offers FolderDigestDocument, paths *folderPathDocument) (bool, error) {
		current := offers.Offer(offer.ID)
		entry, found = paths.Entries[offer.ID]
		found = found && current != nil && folderPathMatches(*current, entry) && folderPathMatches(offer, entry)
		return false, nil
	})
	if err != nil || !found {
		return folderPathEntry{}, false, err
	}
	return entry, true, nil
}
