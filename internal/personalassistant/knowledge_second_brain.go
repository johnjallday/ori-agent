package personalassistant

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// SecondBrainSourceKind is the reviewed-knowledge source for the fact a
// connected notes folder teaches: where the user keeps their notes (FR16).
const SecondBrainSourceKind = "second_brain"

// SecondBrainFactText is the fact connecting proposes (FR16).
func SecondBrainFactText(kind, name string) string {
	if kind == secondbrain.KindObsidian {
		return `You keep your notes in Obsidian, in the vault "` + name + `".`
	}
	return "You keep your notes in a Markdown folder called " + name + "."
}

// SecondBrainKnowledgeProposal is the one proposal a connected source makes.
// Its meaning is the source itself, so connecting the same folder again is the
// same fact and another folder is a new one.
func SecondBrainKnowledgeProposal(source secondbrain.Source) KnowledgeProposal {
	return KnowledgeProposal{
		SourceKind: SecondBrainSourceKind, ScopeID: source.ID, SubjectID: "notes",
		Predicate: "keeps notes in", Value: source.Kind, Category: "how_you_work",
		Text: SecondBrainFactText(source.Kind, source.Name),
		Evidence: []KnowledgeEvidence{{
			SourceKind: SecondBrainSourceKind, SourceID: source.ID,
			ObservedAt: source.ConnectedAt.UTC(), Summary: "Notes folder connected to Personal HQ",
		}},
	}
}

// SecondBrainKnowledge learns from the connection: it proposes and approves
// the how-you-work fact when a folder is connected, and sends it back to
// review when the folder is disconnected (FR16, FR37).
type SecondBrainKnowledge struct {
	learning *KnowledgeLearningService
}

// NewSecondBrainKnowledge builds the producer over the lifecycle service.
func NewSecondBrainKnowledge(learning *KnowledgeLearningService) *SecondBrainKnowledge {
	return &SecondBrainKnowledge{learning: learning}
}

func secondBrainRequestID(action, sourceID string) string {
	return "second-brain-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("second-brain:"+action+":"+sourceID)).String()
}

// Learn proposes the fact and approves it: connecting the folder was the
// user's own reviewed choice, as accepting a folder offer is. A paused
// assistant, a full queue, or an earlier "forget this" leaves it unlearned.
func (k *SecondBrainKnowledge) Learn(ctx context.Context, userID string, source secondbrain.Source) {
	if k == nil || k.learning == nil {
		return
	}
	item, _, err := k.learning.Propose(ctx, userID, SecondBrainKnowledgeProposal(source))
	if err != nil {
		if !errors.Is(err, ErrKnowledgeSuppressed) && !errors.Is(err, ErrKnowledgePaused) {
			logger.Debug("Second-brain fact not proposed", logger.Fields{"error": err.Error()})
		}
		return
	}
	if item.State != KnowledgeCandidate {
		return
	}
	if _, err := k.learning.ApproveCandidate(ctx, userID, item.ID, item.Version, secondBrainRequestID("approve", source.ID+source.ConnectedAt.String())); err != nil {
		logger.Debug("Second-brain fact proposed but not approved", logger.Fields{"error": err.Error()})
	}
}

// Unlearn marks the approved fact Needs review once its folder is
// disconnected: it is no longer true that Ori reads notes there.
func (k *SecondBrainKnowledge) Unlearn(ctx context.Context, userID string, source secondbrain.Source) {
	if k == nil || k.learning == nil || k.learning.store == nil {
		return
	}
	binding, err := k.learning.store.resolve(ctx, userID)
	if err != nil {
		return
	}
	key := KnowledgeSemanticKey(binding, SecondBrainKnowledgeProposal(source))
	doc, err := k.learning.store.Read(ctx, userID)
	if err != nil {
		return
	}
	for _, item := range doc.Items {
		if !knowledgeItemMatches(item, key) || item.State != KnowledgeApproved {
			continue
		}
		requestID := secondBrainRequestID("suspend", source.ID+source.ConnectedAt.String())
		if _, err := k.learning.SuspendApproved(ctx, userID, item.ID, item.Version, requestID); err != nil {
			logger.Debug("Second-brain fact not sent back to review", logger.Fields{"error": err.Error()})
		}
		return
	}
}
