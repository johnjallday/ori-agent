package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Hard bounds and deletion guards (FR-42–FR-45, FR-17).
//
// Two properties matter more than the numbers themselves. Over-limit content is
// refused WHOLE and never truncated — silently dropping a user's twenty-first
// group would lose work they wrote and could not see was gone. And a Plan that
// produced anything is never hard-deleted, so materialized work cannot vanish
// behind a delete button.

// contentWithGroups builds content with the requested number of groups, each
// holding one item.
func contentWithGroups(groups int) PlanContent {
	content := PlanContent{Execution: ExecutionPolicy{Mode: ExecutionStepThrough}}
	for index := range groups {
		content.Groups = append(content.Groups, TaskGroup{
			ID:    fmt.Sprintf("grp-%d", index),
			Title: fmt.Sprintf("Group %d", index),
			Items: []TaskItem{{
				ID:          fmt.Sprintf("itm-%d", index),
				Description: fmt.Sprintf("Step %d", index),
			}},
		})
	}
	return content
}

// contentWithItems builds one group holding the requested number of items.
func contentWithItems(items int) PlanContent {
	group := TaskGroup{ID: "grp-1", Title: "Everything"}
	for index := range items {
		group.Items = append(group.Items, TaskItem{
			ID:          fmt.Sprintf("itm-%d", index),
			Description: fmt.Sprintf("Step %d", index),
		})
	}
	return PlanContent{
		Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
		Groups:    []TaskGroup{group},
	}
}

// --- Exact boundaries (FR-42) ----------------------------------------------

// The limit itself is ACCEPTED. An off-by-one here would refuse a plan the
// product says is fine, and the user would have no way to tell which.
func TestExactlyTheGroupLimitIsAccepted(t *testing.T) {
	result := ValidatePlanContent("Ship it", contentWithGroups(MaxTaskGroups), ValidationContext{})
	if !result.OK() {
		t.Errorf("%d groups was refused at the limit: %+v", MaxTaskGroups, result.Issues)
	}
}

func TestOneGroupOverTheLimitIsRefused(t *testing.T) {
	result := ValidatePlanContent("Ship it", contentWithGroups(MaxTaskGroups+1), ValidationContext{})
	if result.OK() {
		t.Fatalf("%d groups was accepted", MaxTaskGroups+1)
	}
	if !hasCode(result, IssueTooManyGroups) {
		t.Errorf("wrong issue for too many groups: %+v", result.Issues)
	}
}

func TestExactlyTheItemLimitIsAccepted(t *testing.T) {
	result := ValidatePlanContent("Ship it", contentWithItems(MaxTaskItems), ValidationContext{})
	if !result.OK() {
		t.Errorf("%d items was refused at the limit: %+v", MaxTaskItems, result.Issues)
	}
}

func TestOneItemOverTheLimitIsRefused(t *testing.T) {
	result := ValidatePlanContent("Ship it", contentWithItems(MaxTaskItems+1), ValidationContext{})
	if result.OK() {
		t.Fatalf("%d items was accepted", MaxTaskItems+1)
	}
	if !hasCode(result, IssueTooManyItems) {
		t.Errorf("wrong issue for too many items: %+v", result.Issues)
	}
}

// Oversized content is refused whole. Truncating would drop work the user
// wrote without telling them which part went (FR-43).
func TestOversizedContentIsRefusedNotTruncated(t *testing.T) {
	content := contentWithItems(5)
	// One enormous detail field pushes the canonical form past the byte limit.
	content.Groups[0].Items[0].Details = strings.Repeat("x", MaxContentBytes+1024)

	result := ValidatePlanContent("Ship it", content, ValidationContext{})
	if result.OK() {
		t.Fatal("oversized content was accepted")
	}
	if !hasCode(result, IssueContentTooLarge) {
		t.Errorf("wrong issue for oversized content: %+v", result.Issues)
	}
	// The content the caller handed in is unchanged: validation reports, it
	// never edits.
	if len(content.Groups[0].Items[0].Details) != MaxContentBytes+1024 {
		t.Error("validation truncated the caller's content")
	}
	if len(content.Groups[0].Items) != 5 {
		t.Error("validation dropped items from the caller's content")
	}
}

