package workspaceplan

import (
	"context"
	"strings"
	"testing"
)

func TestApplyArtifactPolicyCreatesCanonicalRepositoryArtifacts(t *testing.T) {
	plan := &Plan{
		ID:              "plan_12345678-1234-1234-1234-123456789abc",
		Title:           "Renamed display title",
		OriginalRequest: "#292 Coordinate based map\nKeep this stable.",
	}
	content := reviewableContent()
	content.Artifacts = nil

	got, err := ApplyArtifactPolicy(plan, content, ArtifactPolicy{
		Apply: true, Directory: "tasks", WritePRD: true, WriteTaskList: true,
	})
	if err != nil {
		t.Fatalf("apply artifact policy: %v", err)
	}
	if len(got.Artifacts) != 2 {
		t.Fatalf("artifacts = %#v, want canonical PRD and task list", got.Artifacts)
	}

	feature := "292-coordinate-based-map-12345678"
	want := map[ArtifactKind]string{
		ArtifactPRD:      "tasks/prd-" + feature + ".md",
		ArtifactTaskList: "tasks/tasks-" + feature + ".md",
	}
	for _, artifact := range got.Artifacts {
		if artifact.Path != want[artifact.Kind] {
			t.Errorf("%s path = %q, want %q", artifact.Kind, artifact.Path, want[artifact.Kind])
		}
		if !artifact.Enabled {
			t.Errorf("%s was not enabled by compiled policy", artifact.Kind)
		}
		if artifact.ID == "" {
			t.Errorf("%s has no stable artifact id", artifact.Kind)
		}
	}
}

func TestApplyArtifactPolicyOverridesModelPathsAndDeduplicatesKinds(t *testing.T) {
	plan := &Plan{ID: "plan_abcdef12-1234-1234-1234-123456789abc", OriginalRequest: "Ship reports"}
	content := reviewableContent()
	content.Artifacts = []ProposedArtifact{
		{ID: "model-prd", Kind: ArtifactPRD, Path: "somewhere/model.md", Title: "Keep this title"},
		{ID: "duplicate-prd", Kind: ArtifactPRD, Path: "other.md"},
		{ID: "note", Kind: ArtifactNote, Path: "notes/context.md", Enabled: true},
	}

	got, err := ApplyArtifactPolicy(plan, content, ArtifactPolicy{
		Apply: true, Directory: "plans", WritePRD: true, WriteTaskList: false,
	})
	if err != nil {
		t.Fatalf("apply artifact policy: %v", err)
	}
	if len(got.Artifacts) != 2 {
		t.Fatalf("artifacts = %#v, want one PRD plus the unrelated note", got.Artifacts)
	}
	if got.Artifacts[0].ID != "model-prd" || got.Artifacts[0].Title != "Keep this title" {
		t.Fatalf("first proposal metadata was not preserved: %#v", got.Artifacts[0])
	}
	if got.Artifacts[0].Path != "plans/prd-ship-reports-abcdef12.md" || !got.Artifacts[0].Enabled {
		t.Fatalf("PRD was not canonicalized: %#v", got.Artifacts[0])
	}
	if got.Artifacts[1].Kind != ArtifactNote || got.Artifacts[1].Path != "notes/context.md" {
		t.Fatalf("non-planning artifact changed: %#v", got.Artifacts[1])
	}
}

func TestRequestReviewHashesCanonicalArtifactsRatherThanModelPaths(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	content := reviewableContent()
	content.Artifacts = []ProposedArtifact{{
		ID: "model-task-list", Kind: ArtifactTaskList, Path: "drafts/model-choice.md",
	}}
	plan := newReviewablePlan(t, ctx, service, content)

	version, err := service.RequestReview(ctx, testWorkspaceID, plan.ID, ReviewInput{
		Actor: "jj",
		Artifacts: ArtifactPolicy{
			Apply: true, Directory: "tasks", WritePRD: true, WriteTaskList: true,
		},
	})
	if err != nil {
		t.Fatalf("request review: %v", err)
	}
	feature := PlanFeatureSlug(plan)
	if len(version.Content.Artifacts) != 2 {
		t.Fatalf("review artifacts = %#v", version.Content.Artifacts)
	}
	for _, artifact := range version.Content.Artifacts {
		if !artifact.Enabled || !strings.Contains(artifact.Path, feature) {
			t.Fatalf("review version did not snapshot compiled artifact policy: %#v", artifact)
		}
	}
	if version.ContentHash == "" {
		t.Error("review version did not hash canonical output paths")
	}
}

func TestApplyArtifactPolicyRefusesUnsafeDirectory(t *testing.T) {
	plan := &Plan{ID: "plan_12345678", OriginalRequest: "Demo"}
	_, err := ApplyArtifactPolicy(plan, reviewableContent(), ArtifactPolicy{
		Apply: true, Directory: "../outside", WriteTaskList: true,
	})
	if err == nil || !strings.Contains(err.Error(), "planning output directory") {
		t.Fatalf("unsafe directory error = %v", err)
	}
}

func TestPlanFeatureSlugUsesImmutableRequestAndStableID(t *testing.T) {
	plan := &Plan{
		ID:              "plan_90abcdef-1234-1234-1234-123456789abc",
		OriginalRequest: "Build the résumé importer",
		Title:           "A title that can change",
	}
	first := PlanFeatureSlug(plan)
	plan.Title = "Renamed later"
	second := PlanFeatureSlug(plan)
	if first != second {
		t.Fatalf("feature identity changed after title edit: %q -> %q", first, second)
	}
	if first != "build-the-r-sum-importer-90abcdef" {
		t.Fatalf("feature slug = %q", first)
	}
}
