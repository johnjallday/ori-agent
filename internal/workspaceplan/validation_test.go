package workspaceplan

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// validContent is the smallest content that passes every structural check, so
// each test can introduce exactly one defect.
func validContent() (string, PlanContent) {
	return "Ship the migration safely", PlanContent{
		Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
		Groups: []TaskGroup{{
			ID:    "grp-1",
			Title: "Build",
			Items: []TaskItem{{ID: "itm-1", Description: "Write the code"}},
		}},
	}
}

func codes(result ValidationResult) []ValidationCode {
	out := make([]ValidationCode, 0, len(result.Issues))
	for _, issue := range result.Issues {
		out = append(out, issue.Code)
	}
	return out
}

func hasCode(result ValidationResult, want ValidationCode) bool {
	for _, issue := range result.Issues {
		if issue.Code == want {
			return true
		}
	}
	return false
}

func TestValidateAcceptsWellFormedContent(t *testing.T) {
	objective, content := validContent()
	result := ValidatePlanContent(objective, content, ValidationContext{})
	if !result.OK() {
		t.Fatalf("valid content was rejected: %v", codes(result))
	}
	if result.Error() != nil {
		t.Errorf("OK result produced an error: %v", result.Error())
	}
}

func TestValidateRejectsMissingObjectiveAndGroups(t *testing.T) {
	result := ValidatePlanContent("   ", PlanContent{Execution: ExecutionPolicy{Mode: ExecutionStepThrough}}, ValidationContext{})
	if !hasCode(result, IssueMissingObjective) {
		t.Errorf("missing objective not reported: %v", codes(result))
	}
	if !hasCode(result, IssueNoGroups) {
		t.Errorf("missing groups not reported: %v", codes(result))
	}
	// Both are reported in one pass, so a repair attempt can fix them together
	// rather than burning a retry per issue (FR-44).
	if len(result.Issues) < 2 {
		t.Errorf("issues = %d, want every problem reported at once", len(result.Issues))
	}
}

func TestValidateRejectsEmptyGroupsAndItems(t *testing.T) {
	objective, content := validContent()
	content.Groups = append(content.Groups, TaskGroup{ID: "grp-2", Title: "Empty"})
	content.Groups[0].Items[0].Description = "  "

	result := ValidatePlanContent(objective, content, ValidationContext{})
	if !hasCode(result, IssueEmptyGroup) {
		t.Errorf("empty group not reported: %v", codes(result))
	}
	if !hasCode(result, IssueMissingDescription) {
		t.Errorf("item without a description not reported: %v", codes(result))
	}
}

// Dependencies reference stable IDs, so a duplicate ID makes them ambiguous
// (FR-8, FR-42).
func TestValidateRejectsDuplicateIDsAcrossElementKinds(t *testing.T) {
	objective, content := validContent()
	content.Groups = append(content.Groups, TaskGroup{
		ID:    "grp-1", // duplicate of the first group
		Title: "Second",
		Items: []TaskItem{{ID: "itm-2", Description: "More work"}},
	})
	content.Risks = []Risk{{ID: "itm-1", Statement: "collides with an item id"}}

	result := ValidatePlanContent(objective, content, ValidationContext{})
	duplicates := 0
	for _, issue := range result.Issues {
		if issue.Code == IssueDuplicateID {
			duplicates++
		}
	}
	if duplicates != 2 {
		t.Errorf("duplicate id issues = %d, want 2 (%v)", duplicates, codes(result))
	}
}

func TestValidateRejectsDanglingAndSelfDependencies(t *testing.T) {
	objective, content := validContent()
	content.Groups[0].Items[0].DependsOn = []string{"itm-missing"}
	content.Groups[0].DependsOn = []string{"grp-1"}

	result := ValidatePlanContent(objective, content, ValidationContext{})
	if !hasCode(result, IssueDanglingDependency) {
		t.Errorf("dangling dependency not reported: %v", codes(result))
	}
	if !hasCode(result, IssueSelfDependency) {
		t.Errorf("self dependency not reported: %v", codes(result))
	}
}

