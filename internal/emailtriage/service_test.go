package emailtriage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/mailbox"
)

// memoryStore is the Store contract with no persistence.
type memoryStore struct {
	mu     sync.Mutex
	states map[Scope]map[string]State
	saves  int
}

func (m *memoryStore) Load(_ context.Context, scope Scope) (map[string]State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]State{}
	for id, state := range m.states[scope] {
		out[id] = state
	}
	return out, nil
}

func (m *memoryStore) Save(_ context.Context, scope Scope, states []State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saves++
	if m.states == nil {
		m.states = map[Scope]map[string]State{}
	}
	if m.states[scope] == nil {
		m.states[scope] = map[string]State{}
	}
	for _, state := range states {
		m.states[scope][state.ThreadID] = state
	}
	return nil
}

type fakeInbox struct {
	threads []mailbox.Thread
	queries []mailbox.Query
}

func (f *fakeInbox) SearchThreads(_ context.Context, _ mailbox.Account, q mailbox.Query) (mailbox.ThreadPage, error) {
	f.queries = append(f.queries, q)
	return mailbox.ThreadPage{Threads: f.threads}, nil
}

func (f *fakeInbox) GetThread(context.Context, mailbox.Account, string) (mailbox.Thread, error) {
	return mailbox.Thread{}, errors.New("not used")
}

var (
	account = mailbox.Account{ID: "acct", EmailAddress: owner}
	base    = time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
)

func mail(id, subject string, at time.Time, m mailbox.Message) mailbox.Thread {
	if m.To == nil && !m.Bulk && !m.AutoSubmitted {
		m.To = []mailbox.Participant{from(owner)}
	}
	m.Subject, m.SentAt = subject, at
	return mailbox.Thread{ID: id, Subject: subject, LastMessageAt: at, Messages: []mailbox.Message{m}}
}

func inbox() *fakeInbox {
	return &fakeInbox{threads: []mailbox.Thread{
		mail("t-sam", "Offsite", base, mailbox.Message{From: mailbox.Participant{Name: "Sam Lee", Address: "sam@example.com"}, Snippet: "Can you make Friday?"}),
		mail("t-news", "Sale", base.Add(-time.Hour), mailbox.Message{From: from("news@shop.example"), Bulk: true}),
		mail("t-bank", "Statement", base.Add(-2*time.Hour), mailbox.Message{From: from("no-reply@bank.example"), Snippet: "Payment due Oct 10"}),
		mail("t-done", "Thanks", base.Add(-3*time.Hour), mailbox.Message{From: from(owner), FromUser: true}),
	}}
}

func subjects(items []Item) string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Subject)
	}
	return strings.Join(out, ",")
}

func TestListSortsTheInboxByRulesAlone(t *testing.T) {
	svc := NewService(inbox(), &memoryStore{}, nil)
	list, err := svc.List(context.Background(), "ws", account)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if subjects(list.NeedsYou) != "Offsite" || subjects(list.FYI) != "Statement" || subjects(list.Ignorable) != "Sale" {
		t.Fatalf("needs=%q fyi=%q ignorable=%q", subjects(list.NeedsYou), subjects(list.FYI), subjects(list.Ignorable))
	}
	if list.NeedsYou[0].Why != "Sam wrote to you and hasn't had a reply." || list.NeedsYou[0].From != "Sam Lee" {
		t.Fatalf("row = %+v", list.NeedsYou[0])
	}
	if list.Explained {
		t.Fatal("no model ran, but the list claims its reasons were explained")
	}
}

func TestListAsksTheModelOnlyAboutWhatIsNew(t *testing.T) {
	box := inbox()
	store := &memoryStore{}
	model := &scriptedModel{answer: `[
		{"item": 1, "needs_you": true, "kind": "decision", "why": "Sam asks whether Friday works for the offsite."},
		{"item": 2, "needs_you": true, "kind": "deadline", "why": "Bank says a payment is due Oct 10."}
	]`}
	svc := NewService(box, store, func() Completer { return model })

	list, err := svc.List(context.Background(), "ws", account)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if subjects(list.NeedsYou) != "Offsite,Statement" || !list.Explained {
		t.Fatalf("needs you = %q explained=%v; want the model to promote the payment notice", subjects(list.NeedsYou), list.Explained)
	}
	if !box.queries[0].WithSnippets {
		t.Fatal("the list did not ask for snippets, so the model saw no text")
	}
	if strings.Contains(model.user, "Sale") {
		t.Fatal("a newsletter the rules settled was sent to the model")
	}

	// Nothing new: the saved verdicts stand and the model is not asked again.
	again, _ := svc.List(context.Background(), "ws", account)
	if model.calls != 1 || subjects(again.NeedsYou) != "Offsite,Statement" {
		t.Fatalf("second read: model calls %d, needs you %q", model.calls, subjects(again.NeedsYou))
	}

	// A new message in one thread: only that thread is looked at again.
	box.threads[0] = mail("t-sam", "Offsite", base.Add(time.Minute), mailbox.Message{From: from("sam@example.com"), Snippet: "Never mind, sorted."})
	model.answer = `[{"item": 1, "needs_you": false, "kind": "info", "why": "Sam says the offsite is sorted."}]`
	third, _ := svc.List(context.Background(), "ws", account)
	if model.calls != 2 || strings.Count(model.user, "<item number=") != 1 {
		t.Fatalf("model calls %d with %d items; want one call about the changed thread", model.calls, strings.Count(model.user, "<item number="))
	}
	if subjects(third.NeedsYou) != "Statement" || subjects(third.FYI) != "Offsite" {
		t.Fatalf("after the update: needs=%q fyi=%q", subjects(third.NeedsYou), subjects(third.FYI))
	}
}

