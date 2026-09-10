package progression_test

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/types"
)

// rewardTestStore is the minimal StateStore the reward tests need.
type rewardTestStore struct{ state types.ProgressionState }

func (s *rewardTestStore) GetProgression() types.ProgressionState { return s.state }

func (s *rewardTestStore) SetProgression(p types.ProgressionState) error {
	s.state = p
	return nil
}

// The City Economy pays quest rewards from the onComplete callback, and relies
// on Backfill NOT firing it: an install that already did this work before the
// economy existed is grandfathered by the economy's own one-time grant, and
// must not also be paid per quest for its whole history.
//
// That property is currently a comment on Backfill. This pins it, because if it
// ever changed the symptom would be an established user silently minting Craft
// on upgrade — a bug nobody would think to look for in the quest engine.
func TestBackfillDoesNotPayQuestRewards(t *testing.T) {
	var completedLive []string
	engine := progression.New(
		&rewardTestStore{},
		progression.WithQuests(progression.PersonalAssistantQuests()),
		progression.WithOnComplete(func(q progression.Quest) {
			completedLive = append(completedLive, q.ID)
		}),
	)

	// A snapshot of an established install: it has workspaces, agents, notes,
	// and finished tasks, so several rewarded quests are already satisfied.
	err := engine.Backfill(progression.ScannerFunc(func() progression.Snapshot {
		return progression.Snapshot{
			Workspaces: 4, Agents: 3, Notes: 7,
			TasksStarted: 12, AgentTasksDone: 9, ChatMessages: 300,
			Personalized: true, HasPersonalHQ: true,
		}
	}))
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if len(completedLive) != 0 {
		t.Fatalf("backfill fired onComplete for %v — an established install would "+
			"be paid quest Craft for work the economy's own backfill already counted",
			completedLive)
	}
}

// The City Economy pays for onboarding quests by id (internal/economy/tuning.go).
// Ids are strings, so nothing but a test stops one from being misspelled or
// outliving the quest it names — and a reward nobody can earn fails silently,
// which is the worst way for a cold-start fix to break.
//
// This test lives in progression rather than economy because only an external
// test package can import both without a cycle.
func TestEveryRewardedQuestExists(t *testing.T) {
	known := make(map[string]progression.Quest)
	for _, quest := range progression.PersonalAssistantQuests() {
		known[quest.ID] = quest
	}

	for _, quest := range progression.PersonalAssistantQuests() {
		amount, rewarded := economy.StarterQuestCraft(quest.ID)
		if rewarded && amount <= 0 {
			t.Fatalf("quest %q is listed as rewarded but pays %d", quest.ID, amount)
		}
	}

	// The reverse direction is the one that actually rots: an id in the reward
	// table that no longer matches a quest.
	for _, questID := range rewardedQuestIDs() {
		quest, ok := known[questID]
		if !ok {
			t.Fatalf("the economy pays for quest %q, which no longer exists", questID)
		}
		if quest.Tier > 2 {
			t.Fatalf("quest %q is tier %d; only tiers 1 and 2 cover the cold start",
				questID, quest.Tier)
		}
	}
}

// A rewarded quest has to be reachable by doing something. One that can only be
// backfilled would never pay, because backfill deliberately does not fire the
// completion callback.
func TestRewardedQuestsCanCompleteLive(t *testing.T) {
	for _, quest := range progression.PersonalAssistantQuests() {
		if _, rewarded := economy.StarterQuestCraft(quest.ID); !rewarded {
			continue
		}
		if quest.Match == nil && !completedByDirectCall(quest.ID) {
			t.Fatalf("quest %q pays Craft but has no live completion path, so the "+
				"reward can never be earned", quest.ID)
		}
	}
}

// Quests with no Match that the server still completes explicitly from a
// non-event code path (see initializeProgression).
func completedByDirectCall(questID string) bool {
	switch questID {
	case "t1-personalize", "t2-build-hq":
		return true
	default:
		return false
	}
}

func rewardedQuestIDs() []string {
	// Kept in step with the economy's table through StarterQuestCraft rather
	// than by copying it: this list only has to name candidates, and the lookup
	// decides.
	candidates := []string{
		"t1-first-message", "t1-personalize",
		"t2-create-workspace", "t2-create-note", "t2-run-task", "t2-build-hq",
		"t3-second-agent", "t3-delegate", "t4-enable-skill",
		"t5-create-trigger", "t6-memory",
	}
	var rewarded []string
	for _, id := range candidates {
		if _, ok := economy.StarterQuestCraft(id); ok {
			rewarded = append(rewarded, id)
		}
	}
	return rewarded
}
