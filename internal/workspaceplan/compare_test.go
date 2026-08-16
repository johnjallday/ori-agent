package workspaceplan

import (
	"slices"
	"testing"
)

func versionOf(number int, objective string, content PlanContent) *Version {
	hash, _ := ContentHash(objective, content, PolicySnapshot{})
	return &Version{
		Number:      number,
		Objective:   objective,
		Content:     content,
		ContentHash: hash,
	}
}

func comparableContent() PlanContent {
	return PlanContent{
		InScope:   []string{"reporting"},
		NonGoals:  []string{"billing"},
		Execution: ExecutionPolicy{Mode: ExecutionStepThrough, Preconditions: []string{"repo_scan"}},
		Artifacts: []ProposedArtifact{{ID: "art-1", Kind: ArtifactPRD, Path: "tasks/prd.md", Enabled: true}},
		Validations: []ValidationCheckpoint{{
			ID: "val-1", Title: "Row counts match", Required: true,
		}},
		Groups: []TaskGroup{
			{
				ID: "grp-1", Title: "Prepare", Outcome: "Ready",
				Items: []TaskItem{
					{ID: "itm-1", Description: "Snapshot", Assignee: "builder"},
					{ID: "itm-2", Description: "Verify", DependsOn: []string{"itm-1"}},
				},
			},
			{
				ID: "grp-2", Title: "Cut over",
				Items: []TaskItem{{ID: "itm-3", Description: "Switch traffic"}},
			},
		},
	}
}

func itemChangeFor(diff VersionDiff, id string, kind ChangeKind) *ItemChange {
	for i := range diff.Items {
		if diff.Items[i].ID == id && diff.Items[i].Kind == kind {
			return &diff.Items[i]
		}
	}
	return nil
}

func groupChangeFor(diff VersionDiff, id string, kind ChangeKind) *GroupChange {
	for i := range diff.Groups {
		if diff.Groups[i].ID == id && diff.Groups[i].Kind == kind {
			return &diff.Groups[i]
		}
	}
	return nil
}

func TestCompareIdenticalVersions(t *testing.T) {
	first := versionOf(1, "Objective", comparableContent())
	second := versionOf(2, "Objective", comparableContent())

	diff := CompareVersions(first, second)
	if !diff.Identical {
		t.Fatalf("identical content compared as different: %+v", diff)
	}
	if diff.From != 1 || diff.To != 2 {
		t.Errorf("version numbers = %d -> %d", diff.From, diff.To)
	}
}

// Two versions that differ only in prose compare as identical: a reviewer
// should not be asked to re-read a plan because a paragraph was rewritten
// (FR-34).
func TestCompareIgnoresCosmeticChanges(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	after.Explanation = "Completely different prose"
	after.Rationale = "A different justification"
	after.Groups[0].Notes = "different notes"

	diff := CompareVersions(versionOf(1, "Objective", before), versionOf(2, "Objective", after))
	if !diff.Identical {
		t.Errorf("a prose-only change compared as a real difference: %+v", diff)
	}
}

func TestCompareDetectsAddedAndRemovedItems(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	// Remove itm-2 and add a new one.
	after.Groups[0].Items = []TaskItem{
		{ID: "itm-1", Description: "Snapshot", Assignee: "builder"},
		{ID: "itm-new", Description: "Freshly added"},
	}

	diff := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))
	if diff.Identical {
		t.Fatal("a structural change compared as identical")
	}
	if itemChangeFor(diff, "itm-2", ChangeRemoved) == nil {
		t.Errorf("removed item not reported: %+v", diff.Items)
	}
	added := itemChangeFor(diff, "itm-new", ChangeAdded)
	if added == nil {
		t.Fatalf("added item not reported: %+v", diff.Items)
	}
	if added.Description != "Freshly added" || added.GroupID != "grp-1" {
		t.Errorf("added item lacks context: %+v", added)
	}
}

// A moved item must read as moved, not as a removal plus an addition: those
// carry different risk, and only one of them means work disappeared (FR-36).
func TestCompareDetectsReorderingRatherThanChurn(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	after.Groups[0].Items[0], after.Groups[0].Items[1] =
		after.Groups[0].Items[1], after.Groups[0].Items[0]

	diff := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))

	moved := itemChangeFor(diff, "itm-1", ChangeMoved)
	if moved == nil {
		t.Fatalf("reordering was not reported as a move: %+v", diff.Items)
	}
	if moved.FromIndex != 0 || moved.ToIndex != 1 {
		t.Errorf("move indices = %d -> %d, want 0 -> 1", moved.FromIndex, moved.ToIndex)
	}
	// Nothing was added or removed by a reorder.
	for _, change := range diff.Items {
		if change.Kind == ChangeAdded || change.Kind == ChangeRemoved {
			t.Errorf("a reorder produced an %s: %+v", change.Kind, change)
		}
	}
}

func TestCompareDetectsAnItemMovingBetweenGroups(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	// Move itm-3 from grp-2 into grp-1.
	after.Groups[0].Items = append(after.Groups[0].Items, TaskItem{ID: "itm-3", Description: "Switch traffic"})
	after.Groups[1].Items = nil

	diff := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))

	moved := itemChangeFor(diff, "itm-3", ChangeMoved)
	if moved == nil {
		t.Fatalf("an item moving between groups was not reported: %+v", diff.Items)
	}
	if moved.FromGroupID != "grp-2" || moved.GroupID != "grp-1" {
		t.Errorf("move did not name both groups: %+v", moved)
	}
}