func TestListKeepsTheRulesWhenTheModelFails(t *testing.T) {
	svc := NewService(inbox(), &memoryStore{}, func() Completer { return &scriptedModel{err: errors.New("model down")} })
	list, err := svc.List(context.Background(), "ws", account)
	if err != nil {
		t.Fatalf("a model failure failed the list: %v", err)
	}
	if subjects(list.NeedsYou) != "Offsite" || list.Explained {
		t.Fatalf("needs=%q explained=%v", subjects(list.NeedsYou), list.Explained)
	}
}

func TestTheUsersCallHoldsUntilANewMessage(t *testing.T) {
	box := inbox()
	svc := NewService(box, &memoryStore{}, nil)
	if _, err := svc.List(context.Background(), "ws", account); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := svc.SetBucket(context.Background(), "ws", account, "t-sam", BucketIgnorable); err != nil {
		t.Fatalf("SetBucket: %v", err)
	}
	if err := svc.SetBucket(context.Background(), "ws", account, "t-news", BucketNeedsYou); err != nil {
		t.Fatalf("SetBucket: %v", err)
	}
	list, _ := svc.List(context.Background(), "ws", account)
	if subjects(list.NeedsYou) != "Sale" || !strings.Contains(subjects(list.Ignorable), "Offsite") || !list.NeedsYou[0].ByYou {
		t.Fatalf("after the user's calls: needs=%q ignorable=%q", subjects(list.NeedsYou), subjects(list.Ignorable))
	}

	// Sam writes again: Ori looks afresh.
	box.threads[0] = mail("t-sam", "Offsite", base.Add(time.Hour), mailbox.Message{From: from("sam@example.com")})
	fresh, _ := svc.List(context.Background(), "ws", account)
	if !strings.Contains(subjects(fresh.NeedsYou), "Offsite") {
		t.Fatalf("a new message did not bring the thread back: needs=%q", subjects(fresh.NeedsYou))
	}

	if err := svc.SetBucket(context.Background(), "ws", account, "t-unknown", BucketIgnorable); !errors.Is(err, ErrUnknownThread) {
		t.Fatalf("unknown thread err = %v", err)
	}
	if err := svc.SetBucket(context.Background(), "ws", account, "t-sam", BucketHandled); err == nil {
		t.Fatal("a thread was marked handled by hand")
	}
}

func TestTrackMakesOneFollowUpPerThread(t *testing.T) {
	box := inbox()
	svc := NewService(box, &memoryStore{}, nil)
	if _, err := svc.List(context.Background(), "ws", account); err != nil {
		t.Fatalf("List: %v", err)
	}
	var captured []State
	capture := func(state State) (string, error) {
		captured = append(captured, state)
		return "fu-1", nil
	}
	state, err := svc.Track(context.Background(), "ws", account, "t-sam", capture)
	if err != nil || state.FollowUpID != "fu-1" {
		t.Fatalf("Track = %+v, %v", state, err)
	}
	if captured[0].Subject != "Offsite" || captured[0].From != "Sam Lee" || captured[0].Why == "" {
		t.Fatalf("capture saw %+v; want the subject, sender and reason", captured[0])
	}
	if _, err := svc.Track(context.Background(), "ws", account, "t-sam", capture); err != nil || len(captured) != 1 {
		t.Fatalf("a second Track made another follow-up: %d captures, %v", len(captured), err)
	}

	// The follow-up outlives a newer message in the thread.
	box.threads[0] = mail("t-sam", "Offsite", base.Add(time.Hour), mailbox.Message{From: from("sam@example.com")})
	list, _ := svc.List(context.Background(), "ws", account)
	if len(list.NeedsYou) == 0 || !list.NeedsYou[0].Tracked {
		t.Fatalf("the tracked mark was lost on a new message: %+v", list.NeedsYou)
	}

	if _, err := svc.Track(context.Background(), "ws", account, "t-news", func(State) (string, error) { return "", errors.New("store down") }); err == nil {
		t.Fatal("a failed capture was reported as tracked")
	}
}

func TestPeekNeverAsksTheModel(t *testing.T) {
	box := inbox()
	model := &scriptedModel{answer: "[]"}
	svc := NewService(box, &memoryStore{}, func() Completer { return model })
	list, err := svc.Peek(context.Background(), "ws", account)
	if err != nil || subjects(list.NeedsYou) != "Offsite" {
		t.Fatalf("Peek = %q, %v", subjects(list.NeedsYou), err)
	}
	if model.calls != 0 || box.queries[0].WithSnippets {
		t.Fatalf("Peek asked the model (%d calls) or read message text (%v)", model.calls, box.queries[0].WithSnippets)
	}
}

func TestListsAreKeptPerWorkspaceAndAccount(t *testing.T) {
	store := &memoryStore{}
	svc := NewService(inbox(), store, nil)
	if _, err := svc.List(context.Background(), "ws-a", account); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := svc.SetBucket(context.Background(), "ws-b", account, "t-sam", BucketIgnorable); !errors.Is(err, ErrUnknownThread) {
		t.Fatalf("another workspace's thread was changed: %v", err)
	}
}

func TestListSavesOnlyWhatChanged(t *testing.T) {
	store := &memoryStore{}
	svc := NewService(inbox(), store, nil)
	if _, err := svc.List(context.Background(), "ws", account); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := svc.List(context.Background(), "ws", account); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.saves != 1 {
		t.Fatalf("saves = %d; an unchanged inbox must not be written again", store.saves)
	}
}
