// Package economy implements the City Economy: two earned resources (Craft and
// Harvest), the cadence tiers that price a recurring task ("a Farm"), and the
// append-only ledger both are derived from.
//
// Nothing here generates a resource on a timer. Craft comes from work the user
// did by hand, Harvest comes from Farm output the user actually opened, and
// Energy is a read-only view of tokens already spent. See
// tasks/prd-city-economy.md.
package economy

import (
	"strings"
	"time"
)

// Every tunable number in the feature lives here (PRD FR4). Nothing else in the
// feature may hardcode a cost, a cap, or a window — a week of real use is
// expected to move these values, and one file is the whole blast radius.
const (
	// CraftPerChatMessage is credited for each chat message the user sends.
	CraftPerChatMessage int64 = 1

	// ChatCraftHourlyCap bounds chat Craft per clock hour so a scripted
	// message loop cannot mint a city.
	ChatCraftHourlyCap int64 = 20

	// ChatDuplicateWindow suppresses Craft for a message identical to the
	// previous one sent within this window.
	ChatDuplicateWindow = 30 * time.Second

	// CraftPerManualTask is credited when a task the user ran by hand
	// completes. A Farm run never earns Craft (FR13).
	CraftPerManualTask int64 = 5

	// HarvestPerFarmRun is credited per Farm run when the user opens that
	// Farm's result and banks the pending runs.
	HarvestPerFarmRun int64 = 1

	// FarmBuildCost is the flat Craft price of turning a task into a Farm,
	// charged once per task at any cadence tier (FR17, FR18).
	FarmBuildCost int64 = 25

	// BackfillGrantCap bounds the one-time Craft grant an existing install
	// receives so the loop is reachable on day one (FR32).
	BackfillGrantCap int64 = 200

	// DefaultDailyEnergyTokens is the figure the Energy bar fills against
	// until the user sets their own (FR30). A gauge, never a limit.
	DefaultDailyEnergyTokens int64 = 1_000_000
)

// CraftPerStarterQuest is what one onboarding quest pays.
//
// The onboarding quests ARE hand-work — send your first request, create a
// workspace, run a task — so paying Craft for them is the same rule as
// everything else here, not a starter grant handed out for free.
const CraftPerStarterQuest int64 = 5

// starterQuests are the onboarding quests that pay Craft, by quest id.
//
// Tiers 1 and 2 only, and that boundary is the whole point. A brand-new install
// earns nothing from the first-run backfill (it has no history to count), so
// reaching the 25 Craft a first Farm costs means 25 chat messages — and the
// hourly cap makes that two clock hours. That is a wall in front of the loop
// the PRD's first success metric says a user should complete in one sitting.
//
// These six quests are exactly the stretch before that wall. Finishing ordinary
// setup now leaves a user able to afford their first Farm right about when
// Tier 5 asks them to set up a schedule. Tier 3 and beyond pay nothing: by then
// the user is earning normally and does not need the help.
//
// Ids are the durable identifiers from internal/progression/quests.go. A quest
// this map does not name simply pays nothing, so a renamed or retired quest
// degrades to silence rather than to a crash.
var starterQuests = map[string]int64{
	"t1-first-message":    CraftPerStarterQuest,
	"t1-personalize":      CraftPerStarterQuest,
	"t2-create-workspace": CraftPerStarterQuest,
	"t2-create-note":      CraftPerStarterQuest,
	"t2-run-task":         CraftPerStarterQuest,
	"t2-build-hq":         CraftPerStarterQuest,
}

// StarterQuestCraft reports what completing a quest pays, and whether it pays at
// all. Exported so the quest log can show the reward rather than granting it
// silently — a reward the user cannot see teaches nothing.
func StarterQuestCraft(questID string) (int64, bool) {
	amount, ok := starterQuests[strings.TrimSpace(questID)]
	return amount, ok
}

// StarterQuestTotal is what a user who finishes every paying quest earns. Used
// by the tests that keep this table honest against the cost of a first Farm.
func StarterQuestTotal() int64 {
	var total int64
	for _, amount := range starterQuests {
		total += amount
	}
	return total
}

// UpgradeStepCost is the Harvest price of one cadence step up, from tier n to
// tier n+1. It rises with the tier so each successive speed-up is paid for by
// more reviewed output than the last (FR4).
//
// A tier outside the 1-4 range has no step above it and costs nothing: tier 5
// is the top of the ladder and anything lower is not a real tier.
func UpgradeStepCost(n int) int64 {
	if n < 1 || n > 4 {
		return 0
	}
	return 10 * int64(n)
}

// UpgradeCost is the total Harvest price of moving from one tier to another: the
// sum of every step crossed (FR19). Daily (2) to Hourly (4) is 20 + 30 = 50.
// A decrease, or no change at all, is free (FR20).
func UpgradeCost(fromTier, toTier int) int64 {
	if fromTier < 1 || toTier <= fromTier {
		return 0
	}
	var total int64
	for tier := fromTier; tier < toTier; tier++ {
		total += UpgradeStepCost(tier)
	}
	return total
}
