//go:build darwin || linux || freebsd

package folderagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type reviewChatFunc func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error)

func (f reviewChatFunc) Chat(ctx context.Context, r llm.ChatRequest) (*llm.ChatResponse, error) {
	return f(ctx, r)
}
func TestReviewRevocationDuringModelDiscardsResult(t *testing.T) {
	root := build(t, "budget.txt")
	h := newHarness(t, root)
	h.svc.deps.KnownFolderRoot = knownOnly("documents", root)
	id := h.start(t, "").WorkspaceID
	h.svc.Wait()
	rec, _ := h.svc.deps.Store.Read(id)
	granted := true
	h.svc.deps.DocumentsAccess = func(string, string) (DocumentsAccess, error) {
		if !granted {
			return DocumentsAccess{}, ErrNotOwner
		}
		return DocumentsAccess{root, rec.AgentName, "binding"}, nil
	}
	h.svc.deps.ReviewModel = func(string, string) (ReviewProcessor, ReviewChat) {
		return ReviewProcessor{"Example", "model", "pin"}, reviewChatFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
			granted = false
			return &llm.ChatResponse{Content: `{"groups":[]}`, FinishReason: "end_turn"}, nil
		})
	}
	p, err := h.svc.OrganizationPreview("u1", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.ApproveOrganization(t.Context(), "u1", id, p.Approval, ReviewSelection{}); err == nil {
		t.Fatal("result returned after revocation")
	}
	if rows, err := h.svc.OrganizationResults("u1", id); rows != nil || err == nil {
		t.Fatal("result reopened after revocation")
	}
	granted = true
	rows, err := h.svc.OrganizationResults("u1", id)
	if err != nil || len(rows) != 1 || rows[0].Status != "scope changed" || len(rows[0].Groups) > 0 {
		t.Fatal("revoked work published", err)
	}
}

func TestApprovedOrganizationReadIsBoundedAndNeverResent(t *testing.T) {
	root := build(t, "01.txt", "02.txt", "03.pdf", "Maple/ref.txt", "Oak/ref.txt")
	for _, name := range []string{"01.txt", "02.txt", "Maple/ref.txt", "Oak/ref.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("Kitchen remodel · PO-4387 · private-fixture-marker "+strings.Repeat("extra ", 400)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := newHarness(t, root)
	h.svc.deps.KnownFolderRoot = knownOnly("documents", root)
	id := h.start(t, "").WorkspaceID
	h.svc.Wait()
	rec, _ := h.svc.deps.Store.Read(id)
	granted := atomic.Bool{}
	granted.Store(true)
	h.svc.deps.DocumentsAccess = func(string, string) (DocumentsAccess, error) {
		if !granted.Load() {
			return DocumentsAccess{}, ErrNotOwner
		}
		return DocumentsAccess{root, rec.AgentName, "binding"}, nil
	}
	var calls, reads atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	pin := "original"
	h.svc.deps.ReviewRead = func(root, path string, cap int64, w string, v workspace.SourceVersion) (workspace.ContainedFile, error) {
		reads.Add(1)
		return workspace.ReadVersionedFile(root, path, cap, w, v)
	}
	h.svc.deps.ReviewModel = func(string, string) (ReviewProcessor, ReviewChat) {
		return ReviewProcessor{"Example cloud", "fixture", pin}, reviewChatFunc(func(ctx context.Context, r llm.ChatRequest) (*llm.ChatResponse, error) {
			calls.Add(1)
			if !r.Isolated || !r.SingleAttempt || r.MaxTokens != 1200 || r.ProviderConfiguration != "original" || len(r.Tools) > 0 || !strings.Contains(r.Messages[0].Content, "private-fixture-marker") || strings.Contains(r.Messages[0].Content, "01.txt") || strings.Contains(r.Messages[0].Content, "Oak") || len(r.Messages[0].Content) > 16<<10 {
				t.Error("unexpected request scope")
			}
			close(entered)
			<-release
			return &llm.ChatResponse{Content: `{"groups":[{"title":"Kitchen remodel","members":["s1","s2"],"links":[{"a":"s1","b":"s2","key":"PO-4387"}]}]}`, FinishReason: llm.FinishReasonStop}, nil
		})
	}
	preview := func(sel ReviewSelection) OrganizationPreview {
		t.Helper()
		p, err := h.svc.PreviewOrganization(t.Context(), "u1", id, sel)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	initial := preview(ReviewSelection{})
	sel := ReviewSelection{Examples: []string{initial.Folders[0].Ref}, Exclude: []string{initial.Options[2].Ref}}
	p := preview(sel)
	if len(p.Files) != 3 || p.Remaining != 1 || len(p.Options) != 4 || calls.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("preview %+v", p)
	}
	pin = "changed"
	if _, err := h.svc.ApproveOrganization(t.Context(), "u1", id, p.Approval, sel); err == nil {
		t.Fatal("changed processor accepted")
	}
	pin = "original"
	if _, err := h.svc.ApproveOrganization(t.Context(), "u1", id, p.Approval, ReviewSelection{}); err == nil || calls.Load() != 0 || reads.Load() != 0 {
		t.Fatal("changed selection accepted")
	}
	done := make(chan error, 1)
	go func() { _, err := h.svc.ApproveOrganization(t.Context(), "u1", id, p.Approval, sel); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("no call: %v", err)
	}
	again, err := h.svc.ApproveOrganization(t.Context(), "u1", id, p.Approval, sel)
	if err != nil || again.Status != reviewRunning {
		t.Errorf("duplicate %+v %v", again, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	again, err = h.svc.ApproveOrganization(t.Context(), "u1", id, p.Approval, sel)
	if err != nil || again.Status != "reviewed" || again.Read != 3 || len(again.Groups) != 1 || !again.Files[0].Truncated || calls.Load() != 1 || reads.Load() != 3 {
		t.Fatalf("result %+v %v", again, err)
	}
	rows, err := h.svc.OrganizationResults("u1", id)
	if err != nil || len(rows) != 1 {
		t.Fatal("missing receipt")
	}
	saved, _ := h.svc.deps.Store.Read(id)
	data, _ := json.Marshal(saved)
	if strings.Contains(string(data), "private-fixture-marker") {
		t.Fatal("raw excerpt persisted")
	}
	granted.Store(false)
	if rows, err := h.svc.OrganizationResults("u1", id); err == nil || rows != nil {
		t.Fatal("revoked access exposed result")
	}
}
