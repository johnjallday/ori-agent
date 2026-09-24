package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestGenericProfileToolFailsClosedWhenHiredHQReceiptMetadataIsLost(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	if err := f.profileStore.Upsert(ctx, &userprofile.UserProfile{ID: "local"}); err != nil {
		t.Fatal(err)
	}
	current, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	guard := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	if err := guard.GuardGenericProfileWrite(ctx, "local", current, map[string]any{"preferences.response_style": "concise"}); !errors.Is(err, ErrRepairNeeded) {
		t.Fatalf("missing metadata could be a lost Forget receipt: %v", err)
	}
	if err := guard.GuardGenericProfileWrite(ctx, "local", current, map[string]any{"about": "ordinary About"}); err != nil {
		t.Fatalf("unrelated About should remain available without interview metadata: %v", err)
	}
}

func TestGenericProfileToolCannotRestoreInterviewPreferenceAfterForgetOrOutsideEdit(t *testing.T) {
	ctx := context.Background()
	f, interview, version := interviewSaveFixture(t)
	initial, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	result, err := interview.SaveReviewed(ctx, "local", KnowledgeInterviewSaveRequest{
		StateVersion: version, RequestID: "guarded-profile-save", Rows: []KnowledgeInterviewReviewedRow{{
			RowID: "communication", Category: "how_you_work", Destination: "profile", Text: "concise",
			Preference: "response_style", ExpectedProfileValue: "brief", ExpectedProfileUpdatedAt: initial.UpdatedAt,
		}},
	})
	if err != nil || result.Status != KnowledgeInterviewCompleted {
		t.Fatalf("complete reviewed preference: %+v %v", result, err)
	}
	guard := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	current, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.GuardGenericProfileWrite(ctx, "local", current, map[string]any{"preferences.response_style": "concise"}); err != nil {
		t.Fatalf("duplicate current wording: %v", err)
	}
	if _, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.response_style", current.UpdatedAt, "concise", ""); err != nil {
		t.Fatal(err)
	}
	current, err = f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	for _, value := range []any{"concise", " concise "} {
		if err := restarted.GuardGenericProfileWrite(ctx, "local", current, map[string]any{
			"preferences.response_style": value, "about": "A bundled tool update",
		}); !errors.Is(err, workspace.ErrMemoryManaged) {
			t.Fatalf("old interview wording restored by global tool after restart: %v", err)
		}
	}
	if err := restarted.GuardGenericProfileWrite(ctx, "local", current, map[string]any{"preferences.response_style": "clearer"}); err != nil {
		t.Fatalf("unrelated new preference should retain legacy profile_set behavior: %v", err)
	}
	if err := restarted.GuardGenericProfileWrite(ctx, "local", current, map[string]any{"about": "Unrelated About"}); err != nil {
		t.Fatalf("About tool write should not be mistaken for reviewed preference: %v", err)
	}
	root, err := f.folder.GetFolderPath("hq-local")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, workspace.SidecarDirName, knowledgeFileName)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.GuardGenericProfileWrite(ctx, "local", current, map[string]any{"preferences.response_style": "concise"}); !errors.Is(err, ErrRepairNeeded) {
		t.Fatalf("deleted interview receipts must fail closed, not restore old wording: %v", err)
	}
}
