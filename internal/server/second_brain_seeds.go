package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// secondBrainSeeds gathers what Ori already holds, for the one setup that
// copies it into a new notes folder (FR8). Every source is resolved at call
// time, since the learning services are built after the second-brain service.
// It reads only what the user has already seen and confirmed in HQ.
type secondBrainSeeds struct{ builder *ServerBuilder }

func (s secondBrainSeeds) Seeds(ctx context.Context, userID string) (secondbrain.Seeds, error) {
	var seeds secondbrain.Seeds
	if s.builder == nil {
		return seeds, nil
	}
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	note(s.followUps(ctx, userID, &seeds))
	note(s.knowledge(ctx, userID, &seeds))
	s.agreement(ctx, userID, &seeds)
	// A source that could not be read leaves its part of the seed empty; the
	// others still stand and the first look reports what was written.
	if firstErr != nil && len(seeds.FollowUps) == 0 && len(seeds.People) == 0 && len(seeds.Facts) == 0 && seeds.WorkingAgreement == "" {
		return seeds, firstErr
	}
	return seeds, nil
}

// followUps seeds the follow-ups the user has confirmed and not closed:
// active, snoozed and reopened, not an unconfirmed candidate.
func (s secondBrainSeeds) followUps(ctx context.Context, userID string, seeds *secondbrain.Seeds) error {
	service := s.builder.followUpService
	if service == nil {
		return nil
	}
	open, err := service.List(ctx, followup.Filter{
		UserID:   userID,
		Statuses: []followup.Status{followup.StatusActive, followup.StatusSnoozed, followup.StatusReopened},
	})
	if err != nil {
		return err
	}
	for _, item := range open {
		if len(seeds.FollowUps) == secondbrain.MaxSeedFollowUps {
			break
		}
		seed := secondbrain.SeedFollowUp{Title: item.Title, Source: followUpSourceLabel(item)}
		if item.DueAt != nil {
			seed.Due = item.DueAt.Local().Format("2006-01-02")
		}
		seeds.FollowUps = append(seeds.FollowUps, seed)
	}
	return nil
}

// followUpSourceLabel says where a follow-up came from in the words the
// Follow-ups page uses. A counterparty that is an address is left out: the
// notes carry names, not addresses.
func followUpSourceLabel(item *followup.FollowUp) string {
	label := ""
	switch item.Source.Type {
	case "email_thread":
		label = "Email"
	case "journal":
		label = "Journal"
	case secondbrain.FollowUpSourceType:
		label = "Your notes"
	case "manual":
		label = "Added by you"
	}
	if who := strings.TrimSpace(item.Counterparty); who != "" && !strings.ContainsAny(who, "@<>") {
		if label == "" {
			return who
		}
		return label + " · " + who
	}
	return label
}

// knowledge seeds the approved facts and the people Ori knows. A fact whose
// source is unavailable, or that is the second-brain fact itself, is left out.
func (s secondBrainSeeds) knowledge(ctx context.Context, userID string, seeds *secondbrain.Seeds) error {
	learning := s.builder.personalAssistantLearning
	if learning == nil {
		return nil
	}
	items, err := learning.ReviewItems(ctx, userID)
	if err != nil {
		return err
	}
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if item.State != personalassistant.KnowledgeApproved || item.ReviewUnavailable != "" || text == "" ||
			item.SourceKind == personalassistant.SecondBrainSourceKind {
			continue
		}
		if item.Category == "people" {
			if person, ok := secondbrain.PersonFromSentence(text); ok && len(seeds.People) < secondbrain.MaxSeedPeople {
				seeds.People = append(seeds.People, person)
			}
			continue
		}
		seeds.Facts = append(seeds.Facts, text)
	}
	return nil
}

// agreement seeds the working agreement's summary: what the user asked the
// assistant to do and the areas it focuses on. No relationship, or one that
// cannot be read, has no agreement to copy; that is not a failure of the setup.
func (s secondBrainSeeds) agreement(ctx context.Context, userID string, seeds *secondbrain.Seeds) {
	service := s.builder.personalAssistantService
	if service == nil {
		return
	}
	projection, err := service.Get(ctx, userID)
	if err != nil || (projection.State != personalassistant.APIStateActive && projection.State != personalassistant.APIStatePaused) {
		return
	}
	parts := []string{strings.TrimSpace(projection.Mandate)}
	if len(projection.FocusAreas) > 0 {
		areas := make([]string, 0, len(projection.FocusAreas))
		for _, area := range projection.FocusAreas {
			areas = append(areas, strings.ReplaceAll(string(area), "_", " "))
		}
		parts = append(parts, "Focus: "+strings.Join(areas, ", ")+".")
	}
	seeds.WorkingAgreement = strings.TrimSpace(strings.Join(parts, " "))
}