// Assignee and dependency changes are named explicitly, because they change who
// does the work and in what order (FR-36).
func TestCompareNamesAssigneeAndDependencyChanges(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	after.Groups[0].Items[0].Assignee = "reviewer"
	after.Groups[0].Items[1].DependsOn = nil

	diff := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))

	assignee := itemChangeFor(diff, "itm-1", ChangeModified)
	if assignee == nil || !slices.Contains(assignee.Fields, "assignee") {
		t.Errorf("assignee change not named: %+v", assignee)
	}
	dependency := itemChangeFor(diff, "itm-2", ChangeModified)
	if dependency == nil || !slices.Contains(dependency.Fields, "depends_on") {
		t.Errorf("dependency change not named: %+v", dependency)
	}
}

func TestCompareDetectsGroupChanges(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	after.Groups[0].Title = "Prepare, revised"
	after.Groups[1].DependsOn = []string{"grp-1"}

	diff := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))

	renamed := groupChangeFor(diff, "grp-1", ChangeModified)
	if renamed == nil || !slices.Contains(renamed.Fields, "title") {
		t.Errorf("group rename not reported: %+v", diff.Groups)
	}
	gated := groupChangeFor(diff, "grp-2", ChangeModified)
	if gated == nil || !slices.Contains(gated.Fields, "depends_on") {
		t.Errorf("group dependency change not reported: %+v", diff.Groups)
	}
}

func TestCompareDetectsScopeAndObjectiveChanges(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	after.InScope = []string{"reporting", "archival"}
	after.NonGoals = nil

	diff := CompareVersions(versionOf(1, "Old objective", before), versionOf(2, "New objective", after))

	if diff.Objective == nil || diff.Objective.After != "New objective" {
		t.Errorf("objective change not reported: %+v", diff.Objective)
	}
	if len(diff.InScope) != 1 || diff.InScope[0].Kind != ChangeAdded || diff.InScope[0].Value != "archival" {
		t.Errorf("scope addition not reported: %+v", diff.InScope)
	}
	if len(diff.NonGoals) != 1 || diff.NonGoals[0].Kind != ChangeRemoved {
		t.Errorf("non-goal removal not reported: %+v", diff.NonGoals)
	}
}

// Whether a file gets written at all is the artifact change a reviewer most
// needs to see.
func TestCompareDetectsArtifactChanges(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	after.Artifacts[0].Enabled = false
	after.Artifacts = append(after.Artifacts, ProposedArtifact{
		ID: "art-2", Kind: ArtifactTaskList, Path: "tasks/tasks.md", Enabled: true,
	})

	diff := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))

	var disabled, added *EntryChange
	for i := range diff.Artifacts {
		switch diff.Artifacts[i].ID {
		case "art-1":
			disabled = &diff.Artifacts[i]
		case "art-2":
			added = &diff.Artifacts[i]
		}
	}
	if disabled == nil || !slices.Contains(disabled.Fields, "enabled") {
		t.Errorf("artifact enablement change not reported: %+v", diff.Artifacts)
	}
	if added == nil || added.Kind != ChangeAdded {
		t.Errorf("added artifact not reported: %+v", diff.Artifacts)
	}
}

func TestCompareDetectsExecutionPolicyChanges(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	after.Execution.Mode = ExecutionAuto
	after.Execution.Preconditions = []string{"repo_scan", "safe_branch"}

	diff := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))

	if diff.Execution == nil || diff.Execution.After != string(ExecutionAuto) {
		t.Errorf("execution mode change not reported: %+v", diff.Execution)
	}
	if len(diff.Preconditions) != 1 || diff.Preconditions[0].Value != "safe_branch" {
		t.Errorf("precondition change not reported: %+v", diff.Preconditions)
	}
}

func TestCompareDetectsValidationChanges(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	after.Validations[0].Required = false

	diff := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))
	if len(diff.Validation) != 1 || !slices.Contains(diff.Validation[0].Fields, "required") {
		t.Errorf("validation change not reported: %+v", diff.Validation)
	}
}

// A comparison that reshuffles between refreshes is one a reviewer cannot
// trust.
func TestCompareIsDeterministic(t *testing.T) {
	before := comparableContent()
	after := comparableContent()
	after.Groups[0].Items = []TaskItem{
		{ID: "itm-2", Description: "Verify"},
		{ID: "itm-a", Description: "A"},
		{ID: "itm-b", Description: "B"},
	}

	first := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))
	for range 10 {
		again := CompareVersions(versionOf(1, "o", before), versionOf(2, "o", after))
		if len(again.Items) != len(first.Items) {
			t.Fatalf("item change count varied: %d vs %d", len(first.Items), len(again.Items))
		}
		for i := range first.Items {
			if again.Items[i].ID != first.Items[i].ID || again.Items[i].Kind != first.Items[i].Kind {
				t.Fatalf("comparison order varied at %d: %+v vs %+v", i, first.Items[i], again.Items[i])
			}
		}
	}
}

func TestCompareHandlesMissingVersions(t *testing.T) {
	diff := CompareVersions(nil, versionOf(1, "o", comparableContent()))
	if diff.From != 0 || diff.To != 0 {
		t.Errorf("comparing against a missing version produced %+v", diff)
	}
}