func TestValidateRejectsCyclicDependencies(t *testing.T) {
	objective, content := validContent()
	content.Groups = []TaskGroup{
		{ID: "grp-a", Title: "A", DependsOn: []string{"grp-c"}, Items: []TaskItem{{ID: "itm-a", Description: "a"}}},
		{ID: "grp-b", Title: "B", DependsOn: []string{"grp-a"}, Items: []TaskItem{{ID: "itm-b", Description: "b"}}},
		{ID: "grp-c", Title: "C", DependsOn: []string{"grp-b"}, Items: []TaskItem{{ID: "itm-c", Description: "c"}}},
	}

	result := ValidatePlanContent(objective, content, ValidationContext{})
	if !hasCode(result, IssueCyclicDependency) {
		t.Fatalf("dependency cycle not reported: %v", codes(result))
	}
	// The message names the loop so a user can see which links to cut.
	var message string
	for _, issue := range result.Issues {
		if issue.Code == IssueCyclicDependency {
			message = issue.Message
		}
	}
	for _, id := range []string{"grp-a", "grp-b", "grp-c"} {
		if !strings.Contains(message, id) {
			t.Errorf("cycle message %q does not name %s", message, id)
		}
	}
}

func TestValidateRejectsItemLevelCycles(t *testing.T) {
	objective, content := validContent()
	content.Groups[0].Items = []TaskItem{
		{ID: "itm-1", Description: "first", DependsOn: []string{"itm-2"}},
		{ID: "itm-2", Description: "second", DependsOn: []string{"itm-1"}},
	}

	result := ValidatePlanContent(objective, content, ValidationContext{})
	if !hasCode(result, IssueCyclicDependency) {
		t.Errorf("item cycle not reported: %v", codes(result))
	}
}

// A cycle report must read the same on every run, or a repair attempt sees a
// "different" error each time and never converges.
func TestValidateCycleReportingIsDeterministic(t *testing.T) {
	objective, content := validContent()
	content.Groups = []TaskGroup{
		{ID: "grp-c", Title: "C", DependsOn: []string{"grp-b"}, Items: []TaskItem{{ID: "itm-c", Description: "c"}}},
		{ID: "grp-a", Title: "A", DependsOn: []string{"grp-c"}, Items: []TaskItem{{ID: "itm-a", Description: "a"}}},
		{ID: "grp-b", Title: "B", DependsOn: []string{"grp-a"}, Items: []TaskItem{{ID: "itm-b", Description: "b"}}},
	}

	var first string
	for range 10 {
		result := ValidatePlanContent(objective, content, ValidationContext{})
		var message string
		for _, issue := range result.Issues {
			if issue.Code == IssueCyclicDependency {
				message = issue.Message
			}
		}
		if first == "" {
			first = message
			continue
		}
		if message != first {
			t.Fatalf("cycle message changed between runs:\n  %q\n  %q", first, message)
		}
	}
}

func TestValidateRejectsUnsupportedExecutionModeAndEnums(t *testing.T) {
	objective, content := validContent()
	content.Execution.Mode = ExecutionMode("fire_and_forget")
	content.Sources = []Source{{ID: "src-1", Kind: SourceKind("telepathy"), Ref: "x"}}
	content.Artifacts = []ProposedArtifact{{ID: "art-1", Kind: ArtifactKind("spreadsheet"), Path: "docs/x.md"}}

	result := ValidatePlanContent(objective, content, ValidationContext{})
	if !hasCode(result, IssueInvalidExecutionMode) {
		t.Errorf("invalid execution mode not reported: %v", codes(result))
	}
	enums := 0
	for _, issue := range result.Issues {
		if issue.Code == IssueInvalidEnum {
			enums++
		}
	}
	if enums != 2 {
		t.Errorf("invalid enum issues = %d, want 2 (%v)", enums, codes(result))
	}
}

