package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

var errSecondBrainSourceUnavailable = errors.New("server: the notes folder is no longer connected")

// secondBrainAuthority revalidates the fact a connected notes folder taught
// (FR16): the folder must still be the one connected to this HQ, still exist,
// and still hold Obsidian's folder (a vault) or Markdown notes (a folder).
// It lists directories only and opens no note.
type secondBrainAuthority struct {
	brain secondBrainSources
}

// secondBrainSources is the slice of the second-brain service the authority
// reads: the folder connected now.
type secondBrainSources interface {
	ConnectedSource(ctx context.Context, userID string) (secondbrain.Source, bool, error)
}

func (a *secondBrainAuthority) Revalidate(ctx context.Context, binding personalassistant.KnowledgeBinding, item personalassistant.KnowledgeItem) error {
	if a == nil || a.brain == nil || item.SourceKind != personalassistant.SecondBrainSourceKind {
		return errSecondBrainSourceUnavailable
	}
	source, connected, err := a.brain.ConnectedSource(ctx, binding.UserID)
	if err != nil || !connected {
		return errSecondBrainSourceUnavailable
	}
	if personalassistant.KnowledgeSemanticKey(binding, personalassistant.SecondBrainKnowledgeProposal(source)) != item.SemanticKey {
		return errSecondBrainSourceUnavailable
	}
	var evidence []personalassistant.KnowledgeEvidence
	for _, revision := range item.Revisions {
		if revision.ID == item.CurrentRevisionID {
			evidence = revision.Evidence
			break
		}
	}
	if len(evidence) != 1 || evidence[0].SourceKind != personalassistant.SecondBrainSourceKind || evidence[0].SourceID != source.ID {
		return errSecondBrainSourceUnavailable
	}
	switch source.Kind {
	case secondbrain.KindObsidian:
		if !secondbrain.IsObsidianVault(source.Path) {
			return errSecondBrainSourceUnavailable
		}
	default:
		if !secondbrain.HasNotes(source.Path) {
			return errSecondBrainSourceUnavailable
		}
	}
	return nil
}
