package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

type reviewTicketFixture struct {
	mu        sync.Mutex
	tickets   map[string]string
	offline   string
	loseReply bool
}

func (f *reviewTicketFixture) Choices(string) []InboxWorkspace {
	return []InboxWorkspace{{"house", "House", "house"}, {"garden", "Garden", "garden"}}
}
func (f *reviewTicketFixture) Available(owner, id string) bool {
	return owner == "local" && id != f.offline && (id == "house" || id == "garden")
}
func (f *reviewTicketFixture) Confirm(_, ws, key, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := ws + ":" + key
	f.tickets[k] = "ticket-" + ws
	if f.loseReply {
		f.loseReply = false
		return "", errors.New("reply lost")
	}
	return f.tickets[k], nil
}

func reviewFixture(t *testing.T) (*SecondBrainService, *reviewTicketFixture, InboxDecision, string) {
	t.Helper()
	ctx := context.Background()
	h, brain := createdHarness(t)
	s := h.service
	f := &reviewTicketFixture{tickets: map[string]string{}}
	s.deps.InboxTickets = f
	s.deps.ReviewLedger = &secondbrain.ReviewLedger{DB: openTestDB(t).DB}
	v, err := s.ConnectionStatus(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	cap, err := s.CaptureForSource(ctx, "local", "Compare both quotes. Ask Maya about disposal.", "capture", v.BrowseRef)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(cap.Note)
	p, err := s.Preview(ctx, "local", "inbox", name, v.BrowseRef)
	if err != nil || len(p.Suggestions) != 2 {
		t.Fatalf("expected siblings: %v", err)
	}
	a := p.Suggestions[0]
	return s, f, InboxDecision{SourceRef: v.BrowseRef, Name: name, ActionID: a.ID, Revision: a.Revision, Decision: "accept", Workspace: "house"}, filepath.Join(brain, cap.Note)
}
func TestInboxDecision_ClaimPinsDestinationAndRecoversLostReply(t *testing.T) {
	s, f, in, _ := reviewFixture(t)
	ctx := context.Background()
	f.loseReply = true
	if _, err := s.DecideInbox(ctx, "local", in); err == nil {
		t.Fatal("ambiguous write reported success")
	}
	f.offline = "house"
	in.Workspace = "garden"
	if _, err := s.DecideInbox(ctx, "local", in); err == nil {
		t.Fatal("missing claimed workspace retargeted")
	}
	f.offline = ""
	a, err := s.DecideInbox(ctx, "local", in)
	if err != nil || a.State != "accepted" || a.Workspace != "house" || a.Ticket != "ticket-house" || len(f.tickets) != 1 {
		t.Fatalf("failed to repair first result: %v", err)
	}
}
func TestInboxDecision_ConcurrentChoicesAreOneDecision(t *testing.T) {
	s, f, in, _ := reviewFixture(t)
	var wg sync.WaitGroup
	for _, ws := range []string{"house", "garden", ""} {
		wg.Add(1)
		go func(ws string) {
			defer wg.Done()
			req := in
			req.Workspace = ws
			if ws == "" {
				req.Decision = "dismiss"
			}
			_, err := s.DecideInbox(context.Background(), "local", req)
			if err != nil {
				t.Errorf("decision: %v", err)
			}
		}(ws)
	}
	wg.Wait()
	a, err := s.DecideInbox(context.Background(), "local", in)
	if err != nil || !(a.State == "accepted" && len(f.tickets) == 1 || a.State == "dismissed" && len(f.tickets) == 0) {
		t.Fatalf("first decision lost: %v", err)
	}
}
func TestInboxDecision_RejectsUnavailableOrStaleAndKeepsSiblings(t *testing.T) {
	s, f, in, path := reviewFixture(t)
	ctx := context.Background()
	in.Workspace = "missing"
	if _, err := s.DecideInbox(ctx, "local", in); err == nil {
		t.Fatal("missing target accepted")
	}
	in.Decision, in.Workspace = "dismiss", ""
	if a, err := s.DecideInbox(ctx, "local", in); err != nil || a.State != "dismissed" {
		t.Fatal(err)
	}
	p, err := s.Preview(ctx, "local", "inbox", in.Name, in.SourceRef)
	if err != nil || len(p.Suggestions) != 2 || p.Suggestions[1].State != "pending" {
		t.Fatal("dismissed sibling")
	}
	in.ActionID = p.Suggestions[1].ID
	in.Decision, in.Workspace = "accept", "garden"
	if err := os.WriteFile(path, []byte("mine\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideInbox(ctx, "local", in); err == nil {
		t.Fatal("changed input accepted")
	}
	if len(f.tickets) != 0 {
		t.Fatal("unexpected tickets")
	}
}
