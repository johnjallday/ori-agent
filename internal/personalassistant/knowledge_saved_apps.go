package personalassistant

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// SavedAppProfileReader must return a previously persisted onboarding profile.
// The production onboarding.Manager now returns a defensive snapshot. This
// interface deliberately offers neither detection nor profile seeding.
type SavedAppProfileReader interface {
	GetUserProfile() *types.InferredProfile
}

// savedAppHypothesis is the tool row an installed app name maps to. The rows
// live in the shared host-owned tool table the folder producer reads too, so
// a tool is added once and both producers agree on its identity (FR36).
type savedAppHypothesis struct {
	id   string
	text string
}

func savedAppHypothesisFor(app string) (savedAppHypothesis, bool) {
	tool, ok := folderdigest.ToolForApp(app)
	if !ok {
		return savedAppHypothesis{}, false
	}
	return savedAppHypothesis{id: tool.ToolID, text: tool.HypothesisText}, true
}

// SavedAppProducer proposes only bounded tool-use hypotheses from a saved
// onboarding observation. It never calls a detector, profiler or provider.
type SavedAppProducer struct {
	learning *KnowledgeLearningService
	reader   SavedAppProfileReader
	profiles userprofile.UserStore
}

func NewSavedAppProducer(learning *KnowledgeLearningService, reader SavedAppProfileReader, profiles userprofile.UserStore) *SavedAppProducer {
	return &SavedAppProducer{learning: learning, reader: reader, profiles: profiles}
}

func (p *SavedAppProducer) Check(ctx context.Context, userID string) ([]KnowledgeItem, error) {
	if p == nil || p.learning == nil || p.learning.store == nil || p.learning.memory == nil || p.reader == nil || p.profiles == nil {
		return nil, ErrRepairNeeded
	}
	binding, err := p.learning.store.resolve(ctx, userID)
	if err != nil {
		return nil, err
	}
	if binding.UserID != userprofile.LocalUserID {
		return nil, ErrRepairNeeded // onboarding manager owns only the local profile
	}
	if binding.Paused {
		return nil, ErrKnowledgePaused
	}
	profile := p.reader.GetUserProfile()
	if profile == nil || profile.InferredAt.IsZero() || len(profile.DetectedApps) == 0 {
		return nil, nil // no saved observation; never fabricate a scan
	}
	observed := profile.InferredAt.UTC()
	apps := append([]string(nil), profile.DetectedApps...)
	canonical, err := p.profiles.Get(ctx, binding.UserID)
	if err != nil && !errors.Is(err, userprofile.ErrNotFound) {
		return nil, err
	}
	snapshot, err := p.learning.memory.SnapshotExact(binding.HQWorkspaceID)
	if err != nil {
		return nil, err
	}
	var results []KnowledgeItem
	seen := make(map[string]bool)
	for _, app := range apps {
		hypothesis, ok := savedAppHypothesisFor(app)
		if !ok || seen[hypothesis.id] {
			continue
		}
		seen[hypothesis.id] = true
		if canonical != nil && profileAlreadyNamesApp(canonical, app, hypothesis.id) {
			continue
		}
		alreadySaved := false
		for _, entry := range snapshot.Entries {
			if strings.EqualFold(entry.Entry.Text, hypothesis.text) {
				alreadySaved = true
				break
			}
		}
		if alreadySaved {
			continue
		}
		proposal := KnowledgeProposal{
			SourceKind: "saved_app", ScopeID: "saved-onboarding", SubjectID: hypothesis.id,
			Predicate: "uses tool", Value: hypothesis.id, Category: "how_you_work", Text: hypothesis.text,
			Evidence: []KnowledgeEvidence{{SourceKind: "saved_app", SourceID: hypothesis.id,
				ObservedAt: observed, Summary: "Saved onboarding app observation"}},
		}
		item, _, err := p.learning.Propose(ctx, userID, proposal)
		if errors.Is(err, ErrKnowledgeSuppressed) {
			continue
		}
		if err != nil {
			return results, err // preserve successes and show quota/unavailability honestly
		}
		results = append(results, item)
	}
	return results, nil
}

func profileAlreadyNamesApp(profile *userprofile.UserProfile, app, id string) bool {
	for _, specialization := range profile.Specializations {
		if strings.EqualFold(strings.TrimSpace(specialization), strings.TrimSpace(app)) || strings.EqualFold(strings.TrimSpace(specialization), id) {
			return true
		}
	}
	return false
}

// SavedAppAuthority is a source-specific approval/context gate. It verifies
// that the saved observation still exists at the reviewed checkpoint, without
// running a fresh app scan. Other source kinds require their own authority.
type SavedAppAuthority struct{ reader SavedAppProfileReader }

func NewSavedAppAuthority(reader SavedAppProfileReader) SavedAppAuthority {
	return SavedAppAuthority{reader: reader}
}

func (a SavedAppAuthority) Revalidate(_ context.Context, binding KnowledgeBinding, item KnowledgeItem) error {
	if a.reader == nil || binding.UserID != userprofile.LocalUserID || item.SourceKind != "saved_app" {
		return ErrRepairNeeded
	}
	profile := a.reader.GetUserProfile()
	if profile == nil || profile.InferredAt.IsZero() || len(profile.DetectedApps) == 0 {
		return ErrRepairNeeded
	}
	var evidence []KnowledgeEvidence
	for _, rev := range item.Revisions {
		if rev.ID == item.CurrentRevisionID {
			evidence = rev.Evidence
			break
		}
	}
	if len(evidence) != 1 || evidence[0].SourceKind != "saved_app" || evidence[0].ObservedAt.IsZero() ||
		!evidence[0].ObservedAt.Equal(profile.InferredAt) {
		return ErrConflict
	}
	wantID := evidence[0].SourceID
	if wantID == "" {
		return ErrConflict
	}
	for _, app := range profile.DetectedApps {
		if hypothesis, ok := savedAppHypothesisFor(app); ok && hypothesis.id == wantID {
			key := proposalSemanticKey(binding, KnowledgeProposal{
				SourceKind: "saved_app", ScopeID: "saved-onboarding", SubjectID: wantID,
				Predicate: "uses tool", Value: wantID,
			})
			if key == item.SemanticKey {
				return nil
			}
			return ErrConflict
		}
	}
	return ErrConflict
}

var _ KnowledgeSourceAuthority = SavedAppAuthority{}
