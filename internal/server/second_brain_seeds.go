package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	if s.builder.personalAssistantLearning == nil {
		return nil
	}
	facts, people, err := s.approvedFacts(ctx, userID)
	if err != nil {
		return err
	}
	for _, held := range people {
		if person, ok := secondbrain.PersonFromSentence(held.Text); ok && len(seeds.People) < secondbrain.MaxSeedPeople {
			seeds.People = append(seeds.People, person)
		}
	}
	seeds.Facts = facts
	return nil
}

// approvedFacts are the facts the user has approved, split into the ones the
// facts note lists and the ones about people. Setup and the fill-in both read
// them here, so the facts note a setup writes is the one the first sync would
// have written, and the sync says nothing about it. A fact whose source is
// unavailable, or the second-brain fact itself, is left out.
func (s secondBrainSeeds) approvedFacts(ctx context.Context, userID string) (facts, people []secondbrain.HeldFact, err error) {
	items, err := s.builder.personalAssistantLearning.ReviewItems(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if item.State != personalassistant.KnowledgeApproved || item.ReviewUnavailable != "" || text == "" ||
			item.SourceKind == personalassistant.SecondBrainSourceKind {
			continue
		}
		held := secondbrain.HeldFact{Text: text, How: knowledgeHowPhrase(item), Date: item.UpdatedAt.Local().Format("2 Jan 2006")}
		switch {
		case item.Category == "people":
			people = append(people, held)
		case factsNoteCategories[item.Category]:
			facts = append(facts, held)
		}
	}
	return facts, people, nil
}

var (
	errSecondBrainLearningUnavailable  = errors.New("server: the learning service is unavailable")
	errSecondBrainFollowUpsUnavailable = errors.New("server: follow-ups are unavailable")
)

// factsNoteCategories are the approved facts the facts note lists (FR4). People
// get notes of their own, and projects come from workspaces and the project
// question.
var factsNoteCategories = map[string]bool{"how_you_work": true, "routines": true, "sources": true}

// Holdings gathers what the fill-in copies into the notes on each sync. It reads
// only what the user has approved. Without the learning service the holdings are
// unknown rather than empty, so the error stops the sync before any note goes
// stale.
func (s secondBrainSeeds) Holdings(ctx context.Context, userID string) (secondbrain.Holdings, error) {
	var holdings secondbrain.Holdings
	if s.builder == nil || s.builder.personalAssistantLearning == nil {
		return holdings, errSecondBrainLearningUnavailable
	}
	var err error
	if holdings.Facts, holdings.People, err = s.approvedFacts(ctx, userID); err != nil {
		return holdings, err
	}
	var seeds secondbrain.Seeds
	s.agreement(ctx, userID, &seeds)
	holdings.WorkingAgreement = seeds.WorkingAgreement
	if holdings.FollowUps, err = s.heldFollowUps(ctx, userID); err != nil {
		return holdings, err
	}
	if holdings.Projects, err = s.heldProjects(); err != nil {
		return holdings, err
	}
	return holdings, nil
}

// heldFollowUps are the follow-ups the user has confirmed, as at setup, plus the
// completed ones so a note Ori keeps can be ticked. A candidate is not confirmed
// and a dismissed one simply goes stale.
func (s secondBrainSeeds) heldFollowUps(ctx context.Context, userID string) ([]secondbrain.HeldFollowUp, error) {
	service := s.builder.followUpService
	if service == nil {
		return nil, errSecondBrainFollowUpsUnavailable
	}
	items, err := service.List(ctx, followup.Filter{
		UserID:   userID,
		Statuses: []followup.Status{followup.StatusActive, followup.StatusSnoozed, followup.StatusReopened, followup.StatusCompleted},
	})
	if err != nil {
		return nil, err
	}
	held := make([]secondbrain.HeldFollowUp, 0, len(items))
	for _, item := range items {
		one := secondbrain.HeldFollowUp{
			ID: item.ID, Title: item.Title, Source: followUpSourceLabel(item), Done: item.Status == followup.StatusCompleted,
			FromNotes: item.Source.Type == secondbrain.FollowUpSourceType,
		}
		if item.DueAt != nil {
			one.Due = item.DueAt.Local().Format("2006-01-02")
		}
		held = append(held, one)
	}
	return held, nil
}

// heldProjects are the user's workspaces. Personal HQ is not a project, and a
// group holds workspaces rather than being one.
func (s secondBrainSeeds) heldProjects() ([]secondbrain.HeldProject, error) {
	store := s.builder.workspaceStore
	if store == nil {
		return nil, errWorkspaceStoreUnavailable
	}
	active, err := store.ListActive()
	if err != nil {
		return nil, err
	}
	held := make([]secondbrain.HeldProject, 0, len(active))
	for _, listed := range active {
		if listed == nil || listed.Designation != "" || listed.Kind == "group" {
			continue
		}
		// The store's listing is a lighter scan that leaves out the goal and the
		// folder references, so each project is read whole. A workspace that
		// cannot be read is described by what the listing has.
		ws := listed
		if whole, err := store.Get(listed.ID); err == nil && whole != nil {
			ws = whole
		}
		held = append(held, secondbrain.HeldProject{
			ID: ws.ID, Name: ws.Name, Goal: ws.Mission, Folder: anchoredFolder(ws), Agents: ws.AgentNames(),
			Date: ws.CreatedAt.Local().Format("2 Jan 2006"),
		})
	}
	return held, nil
}

// anchoredFolder is the folder a workspace is anchored to, or "". The reference
// holds the path; the anchor itself only names the reference.
func anchoredFolder(ws *workspace.Workspace) string {
	id := ws.AnchorDirectoryReferenceID()
	if id == "" {
		return ""
	}
	for _, reference := range ws.DirectoryReferences {
		if reference.ID == id {
			return reference.Path
		}
	}
	return ""
}

// knowledgeHowPhrase says in plain words how Ori came to know a fact, so a user
// auditing the folder in Obsidian sees why each note exists.
func knowledgeHowPhrase(item personalassistant.KnowledgeReviewItem) string {
	switch item.Source.Kind {
	case personalassistant.KnowledgeInterviewSourceKind:
		return "You told Ori this when it asked about you"
	case "explicit":
		return "You told Ori this"
	case personalassistant.SetupBatchSourceKind:
		return "You approved this after Ori saw it in the files you filed"
	case "file_janitor":
		return "You approved this after Ori noticed how you file"
	default:
		return "You approved this on the Profile page"
	}
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
