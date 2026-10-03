package emailtriage

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/mailbox"
)

// Scope is one mailbox in one workspace: the unit triage state is kept for.
type Scope struct {
	WorkspaceID string
	AccountID   string
}

// State is what is remembered about one thread between reads. A verdict holds
// until a newer message arrives; so does the user's own call on it.
type State struct {
	ThreadID      string
	LastMessageAt time.Time
	Bucket        Bucket
	Kind          Kind
	Rule          string
	Why           string
	Subject       string
	From          string
	// UserBucket is the user's override: "Not important" sets ignorable, "This
	// needs me" sets needs_you. Empty means the verdict stands.
	UserBucket Bucket
	// FollowUpID is the follow-up the user tracked this thread as. It outlives
	// a newer message: the follow-up still exists.
	FollowUpID string
	UpdatedAt  time.Time
}

// current reports whether the state was decided about the thread's newest
// message, so neither the verdict nor the user's call is stale.
func (s State) current(lastMessageAt time.Time) bool {
	return !s.LastMessageAt.IsZero() && s.LastMessageAt.Equal(lastMessageAt)
}

// Store keeps triage state per scope.
type Store interface {
	Load(ctx context.Context, scope Scope) (map[string]State, error)
	Save(ctx context.Context, scope Scope, states []State) error
}

// Item is one row of the list. It carries no message text beyond the subject
// and the one-line reason.
type Item struct {
	ThreadID      string    `json:"thread_id"`
	Subject       string    `json:"subject"`
	From          string    `json:"from"`
	FromAddress   string    `json:"from_address,omitempty"`
	Bucket        Bucket    `json:"bucket"`
	Kind          Kind      `json:"kind,omitempty"`
	Why           string    `json:"why"`
	LastMessageAt time.Time `json:"last_message_at"`
	Unread        bool      `json:"unread,omitempty"`
	// ByYou is true when the bucket is the user's own call, not Ori's.
	ByYou bool `json:"by_you,omitempty"`
	// Tracked is true once the user made the thread a follow-up.
	Tracked bool `json:"tracked,omitempty"`
}

// List is the inbox, sorted.
type List struct {
	NeedsYou  []Item    `json:"needs_you"`
	FYI       []Item    `json:"fyi"`
	Ignorable []Item    `json:"ignorable"`
	ReadAt    time.Time `json:"read_at"`
	// Explained is true when a model wrote at least one reason; otherwise every
	// reason is the rules' own.
	Explained bool `json:"explained"`
}

const (
	listLookbackDays = 7
	listMaxThreads   = 25
	modelTimeout     = 45 * time.Second
)

// ErrUnknownThread means an action named a thread the list does not hold.
var ErrUnknownThread = errors.New("emailtriage: that thread is not in the list")

// ErrBusy means Peek found a full read in progress. Peek's callers must stay
// fast, so they read the inbox themselves rather than wait for the model.
var ErrBusy = errors.New("emailtriage: a read is in progress")

// Service sorts inboxes.
type Service struct {
	provider mailbox.MailboxProvider
	store    Store
	model    func() Completer
	now      func() time.Time

	// mu serializes reads and actions, so a double-clicked action and a list
	// refresh cannot interleave their saves.
	mu sync.Mutex
}

// NewService builds the service. model returns the completer to use for a
// read, or nil when no model is available; it is asked each time because the
// user can configure one later.
func NewService(provider mailbox.MailboxProvider, store Store, model func() Completer) *Service {
	return &Service{provider: provider, store: store, model: model, now: time.Now}
}

func (s *Service) ready() error {
	if s == nil || s.provider == nil || s.store == nil {
		return errors.New("emailtriage: unavailable")
	}
	return nil
}

// List reads the recent inbox and sorts it. Threads whose newest message has
// not changed keep their saved verdict, so the list is stable and the model is
// asked only about what is new.
func (s *Service) List(ctx context.Context, workspaceID string, account mailbox.Account) (List, error) {
	return s.read(ctx, workspaceID, account, true)
}