// --- Bounds (FR-42, FR-43, SM-13) -----------------------------------------

func groupsOfSize(groups, itemsPerGroup int) PlanContent {
	content := PlanContent{Execution: ExecutionPolicy{Mode: ExecutionStepThrough}}
	for g := range groups {
		group := TaskGroup{ID: fmt.Sprintf("grp-%d", g), Title: fmt.Sprintf("Group %d", g)}
		for i := range itemsPerGroup {
			group.Items = append(group.Items, TaskItem{
				ID:          fmt.Sprintf("itm-%d-%d", g, i),
				Description: fmt.Sprintf("Item %d/%d", g, i),
			})
		}
		content.Groups = append(content.Groups, group)
	}
	return content
}

func TestValidateAcceptsExactlyTheLimitAndRejectsOneMore(t *testing.T) {
	objective := "Ship it"

	atGroupLimit := groupsOfSize(MaxTaskGroups, 1)
	if result := ValidatePlanContent(objective, atGroupLimit, ValidationContext{}); !result.OK() {
		t.Errorf("%d groups was rejected; the limit is inclusive: %v", MaxTaskGroups, codes(result))
	}
	overGroupLimit := groupsOfSize(MaxTaskGroups+1, 1)
	result := ValidatePlanContent(objective, overGroupLimit, ValidationContext{})
	if !hasCode(result, IssueTooManyGroups) {
		t.Errorf("%d groups was accepted: %v", MaxTaskGroups+1, codes(result))
	}
	// Over-limit content is refused whole, and the message says to split or
	// supersede rather than implying anything was dropped (FR-43).
	if !errors.Is(result.Error(), ErrLimitExceeded) {
		t.Errorf("over-limit error = %v, want ErrLimitExceeded", result.Error())
	}
	if !strings.Contains(result.Error().Error(), "Split it or supersede it") {
		t.Errorf("limit message does not offer split/supersede: %v", result.Error())
	}

	// 200 items exactly, spread across 20 groups.
	atItemLimit := groupsOfSize(MaxTaskGroups, MaxTaskItems/MaxTaskGroups)
	if atItemLimit.ActionableItemCount() != MaxTaskItems {
		t.Fatalf("fixture has %d items, want %d", atItemLimit.ActionableItemCount(), MaxTaskItems)
	}
	if result := ValidatePlanContent(objective, atItemLimit, ValidationContext{}); !result.OK() {
		t.Errorf("%d items was rejected; the limit is inclusive: %v", MaxTaskItems, codes(result))
	}

	overItemLimit := atItemLimit.Clone()
	overItemLimit.Groups[0].Items = append(overItemLimit.Groups[0].Items, TaskItem{
		ID: "itm-extra", Description: "the 201st item",
	})
	if result := ValidatePlanContent(objective, overItemLimit, ValidationContext{}); !hasCode(result, IssueTooManyItems) {
		t.Errorf("%d items was accepted: %v", MaxTaskItems+1, codes(result))
	}
}

func TestValidateRejectsOversizeCanonicalJSON(t *testing.T) {
	objective := "Ship it"
	content := groupsOfSize(1, 1)
	// One very large details field pushes canonical JSON past the byte bound
	// without exceeding the group or item counts.
	content.Groups[0].Items[0].Details = strings.Repeat("x", MaxContentBytes+1)

	result := ValidatePlanContent(objective, content, ValidationContext{})
	if !hasCode(result, IssueContentTooLarge) {
		t.Fatalf("oversize content was accepted: %v", codes(result))
	}
	if !errors.Is(result.Error(), ErrLimitExceeded) {
		t.Errorf("oversize error = %v, want ErrLimitExceeded", result.Error())
	}

	// The content itself is untouched: refusing is not truncating (FR-43).
	if len(content.Groups[0].Items[0].Details) != MaxContentBytes+1 {
		t.Error("validation modified the content it refused")
	}
}

