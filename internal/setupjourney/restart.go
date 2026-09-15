package setupjourney

import (
	"context"
	"strings"
)

// Restart resets setup progress whose saved declaration is no longer
// compatible with the current one (FR 36). It is offered only when the current
// root projection reports DeclarationIncompatible, for the assistant alias,
// plugin quests and host quests. It deletes the root run and its children with
// their receipts, then creates a fresh root for the current declaration and
// returns its reconciled projection. Groups, projects, teams, plugins and
// user-template bindings are untouched, so steps they satisfy read as
// complete again.
//
// User-template quests are excluded: their attachment binding locks a
// definition, and removing and recreating the quest is the supported reset.
func (s *Service) Restart(ctx context.Context, userID string) (*JourneyProjection, error) {
	if s == nil || s.store == nil {
		return nil, failure(ReasonJourneyUnavailable, 0)
	}
	userID = strings.TrimSpace(userID)
	if s.quest == nil {
		pinned, err := s.pinAliasQuest(ctx, userID)
		if err != nil {
			return nil, err
		}
		if pinned != s {
			return pinned.Restart(ctx, userID)
		}
	}
	if s.quest != nil && s.quest.Source == QuestSourceUserTemplate {
		return nil, failure(ReasonActionUnavailable, 0)
	}
	current, err := s.Read(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	if !current.DeclarationIncompatible || current.RunKind != RunKindRoot {
		return nil, failure(ReasonActionUnavailable, current.StateRevision)
	}
	identity, declaration, err := s.currentDeclaration(ctx, userID)
	if err != nil {
		return nil, err
	}
	root, err := s.store.GetRun(ctx, current.RunID)
	if err != nil || root.Kind != RunKindRoot || root.OwnerUserID != userID ||
		root.RelationshipID != identity.AssistantID || root.SpecialistSlug != identity.SpecialistSlug ||
		root.JourneyID != declaration.ID {
		return nil, failure(ReasonRunNotFound, current.StateRevision)
	}
	if err := s.store.DeleteRoot(ctx, userID, root.ID); err != nil {
		return nil, safeActionStoreFailure(err, current.StateRevision)
	}
	return s.Read(ctx, userID, "")
}
