package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func externalRequest(text string) ExternalPromoteRequest {
	return ExternalPromoteRequest{
		Source: "codex", EntryID: "0123456789abcdef0123456789abcdef",
		Category: "how_you_work", Text: text, Title: "User preferences",
	}
}

func TestPromoteExternalEntry_ApprovesTheFactWithExternalEvidence(t *testing.T) {
	f, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	result, item, err := svc.PromoteExternalEntry(ctx, "local", externalRequest("John reports backtests net of fees."))
	if err != nil || result != WorkspacePromoted || item.State != KnowledgeApproved || item.SourceKind != "explicit" {
		t.Fatalf("promote = %q %+v %v", result, item, err)
	}
	evidence := item.Revisions[0].Evidence
	if len(evidence) != 1 || evidence[0].SourceKind != ExternalMemorySourceKind ||
		evidence[0].SourceID != "external:codex:0123456789abcdef0123456789abcdef" || evidence[0].Summary != "User preferences" {
		t.Fatalf("evidence = %+v", evidence)
	}
	snapshot, err := f.memory.SnapshotExact("hq-local")
	if err != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].Entry.Text != "John reports backtests net of fees." {
		t.Fatalf("HQ memory = %+v %v", snapshot.Entries, err)
	}
	items, err := svc.ReviewItems(ctx, "local")
	if err != nil || len(items) != 1 || items[0].Source.External != "codex" ||
		items[0].Source.ExternalEntryID != "0123456789abcdef0123456789abcdef" || items[0].Source.WorkspaceID != "" {
		t.Fatalf("review item source = %+v %v", items, err)
	}
}

func TestPromoteExternalEntry_SecondPromoteLinksTheExistingItem(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	_, first, err := svc.PromoteExternalEntry(ctx, "local", externalRequest("John reports backtests net of fees."))
	if err != nil {
		t.Fatal(err)
	}
	// The entry remembers the item; promoting it again, even with new wording,
	// links the existing item and creates nothing.
	again := externalRequest("A different wording of the same entry.")
	again.PromotedItemID = first.ID
	result, item, err := svc.PromoteExternalEntry(ctx, "local", again)
	if err != nil || result != WorkspacePromoteAlreadyKnown || item.ID != first.ID {
		t.Fatalf("second promote = %q %+v %v", result, item, err)
	}
	// Without the link, the same meaning is still found.
	result, item, err = svc.PromoteExternalEntry(ctx, "local", externalRequest("john reports backtests  NET of fees."))
	if err != nil || result != WorkspacePromoteAlreadyKnown || item.ID != first.ID {
		t.Fatalf("same meaning = %q %+v %v", result, item, err)
	}
	doc, err := svc.store.Read(ctx, "local")
	if err != nil || len(doc.Items) != 1 {
		t.Fatalf("repeats created items: %d %v", len(doc.Items), err)
	}
}

func TestPromoteExternalEntry_AfterForgetPromotesAgain(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	_, first, err := svc.PromoteExternalEntry(ctx, "local", externalRequest("John masters at -14 LUFS."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ForgetItem(ctx, "local", first.ID, first.Version, "forget-external"); err != nil {
		t.Fatal(err)
	}
	req := externalRequest("John masters at -14 LUFS.")
	req.PromotedItemID = first.ID
	result, again, err := svc.PromoteExternalEntry(ctx, "local", req)
	if err != nil || result != WorkspacePromoted || again.ID == first.ID {
		t.Fatalf("promote after forget = %q %+v %v", result, again, err)
	}
}

func TestPromoteExternalEntry_Refusals(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	if _, _, err := svc.PromoteExternalEntry(ctx, "someone-else", externalRequest("A fact.")); !errors.Is(err, ErrGlobalMemoryUnavailable) {
		t.Fatalf("no assistant = %v", err)
	}
	for name, mutate := range map[string]func(*ExternalPromoteRequest){
		"bad category": func(r *ExternalPromoteRequest) { r.Category = "sources" },
		"no entry":     func(r *ExternalPromoteRequest) { r.EntryID = "" },
		"path-like id": func(r *ExternalPromoteRequest) { r.EntryID = "../x" },
		"too long":     func(r *ExternalPromoteRequest) { r.Text = strings.Repeat("fact ", 120) },
		"secret text":  func(r *ExternalPromoteRequest) { r.Text = "the api key is sk-abc1234567890def" },
	} {
		req := externalRequest("A fact.")
		mutate(&req)
		if _, _, err := svc.PromoteExternalEntry(ctx, "local", req); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s = %v", name, err)
		}
	}
	// A title that is not one safe line is simply left out of the evidence.
	req := externalRequest("A fact with a messy title.")
	req.Title = "line one\nline two"
	if _, item, err := svc.PromoteExternalEntry(ctx, "local", req); err != nil || item.Revisions[0].Evidence[0].Summary != "" {
		t.Fatalf("messy title = %+v %v", item, err)
	}
}
