package personalassistant

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// FolderScanProducer turns a resolved project offer into reviewed knowledge
// (PRD §4.7): one `projects` fact, approved in the same request because the
// user's yes is the explicit approval the pipeline requires (FR35), and
// candidate `how_you_work` facts for the tools the folder's marker or file
// kind names, which wait in the review queue like saved-app suggestions
// (FR36). Everything here is best-effort: the workspace is the durable
// result, and a full queue, a paused assistant, or a refused text only
// means no fact (FR37, FR39, FR52).
type FolderScanProducer struct {
	learning *KnowledgeLearningService
}

// NewFolderScanProducer builds the producer over the lifecycle service.
func NewFolderScanProducer(learning *KnowledgeLearningService) *FolderScanProducer {
	return &FolderScanProducer{learning: learning}
}

// folderScanEvidenceSummary words the evidence line: the source, and the
// marker when there is one.
func folderScanEvidenceSummary(record FolderCandidateRecord) string {
	if record.Marker != "" {
		return "Folder shown to the assistant; marker: " + record.Marker
	}
	if record.DominantExtension != "" {
		return "Folder shown to the assistant; kind: " + folderdigest.KindName(record.DominantExtension)
	}
	return "Folder shown to the assistant"
}

// folderScanApprovalID derives the approval's idempotency key from the offer,
// so a replayed resolve approves the same item once.
func folderScanApprovalID(offerID string) string {
	return "folder-scan-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("folder-scan:"+offerID)).String()
}

// LearnFromOffer proposes and approves the project fact, then proposes tool
// candidates. It returns what the offer card may say about it.
func (p *FolderScanProducer) LearnFromOffer(ctx context.Context, userID string, offer FolderOffer) FolderLearning {
	if p == nil || p.learning == nil || offer.Outcome == nil || offer.Outcome.Kind != FolderChoiceProject {
		return FolderLearning{}
	}
	subject := offer.Subject
	if strings.TrimSpace(subject.Key) == "" || strings.TrimSpace(subject.Name) == "" {
		return FolderLearning{}
	}
	learning := p.rememberProject(ctx, userID, offer)
	p.proposeTools(ctx, userID, offer)
	return learning
}

func (p *FolderScanProducer) rememberProject(ctx context.Context, userID string, offer FolderOffer) FolderLearning {
	subject := offer.Subject
	proposal := KnowledgeProposal{
		SourceKind: FolderScanSourceKind, ScopeID: offer.FolderKey, SubjectID: subject.Key,
		Predicate: "works on project", Value: subject.Key, Category: "projects",
		Text: FolderProjectFactText(subject.Name),
		Evidence: []KnowledgeEvidence{{
			SourceKind: FolderScanSourceKind, SourceID: subject.Key,
			ObservedAt: offer.ScannedAt.UTC(), Summary: folderScanEvidenceSummary(subject),
		}},
	}
	item, _, err := p.learning.Propose(ctx, userID, proposal)
	switch {
	case err == nil:
	case errors.Is(err, ErrKnowledgePaused):
		return FolderLearning{Note: "Automatic suggestions are paused, so I did not save this as a project."}
	case errors.Is(err, ErrKnowledgeQuota):
		logger.Debug("Folder scan fact skipped: review queue is full", logger.Fields{"offer_id": offer.ID})
		return FolderLearning{Note: "The review queue is full, so I did not save this as a project."}
	case errors.Is(err, ErrKnowledgeSuppressed):
		return FolderLearning{Note: "You had asked me not to remember this, so I did not."}
	case errors.Is(err, ErrValidation):
		return FolderLearning{Note: "I could not save this folder's name as a fact."}
	default:
		logger.Debug("Folder scan fact skipped", logger.Fields{"offer_id": offer.ID, "error": err.Error()})
		return FolderLearning{Note: "I could not save this as a project right now."}
	}
	if item.State == KnowledgeApproved {
		return FolderLearning{Remembered: true}
	}
	if item.State != KnowledgeCandidate {
		return FolderLearning{Note: "This project is already in your dossier for review."}
	}
	if _, err := p.learning.ApproveCandidate(ctx, userID, item.ID, item.Version, folderScanApprovalID(offer.ID)); err != nil {
		logger.Debug("Folder scan fact proposed but not approved", logger.Fields{"offer_id": offer.ID, "error": err.Error()})
		return FolderLearning{Note: "I saved this project for your review rather than remembering it outright."}
	}
	return FolderLearning{Remembered: true}
}

// proposeTools proposes one candidate per tool the marker or dominant file
// kind names, skipping tools the saved-app producer already proposed (FR36).
func (p *FolderScanProducer) proposeTools(ctx context.Context, userID string, offer FolderOffer) {
	subject := offer.Subject
	var tools []folderdigest.Tool
	if subject.MarkerName != "" {
		for _, m := range folderdigest.Markers {
			if m.Name == subject.MarkerName {
				if tool, ok := folderdigest.ToolForMarker(m); ok {
					tools = append(tools, tool)
				}
				break
			}
		}
	}
	if subject.DominantExtension != "" {
		if tool, ok := folderdigest.ToolForExtension(subject.DominantExtension); ok {
			tools = append(tools, tool)
		}
	}
	if len(tools) == 0 {
		return
	}
	binding, err := p.learning.store.resolve(ctx, userID)
	if err != nil {
		return
	}
	doc, err := p.learning.store.Read(ctx, userID)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool.ToolID] {
			continue
		}
		seen[tool.ToolID] = true
		if savedAppAlreadyProposed(binding, doc, tool) {
			continue
		}
		proposal := KnowledgeProposal{
			SourceKind: FolderScanSourceKind, ScopeID: offer.FolderKey, SubjectID: tool.ToolID,
			Predicate: "uses tool", Value: tool.ToolID, Category: "how_you_work", Text: tool.HypothesisText,
			Evidence: []KnowledgeEvidence{{
				SourceKind: FolderScanSourceKind, SourceID: subject.Key,
				ObservedAt: offer.ScannedAt.UTC(), Summary: folderScanEvidenceSummary(subject),
			}},
		}
		if _, _, err := p.learning.Propose(ctx, userID, proposal); err != nil {
			logger.Debug("Folder scan tool candidate skipped", logger.Fields{"offer_id": offer.ID, "tool": tool.ToolID, "error": err.Error()})
			if errors.Is(err, ErrKnowledgeQuota) || errors.Is(err, ErrKnowledgePaused) {
				return
			}
		}
	}
}

// savedAppAlreadyProposed reports whether the saved-app producer already
// holds this tool, by its semantic key or its hypothesis text.
func savedAppAlreadyProposed(binding KnowledgeBinding, doc KnowledgeDocument, tool folderdigest.Tool) bool {
	key := proposalSemanticKey(binding, KnowledgeProposal{
		SourceKind: "saved_app", ScopeID: "saved-onboarding", SubjectID: tool.ToolID,
		Predicate: "uses tool", Value: tool.ToolID,
	})
	for _, item := range doc.Items {
		if item.SemanticKey == key {
			return true
		}
		for _, revision := range item.Revisions {
			if revision.ID == item.CurrentRevisionID && strings.EqualFold(strings.TrimSpace(revision.Text), tool.HypothesisText) {
				return true
			}
		}
	}
	return false
}
