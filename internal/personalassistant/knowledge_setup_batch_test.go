package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func setupBatchProposal(name string) KnowledgeProposal {
	return KnowledgeProposal{
		SourceKind: SetupBatchSourceKind, ScopeID: "ws-curator:root-1", SubjectID: name,
		Predicate: "deals with", Value: name, Category: "people",
		Text: fmt.Sprintf("You deal with %s (seen in 2 files you filed).", name),
		Evidence: []KnowledgeEvidence{
			{SourceKind: SetupBatchSourceKind, SourceID: SetupBatchEvidenceID("ws-curator", "action-1-"+name), Summary: "User-approved filing of a file whose name includes " + name},
			{SourceKind: SetupBatchSourceKind, SourceID: SetupBatchEvidenceID("ws-curator", "action-2-"+name), Summary: "User-approved filing of a file whose name includes " + name},
		},
	}
}

// The setup beat has a pool of its own: 12 pending, no rolling daily limit (FR18).
func TestSetupBatchPool_TwelvePendingAndNoDailyLimit(t *testing.T) {
	_, svc, now := newWorkspaceProposalService(t)
	ctx := context.Background()
	var admitted []KnowledgeItem
	for i := range setupBatchPendingLimit {
		item, created, err := svc.Propose(ctx, "local", setupBatchProposal(fmt.Sprintf("Company%c", 'A'+i)))
		if err != nil || !created {
			t.Fatalf("learning %d: created=%v err=%v", i, created, err)
		}
		admitted = append(admitted, item)
	}
	// More than the shared pool's 3 a day went in at one instant, so there is no
	// rolling limit; the pending cap is the only one.
	if _, _, err := svc.Propose(ctx, "local", setupBatchProposal("CompanyZ")); !errors.Is(err, ErrKnowledgeQuota) {
		t.Fatalf("13th pending learning = %v, want ErrKnowledgeQuota", err)
	}
	// Answering some frees room straight away, in the same 24 hours.
	for _, item := range admitted[:3] {
		if _, err := svc.SuppressCandidate(ctx, "local", item.ID, item.Version, KnowledgeRejected); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"CompanyW", "CompanyX", "CompanyY"} {
		if _, created, err := svc.Propose(ctx, "local", setupBatchProposal(name)); err != nil || !created {
			t.Fatalf("after answering, %s: created=%v err=%v", name, created, err)
		}
	}
	if _, _, err := svc.Propose(ctx, "local", setupBatchProposal("CompanyV")); !errors.Is(err, ErrKnowledgeQuota) {
		t.Fatalf("pending cap after refilling = %v", err)
	}
	_ = now
}

// The pools do not touch each other: the saved-app and Janitor suggestions keep
// their 6 pending and 3 a day, whether or not the beat's pool is full (FR18).
func TestSetupBatchPool_LeavesTheSharedAndWorkspacePoolsAlone(t *testing.T) {
	_, svc, now := newWorkspaceProposalService(t)
	ctx := context.Background()
	for i := range setupBatchPendingLimit {
		if _, _, err := svc.Propose(ctx, "local", setupBatchProposal(fmt.Sprintf("Company%c", 'A'+i))); err != nil {
			t.Fatal(err)
		}
	}
	// A full setup pool does not block the shared pool's three a day...
	for _, id := range []string{"app-a", "app-b", "app-c"} {
		if _, created, err := svc.Propose(ctx, "local", testProposal(id, "Work-style hypothesis for "+id)); err != nil || !created {
			t.Fatalf("shared pool blocked by the setup pool: %s: %v", id, err)
		}
	}
	// ...which still stops at three, and the setup items did not use it up.
	if _, _, err := svc.Propose(ctx, "local", testProposal("app-d", "A fourth in a day")); !errors.Is(err, ErrKnowledgeQuota) {
		t.Fatalf("shared pool's daily limit = %v, want ErrKnowledgeQuota", err)
	}
	doc, err := svc.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Admissions) != 3 {
		t.Fatalf("setup learnings entered the shared admissions: %v", doc.Admissions)
	}
	// The shared pool's six pending are counted without the setup items.
	*now = now.Add(24 * time.Hour)
	for _, id := range []string{"app-e", "app-f", "app-g"} {
		if _, _, err := svc.Propose(ctx, "local", testProposal(id, "Work-style hypothesis for "+id)); err != nil {
			t.Fatalf("shared pending cap counted setup items: %s: %v", id, err)
		}
	}
	if _, _, err := svc.Propose(ctx, "local", testProposal("app-h", "A seventh pending")); !errors.Is(err, ErrKnowledgeQuota) {
		t.Fatalf("shared pending cap = %v, want ErrKnowledgeQuota", err)
	}
	// And a workspace agent's pool is as it was.
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "A workspace fact.")); err != nil {
		t.Fatalf("workspace pool blocked: %v", err)
	}
}

func TestSetupBatchProposal_OnlyPeopleAndOnlyItsOwnEvidence(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	wrongCategory := setupBatchProposal("Acme")
	wrongCategory.Category = "how_you_work"
	if _, _, err := svc.Propose(ctx, "local", wrongCategory); !errors.Is(err, ErrValidation) {
		t.Fatalf("a setup learning in another category = %v", err)
	}
	foreign := setupBatchProposal("Acme")
	foreign.Evidence[0].SourceKind = "file_janitor"
	if _, _, err := svc.Propose(ctx, "local", foreign); !errors.Is(err, ErrValidation) {
		t.Fatalf("foreign evidence = %v", err)
	}
	// A learning is one line however it was written.
	multiline := setupBatchProposal("Acme")
	multiline.Text = "You deal with Acme\n(seen in 2 files you filed)."
	item, created, err := svc.Propose(ctx, "local", multiline)
	if err != nil || !created {
		t.Fatalf("a multi-line learning: created=%v err=%v", created, err)
	}
	view, err := svc.ReviewItems(ctx, "local")
	if err != nil || len(view) != 1 || view[0].ID != item.ID || strings.ContainsAny(view[0].Text, "\r\n") {
		t.Fatalf("stored learning = %+v, %v", view, err)
	}
}

func TestSetupBatchEvidenceID_RoundTripsAndRefusesAnythingElse(t *testing.T) {
	id := SetupBatchEvidenceID("ws-1", "action-9")
	workspaceID, actionID, ok := SetupBatchEvidenceParts(id)
	if !ok || workspaceID != "ws-1" || actionID != "action-9" {
		t.Fatalf("parts = %q %q %v", workspaceID, actionID, ok)
	}
	for _, bad := range []string{"", "action-only", ":action", "ws:"} {
		if _, _, ok := SetupBatchEvidenceParts(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