// Peek sorts the inbox the same way without asking the model, for readers that
// must stay fast, such as the daily brief. Saved model verdicts still apply.
func (s *Service) Peek(ctx context.Context, workspaceID string, account mailbox.Account) (List, error) {
	return s.read(ctx, workspaceID, account, false)
}

func (s *Service) read(ctx context.Context, workspaceID string, account mailbox.Account, askModel bool) (List, error) {
	if err := s.ready(); err != nil {
		return List{}, err
	}
	if askModel {
		s.mu.Lock()
	} else if !s.mu.TryLock() {
		return List{}, ErrBusy
	}
	defer s.mu.Unlock()

	page, err := s.provider.SearchThreads(ctx, account, mailbox.Query{
		MaxResults: listMaxThreads, LookbackDays: listLookbackDays, WithSnippets: askModel,
	})
	if err != nil {
		return List{}, err
	}
	scope := Scope{WorkspaceID: workspaceID, AccountID: account.ID}
	saved, err := s.store.Load(ctx, scope)
	if err != nil {
		return List{}, err
	}

	now := s.now().UTC()
	states := make(map[string]State, len(page.Threads))
	var candidates []Candidate
	for _, thread := range page.Threads {
		prior, seen := saved[thread.ID]
		if seen && prior.current(thread.LastMessageAt) {
			states[thread.ID] = prior
			continue
		}
		verdict := Classify(thread, account.EmailAddress)
		state := State{
			ThreadID: thread.ID, LastMessageAt: thread.LastMessageAt,
			Bucket: verdict.Bucket, Kind: verdict.Kind, Rule: verdict.Rule, Why: ruleWhy(thread, verdict),
			Subject: thread.Subject, From: threadSender(thread), FollowUpID: prior.FollowUpID, UpdatedAt: now,
		}
		states[thread.ID] = state
		if verdict.AskModel {
			candidates = append(candidates, candidateFor(thread, verdict))
		}
	}

	if askModel {
		s.explain(ctx, candidates, states, now)
	}

	var changed []State
	for _, state := range states {
		if prior, ok := saved[state.ThreadID]; !ok || prior != state {
			changed = append(changed, state)
		}
	}
	if len(changed) > 0 {
		if err := s.store.Save(ctx, scope, changed); err != nil {
			return List{}, err
		}
	}
	return buildList(page.Threads, states, now), nil
}

// explain asks the model about the candidates and folds its answers into
// states. A missing or failing model leaves the rules' verdicts.
func (s *Service) explain(ctx context.Context, candidates []Candidate, states map[string]State, now time.Time) {
	if len(candidates) == 0 || s.model == nil {
		return
	}
	model := s.model()
	if model == nil {
		return
	}
	modelCtx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()
	answers, err := Explain(modelCtx, model, candidates)
	if err != nil {
		return
	}
	for id, answer := range answers {
		state := states[id]
		state.Rule, state.Why, state.Kind, state.UpdatedAt = RuleModel, answer.Why, answer.Kind, now
		switch {
		case answer.NeedsYou:
			state.Bucket = BucketNeedsYou
		case state.Bucket == BucketNeedsYou:
			state.Bucket = BucketFYI
		}
		states[id] = state
	}
}

// SetBucket records the user's call on a thread: "Not important" (ignorable)
// or "This needs me" (needs_you). It holds until a newer message arrives.
func (s *Service) SetBucket(ctx context.Context, workspaceID string, account mailbox.Account, threadID string, bucket Bucket) error {
	switch bucket {
	case BucketIgnorable, BucketNeedsYou, BucketFYI:
	default:
		return errors.New("emailtriage: a thread can be marked needs_you, fyi, or ignorable")
	}
	_, err := s.update(ctx, workspaceID, account, threadID, func(state *State) error {
		state.UserBucket = bucket
		return nil
	})
	return err
}

// Track makes the thread a follow-up through capture, once: a thread already
// tracked returns its state unchanged. capture receives the saved state and
// returns the new follow-up's ID.
func (s *Service) Track(ctx context.Context, workspaceID string, account mailbox.Account, threadID string, capture func(State) (string, error)) (State, error) {
	if capture == nil {
		return State{}, errors.New("emailtriage: follow-ups are unavailable")
	}
	return s.update(ctx, workspaceID, account, threadID, func(state *State) error {
		if state.FollowUpID != "" {
			return nil
		}
		id, err := capture(*state)
		if err != nil {
			return err
		}
		state.FollowUpID = id
		return nil
	})
}

