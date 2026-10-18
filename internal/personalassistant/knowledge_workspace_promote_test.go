package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func promoteRequest(text string) WorkspacePromoteRequest {
	return WorkspacePromoteRequest{WorkspaceID: "ws-album", Category: "how_you_work", Text: text, Original: text}
}

func TestPromoteWorkspaceEntry_ApprovesTheFactWithWorkspaceEvidence(t *testing.T) {
	f, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	req := promoteRequest("John delivers stems at 48 kHz.")
	req.Original = "Stems go out at 48k"
	result, item, err := svc.PromoteWorkspaceEntry(ctx, "local", req)
	if err != nil || result != WorkspacePromoted || item.State != KnowledgeApproved || item.SourceKind != "explicit" {
		t.Fatalf("promote = %q %+v %v", result, item, err)
	}
	if item.Target == nil || !strings.HasPrefix(item.Target.Marker, "ori-hq:") {
		t.Fatalf("promoted fact has no canonical marker: %+v", item.Target)
	}
	evidence := item.Revisions[0].Evidence
	if len(evidence) != 1 || evidence[0].SourceKind != WorkspacePromoteSourceKind || evidence[0].SourceID != "ws-album" ||
		evidence[0].Summary != "Stems go out at 48k" {
		t.Fatalf("evidence = %+v", evidence)
	}
	snapshot, err := f.memory.SnapshotExact("hq-local")
	if err != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].Entry.Text != "John delivers stems at 48 kHz." {
		t.Fatalf("HQ memory = %+v %v", snapshot.Entries, err)
	}
	doc, err := svc.store.Read(ctx, "local")
	if err != nil || len(doc.Admissions) != 0 {
		t.Fatalf("promote consumed proposal quota: %+v %v", doc.Admissions, err)
	}
	items, err := svc.ReviewItems(ctx, "local")
	if err != nil || len(items) != 1 || items[0].Source.Kind != "explicit" || items[0].Source.WorkspaceID != "ws-album" ||
		items[0].Text != "John delivers stems at 48 kHz." {
		t.Fatalf("promoted review item = %+v %v", items, err)
	}
}

func TestPromoteWorkspaceEntry_IsIdempotentByMeaning(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	if result, _, err := svc.PromoteWorkspaceEntry(ctx, "local", promoteRequest("John delivers stems at 48 kHz.")); err != nil || result != WorkspacePromoted {
		t.Fatalf("first promote = %q %v", result, err)
	}
	for _, text := range []string{"John delivers stems at 48 kHz.", "john  delivers STEMS at 48 kHz."} {
		if result, _, err := svc.PromoteWorkspaceEntry(ctx, "local", promoteRequest(text)); err != nil || result != WorkspacePromoteAlreadyKnown {
			t.Fatalf("repeat %q = %q %v", text, result, err)
		}
	}
	other := promoteRequest("John delivers stems at 48 kHz.")
	other.WorkspaceID = "ws-ep"
	if result, _, err := svc.PromoteWorkspaceEntry(ctx, "local", other); err != nil || result != WorkspacePromoteAlreadyKnown {
		t.Fatalf("same fact from another workspace = %q %v", result, err)
	}
	hq := promoteRequest("Anything in HQ is already global.")
	hq.WorkspaceID = "hq-local"
	if result, _, err := svc.PromoteWorkspaceEntry(ctx, "local", hq); err != nil || result != WorkspacePromoteAlreadyKnown {
		t.Fatalf("HQ entry = %q %v", result, err)
	}
	doc, err := svc.store.Read(ctx, "local")
	if err != nil || len(doc.Items) != 1 {
		t.Fatalf("repeats created items: %+v %v", doc.Items, err)
	}
}

func TestPromoteWorkspaceEntry_PendingProposalAndForgetThenPromoteAgain(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John mixes at night.")); err != nil {
		t.Fatal(err)
	}
	if result, item, err := svc.PromoteWorkspaceEntry(ctx, "local", promoteRequest("John mixes at night.")); err != nil ||
		result != WorkspacePromoteAlreadyPending || item.State != KnowledgeCandidate {
		t.Fatalf("pending proposal = %q %+v %v", result, item, err)
	}

	_, first, err := svc.PromoteWorkspaceEntry(ctx, "local", promoteRequest("John masters at -14 LUFS."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ForgetItem(ctx, "local", first.ID, first.Version, "forget-lufs"); err != nil {
		t.Fatal(err)
	}
	// The user can deliberately promote the same wording again after Forget.
	result, again, err := svc.PromoteWorkspaceEntry(ctx, "local", promoteRequest("John masters at -14 LUFS."))
	if err != nil || result != WorkspacePromoted || again.ID == first.ID || again.State != KnowledgeApproved {
		t.Fatalf("promote after forget = %q %+v %v", result, again, err)
	}
}

func TestPromoteWorkspaceEntry_RefusesWithoutGlobalMemoryOrWithABadCategory(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	if _, _, err := svc.PromoteWorkspaceEntry(ctx, "someone-else", promoteRequest("A fact.")); !errors.Is(err, ErrGlobalMemoryUnavailable) {
		t.Fatalf("no assistant = %v", err)
	}
	if _, err := svc.GlobalMemoryWorkspace(ctx, "someone-else"); !errors.Is(err, ErrGlobalMemoryUnavailable) {
		t.Fatalf("no assistant workspace = %v", err)
	}
	if hq, err := svc.GlobalMemoryWorkspace(ctx, "local"); err != nil || hq != "hq-local" {
		t.Fatalf("global memory workspace = %q %v", hq, err)
	}
	for _, category := range []string{"sources", "you", ""} {
		req := promoteRequest("A fact.")
		req.Category = category
		if _, _, err := svc.PromoteWorkspaceEntry(ctx, "local", req); !errors.Is(err, ErrValidation) {
			t.Fatalf("category %q = %v", category, err)
		}
	}
	secret := promoteRequest("the api key is sk-abc1234567890def")
	if _, _, err := svc.PromoteWorkspaceEntry(ctx, "local", secret); !errors.Is(err, ErrValidation) {
		t.Fatalf("secret-looking text = %v", err)
	}
}
