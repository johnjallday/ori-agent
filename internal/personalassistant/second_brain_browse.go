package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

var (
	ErrSecondBrainBrowseStale   = errors.New("personal assistant: notes connection changed")
	ErrSecondBrainBrowseMissing = errors.New("personal assistant: notes folder unavailable")
)

// browseReference is a generation check, not an authorization credential.
// It is only compared with the authenticated user's current connection.
func browseReference(doc SecondBrainDocument) string {
	source := doc.Connected()
	if source == nil {
		return ""
	}
	parts := []string{doc.Owner.UserID, doc.Owner.AssistantID, doc.Owner.HQWorkspaceID,
		source.ID, source.RequestID, source.ConnectedAt.UTC().Format(time.RFC3339Nano)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

type SecondBrainDirectory struct {
	secondbrain.Directory
}

func (s *SecondBrainService) Browse(ctx context.Context, userID, ref string, parts []string) (SecondBrainDirectory, error) {
	if len(ref) != 64 || len(parts) > secondbrain.BrowseMaxDepth {
		return SecondBrainDirectory{}, ErrValidation
	}
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainDirectory{}, err
	}
	source := doc.Connected()
	if source == nil {
		return SecondBrainDirectory{}, ErrSecondBrainNotConnected
	}
	if ref != browseReference(doc) {
		return SecondBrainDirectory{}, ErrSecondBrainBrowseStale
	}
	listing, err := secondbrain.ListDirectory(ctx, source.Path, parts)
	switch {
	case errors.Is(err, secondbrain.ErrBrowsePath):
		return SecondBrainDirectory{}, ErrValidation
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, secondbrain.ErrNotDirectory), errors.Is(err, secondbrain.ErrOutsideOwnedFolders):
		return SecondBrainDirectory{}, ErrSecondBrainBrowseMissing
	case err != nil:
		return SecondBrainDirectory{}, ErrSecondBrainUnavailable
	}
	if err := s.checkBrowseReference(ctx, userID, ref); err != nil {
		return SecondBrainDirectory{}, err
	}
	return SecondBrainDirectory{Directory: listing}, nil
}

func (s *SecondBrainService) checkBrowseReference(ctx context.Context, userID, ref string) error {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return err
	}
	if ref != browseReference(doc) {
		return ErrSecondBrainBrowseStale
	}
	return nil
}
