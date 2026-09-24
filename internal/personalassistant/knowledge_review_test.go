package personalassistant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestReviewReadsOnlyCurrentCanonicalApprovalAndKeepsStaleTextInert(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	s := approvedTestService(f)
	item, _, err := s.Propose(ctx, "local", testProposal("app-review", "Unapproved Obsidian suggestion"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.ReviewItems(ctx, "local")
	if err != nil || len(pending) != 1 || pending[0].State != KnowledgeCandidate || pending[0].Text != "Unapproved Obsidian suggestion" {
		t.Fatalf("candidate view: %+v %v", pending, err)
	}
	approved, err := s.ApproveCandidate(ctx, "local", item.ID, item.Version, "review-approve")
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.ReviewItems(ctx, "local")
	if err != nil || active[0].State != KnowledgeApproved || active[0].Text != "Unapproved Obsidian suggestion" {
		t.Fatalf("reviewed view: %+v %v", active, err)
	}
	// Even when the marker survives, an outside edit is the current owner.
	// Never show the stale sidecar revision as an approved current fact.
	file := filepath.Join(f.folder.path, workspace.MemoryFileName)
	raw, err := os.ReadFile(file) // #nosec G304 -- test fixture path under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(raw), "Unapproved Obsidian suggestion", "Outside change", 1)
	if err := os.WriteFile(file, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	stale, err := s.ReviewItems(ctx, "local")
	if err != nil || stale[0].State != KnowledgeNeedsReview || stale[0].Text != "" || stale[0].ReviewUnavailable != "canonical_target_changed" {
		t.Fatalf("stale canonical target surfaced as approved: %+v %v", stale, err)
	}
	if _, err := s.ForgetApproved(ctx, "local", approved.ID, approved.Version, "review-forget"); err == nil {
		t.Fatal("outside edit was silently deleted")
	}
}