func TestCanonicalSizeMeasuresObjectiveAndContent(t *testing.T) {
	small, err := CanonicalSize("o", PlanContent{})
	if err != nil {
		t.Fatalf("canonical size: %v", err)
	}
	large, err := CanonicalSize("o", groupsOfSize(3, 5))
	if err != nil {
		t.Fatalf("canonical size: %v", err)
	}
	if large <= small {
		t.Errorf("canonical size did not grow with content: %d vs %d", small, large)
	}
}

// --- Assignment and capability availability (FR-47, FR-48) -----------------

func TestValidateRejectsUnavailableAssigneesAndCapabilities(t *testing.T) {
	objective, content := validContent()
	content.Groups[0].Items[0].Assignee = "ghost-agent"
	content.Groups[0].Items[0].RequiredCapabilities = []string{"telepathy"}

	result := ValidatePlanContent(objective, content, ValidationContext{
		AvailableAgents:       []string{"builder"},
		AvailableCapabilities: []string{"email"},
	})
	if !hasCode(result, IssueUnavailableAssignee) {
		t.Errorf("unavailable assignee not reported: %v", codes(result))
	}
	if !hasCode(result, IssueUnavailableCapability) {
		t.Errorf("unavailable capability not reported: %v", codes(result))
	}
	if !errors.Is(result.Error(), ErrUnavailableCapability) {
		t.Errorf("error = %v, want ErrUnavailableCapability", result.Error())
	}
}

// An unassigned item is a legitimate choice, not a defect: it materializes an
// unassigned Task rather than a guessed one (FR-86).
func TestValidateAllowsAnUnassignedItem(t *testing.T) {
	objective, content := validContent()
	content.Groups[0].Items[0].Assignee = ""

	result := ValidatePlanContent(objective, content, ValidationContext{
		AvailableAgents: []string{"builder"},
	})
	if !result.OK() {
		t.Errorf("an unassigned item was rejected: %v", codes(result))
	}
}

// A nil availability list means "not checked", never "nothing is available" —
// otherwise an offline agent registry would invalidate every Plan.
func TestValidateSkipsAssignmentChecksWithoutContext(t *testing.T) {
	objective, content := validContent()
	content.Groups[0].Items[0].Assignee = "some-agent"
	content.Groups[0].Items[0].RequiredCapabilities = []string{"email"}

	if result := ValidatePlanContent(objective, content, ValidationContext{}); !result.OK() {
		t.Errorf("assignment was checked without context: %v", codes(result))
	}
}

// --- Artifact paths (FR-97, FR-169) ---------------------------------------

func TestValidateArtifactPathRejectsEscapesAndAbsolutePaths(t *testing.T) {
	for _, path := range []string{
		"",
		"   ",
		"/etc/passwd",
		"C:/Windows/system32",
		"../outside.md",
		"docs/../../outside.md",
		"docs/",
		"docs/\x00evil.md",
	} {
		if err := ValidateArtifactPath(path); err == nil {
			t.Errorf("unsafe artifact path %q was accepted", path)
		}
	}

	for _, path := range []string{
		"tasks/prd.md",
		"docs/nested/deep/notes.md",
		"plan.md",
	} {
		if err := ValidateArtifactPath(path); err != nil {
			t.Errorf("safe artifact path %q was rejected: %v", path, err)
		}
	}
}

func TestValidateReportsUnsafeArtifactPathsInContent(t *testing.T) {
	objective, content := validContent()
	content.Artifacts = []ProposedArtifact{{ID: "art-1", Kind: ArtifactPRD, Path: "../../escape.md"}}

	result := ValidatePlanContent(objective, content, ValidationContext{})
	if !hasCode(result, IssueUnsafeArtifactPath) {
		t.Fatalf("unsafe artifact path not reported: %v", codes(result))
	}
	if !errors.Is(result.Error(), ErrUnsafePath) {
		t.Errorf("error = %v, want ErrUnsafePath", result.Error())
	}
}