func (s *Service) update(ctx context.Context, workspaceID string, account mailbox.Account, threadID string, change func(*State) error) (State, error) {
	if err := s.ready(); err != nil {
		return State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := Scope{WorkspaceID: workspaceID, AccountID: account.ID}
	saved, err := s.store.Load(ctx, scope)
	if err != nil {
		return State{}, err
	}
	state, ok := saved[strings.TrimSpace(threadID)]
	if !ok {
		return State{}, ErrUnknownThread
	}
	before := state
	if err := change(&state); err != nil {
		return State{}, err
	}
	if state == before {
		return state, nil
	}
	state.UpdatedAt = s.now().UTC()
	if err := s.store.Save(ctx, scope, []State{state}); err != nil {
		return State{}, err
	}
	return state, nil
}

// ruleWhy is the reason shown when no model explained a thread.
func ruleWhy(thread mailbox.Thread, verdict Verdict) string {
	who := "Someone"
	if n := len(thread.Messages); n > 0 {
		if name := firstName(thread.Messages[n-1].From); name != "" {
			who = name
		}
	}
	switch verdict.Rule {
	case RuleAddressedToYou:
		return who + " wrote to you and hasn't had a reply."
	case RuleCopiedOnly:
		return "You were copied; it was addressed to someone else."
	case RuleAutomated:
		return "An automatic message."
	case RuleBulk:
		return "Sent to a mailing list."
	case RuleYouWroteLast, RuleYouReplied:
		return "You replied."
	}
	return ""
}

func firstName(p mailbox.Participant) string {
	if name := strings.TrimSpace(p.Name); name != "" {
		if fields := strings.Fields(name); len(fields) > 0 {
			return strings.Trim(fields[0], `",'`)
		}
	}
	if at := strings.Index(p.Address, "@"); at > 0 {
		return p.Address[:at]
	}
	return ""
}

func threadSender(thread mailbox.Thread) string {
	if n := len(thread.Messages); n > 0 {
		return senderName(thread.Messages[n-1].From)
	}
	return ""
}

// buildList sorts the read threads into the three groups, newest first.
func buildList(threads []mailbox.Thread, states map[string]State, now time.Time) List {
	list := List{NeedsYou: []Item{}, FYI: []Item{}, Ignorable: []Item{}, ReadAt: now}
	for _, thread := range threads {
		state, ok := states[thread.ID]
		if !ok {
			continue
		}
		bucket, byYou := state.Bucket, false
		if state.UserBucket != "" {
			bucket, byYou = state.UserBucket, true
		}
		item := Item{
			ThreadID: thread.ID, Subject: thread.Subject, Bucket: bucket, Kind: state.Kind, Why: state.Why,
			LastMessageAt: thread.LastMessageAt, Unread: thread.Unread, ByYou: byYou, Tracked: state.FollowUpID != "",
		}
		if n := len(thread.Messages); n > 0 {
			last := thread.Messages[n-1]
			item.From, item.FromAddress = senderName(last.From), last.From.Address
		}
		switch bucket {
		case BucketNeedsYou:
			list.NeedsYou = append(list.NeedsYou, item)
		case BucketFYI:
			list.FYI = append(list.FYI, item)
		case BucketIgnorable:
			list.Ignorable = append(list.Ignorable, item)
		}
		if state.Rule == RuleModel && bucket != BucketHandled {
			list.Explained = true
		}
	}
	for _, group := range [][]Item{list.NeedsYou, list.FYI, list.Ignorable} {
		sort.SliceStable(group, func(i, j int) bool { return group[i].LastMessageAt.After(group[j].LastMessageAt) })
	}
	return list
}

func senderName(p mailbox.Participant) string {
	if name := strings.TrimSpace(p.Name); name != "" {
		return name
	}
	return p.Address
}
