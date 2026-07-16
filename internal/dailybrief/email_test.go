package dailybrief

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeMailbox struct {
	threads []EmailThreadSnapshot
	err     error
}

func (f fakeMailbox) BriefEmailThreads(ctx context.Context, userID string) ([]EmailThreadSnapshot, error) {
	return f.threads, f.err
}

func emailRef(id string) SourceRef {
	return SourceRef{WorkspaceID: "hq-1", EntityType: "email_thread", EntityID: id, AccountID: "acct-1", Timestamp: time.Unix(1_700_000_000, 0)}
}

// emailSources returns a valid-but-empty workspace source plus the given mailbox,
// so BuildSnapshot proceeds past the workspace stage to email collection.
func emailSources(mb MailboxSource) SnapshotSources {
	return SnapshotSources{Workspaces: workspace.NewInMemoryStore(), Mailbox: mb}
}

func TestBuildSnapshot_EmailHealthyThreadsNoGap(t *testing.T) {
	mb := fakeMailbox{threads: []EmailThreadSnapshot{
		{Ref: emailRef("t1"), Subject: "Need review", From: "dana@x.com", WaitingOnUser: true, Unread: true},
	}}
	snap := BuildSnapshot(context.Background(), emailSources(mb), Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.EmailThreads) != 1 {
		t.Fatalf("expected 1 email thread, got %d", len(snap.EmailThreads))
	}
	if len(snap.Gaps) != 0 {
		t.Fatalf("healthy email must not add a gap, got %v", snap.Gaps)
	}
	if _, ok := snap.AllRefs()[emailRef("t1").Key()]; !ok {
		t.Fatal("email ref must be in the allowlist")
	}
}

func TestBuildSnapshot_EmailNotConfiguredIsNotAGap(t *testing.T) {
	mb := fakeMailbox{err: ErrEmailNotConfigured}
	snap := BuildSnapshot(context.Background(), emailSources(mb), Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.EmailThreads) != 0 || len(snap.Gaps) != 0 {
		t.Fatalf("not-configured email must be silent (no threads, no gap), got threads=%d gaps=%v", len(snap.EmailThreads), snap.Gaps)
	}
}

func TestBuildSnapshot_EmailReadFailureAddsGap(t *testing.T) {
	mb := fakeMailbox{err: errors.New("rate limited")}
	snap := BuildSnapshot(context.Background(), emailSources(mb), Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.Gaps) != 1 {
		t.Fatalf("a failed email read must add exactly one gap, got %v", snap.Gaps)
	}
	if len(snap.EmailThreads) != 0 {
		t.Fatal("a failed email read must yield no threads")
	}
}

func TestBuildSnapshot_EmailHealthyEmptyIsNotAGap(t *testing.T) {
	snap := BuildSnapshot(context.Background(), emailSources(fakeMailbox{}), Config{Scope: ScopeAll}, "local", time.Now())
	if len(snap.Gaps) != 0 {
		t.Fatalf("healthy-empty email must not add a gap, got %v", snap.Gaps)
	}
}

func TestComputeNeedsAttention_EmailWaitingOnUserSurfaced(t *testing.T) {
	snap := Snapshot{EmailThreads: []EmailThreadSnapshot{
		{Ref: emailRef("t1"), Subject: "Contract", From: "dana@x.com", WaitingOnUser: true},
		{Ref: emailRef("t2"), Subject: "FYI newsletter", From: "news@x.com", Unread: true},
		{Ref: emailRef("t3"), Subject: "Read already", From: "bob@x.com"}, // neither waiting nor unread → skipped
	}}
	items := ComputeNeedsAttention(snap)
	var reasons []string
	for _, it := range items {
		reasons = append(reasons, it.Reason)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 email attention items (waiting + unread), got %d: %v", len(items), reasons)
	}
	// Waiting-on-user must outrank a merely-unread thread.
	if items[0].Reason != "email_waiting_on_user" {
		t.Fatalf("waiting-on-user should rank first, got %v", reasons)
	}
	// Each item keeps its own source ref (no title-only aggregation).
	if items[0].Ref.EntityID != "t1" {
		t.Fatalf("attention item lost its source ref: %+v", items[0])
	}
}

func TestComputeNeedsAttention_EmailRespectsGlobalCap(t *testing.T) {
	var threads []EmailThreadSnapshot
	for i := range 10 {
		threads = append(threads, EmailThreadSnapshot{Ref: emailRef(string(rune('a' + i))), Subject: "s", WaitingOnUser: true})
	}
	items := ComputeNeedsAttention(Snapshot{EmailThreads: threads})
	if len(items) > maxAttentionItems {
		t.Fatalf("email must respect the global attention cap of %d, got %d", maxAttentionItems, len(items))
	}
}