// The refusal says what to do. "Too large" with no next step leaves the user
// deleting things at random to find the threshold (FR-43).
func TestSizeRefusalsExplainWhatToDo(t *testing.T) {
	result := ValidatePlanContent("Ship it", contentWithGroups(MaxTaskGroups+1), ValidationContext{})
	for _, issue := range result.Issues {
		if issue.Code != IssueTooManyGroups {
			continue
		}
		if !strings.Contains(issue.Message, fmt.Sprint(MaxTaskGroups)) {
			t.Errorf("the message does not name the limit: %q", issue.Message)
		}
	}
}

// --- Version cap (FR-31) ---------------------------------------------------

// The 51st review version is refused, and refusing never deletes the fifty
// that exist: history is not a ring buffer.
func TestTheVersionCapRefusesRatherThanEvicting(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	// The cap is seeded directly into the store rather than driven through 50
	// review cycles. Requesting review moves the Plan to in_review, so reaching
	// the cap the long way would mean 50 round trips through the status machine
	// — testing the transition table, not the cap. The guard reads the stored
	// version count, and that is what this sets up.
	for version := 1; version <= MaxReviewVersions; version++ {
		seedVersion(t, ctx, service.Store(), plan.ID, fmt.Sprintf("hash-%d", version))
	}

	_, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if err == nil {
		t.Fatal("a 51st review version was accepted")
	}
	if !strings.Contains(err.Error(), "split") && !strings.Contains(err.Error(), "supersede") {
		t.Errorf("the refusal offers no way forward: %v", err)
	}

	// And every earlier version is still there.
	versions, listErr := service.Store().ListVersions(ctx, "ws-1", plan.ID)
	if listErr != nil {
		t.Fatalf("list versions: %v", listErr)
	}
	if len(versions) != MaxReviewVersions {
		t.Errorf("versions = %d, want %d retained", len(versions), MaxReviewVersions)
	}
}

// --- Deletion guards (FR-17) -----------------------------------------------

// A Plan that never produced anything may be hard-deleted; one that did, may
// not. Both stores must agree, because a guard that holds only in the fake
// protects nothing.
func TestStoreDeletionGuardsAgreeAcrossStores(t *testing.T) {
	forEachStore(t, func(t *testing.T, ctx context.Context, store Store, seed func(string)) {
		seed("ws-1")

		// Never approved, nothing linked: deletable.
		if err := store.CreatePlan(ctx, testPlan("plan-clean")); err != nil {
			t.Fatalf("create clean plan: %v", err)
		}
		if err := store.DeletePlan(ctx, "ws-1", "plan-clean"); err != nil {
			t.Errorf("a plan with no effects was refused deletion: %v", err)
		}

		// One with a linked Task: refused, and the plan survives.
		if err := store.CreatePlan(ctx, testPlan("plan-linked")); err != nil {
			t.Fatalf("create linked plan: %v", err)
		}
		if err := store.LinkTasks(ctx, "ws-1", "plan-linked", []TaskLink{{
			PlanID: "plan-linked", WorkspaceID: "ws-1", Version: 1,
			GroupID: "grp-1", ItemID: "itm-1", TaskID: "task-1", Role: LinkRoleItem,
		}}); err != nil {
			t.Fatalf("link task: %v", err)
		}
		err := store.DeletePlan(ctx, "ws-1", "plan-linked")
		if !errors.Is(err, ErrPlanNotDeletable) {
			t.Errorf("deleting a plan with linked work = %v, want not-deletable", err)
		}
		if _, err := store.GetPlan(ctx, "ws-1", "plan-linked"); err != nil {
			t.Errorf("the refused plan was deleted anyway: %v", err)
		}

		// One with an approval: also refused, even with no tasks yet.
		if err := store.CreatePlan(ctx, testPlan("plan-approved")); err != nil {
			t.Fatalf("create approved plan: %v", err)
		}
		version := seedVersion(t, ctx, store, "plan-approved", "hash-1")
		if _, err := store.CreateApproval(ctx, &Approval{
			PlanID: "plan-approved", WorkspaceID: "ws-1",
			Version: version.Number, ContentHash: version.ContentHash,
			Effect: EffectCreateTasks, IdempotencyKey: "guard-1",
			CreatedAt: version.CreatedAt,
		}); err != nil {
			t.Fatalf("create approval: %v", err)
		}
		if err := store.DeletePlan(ctx, "ws-1", "plan-approved"); !errors.Is(err, ErrPlanNotDeletable) {
			t.Errorf("deleting an approved plan = %v, want not-deletable", err)
		}
	})
}
