package githubhttp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fakes -------------------------------------------------------------------

type fakeRepoResolver struct {
	repo string
}

func (f *fakeRepoResolver) BoundRepo(string) (string, bool) {
	if f.repo == "" {
		return "", false
	}
	return f.repo, true
}

// recordingExecutor counts every GitHub write the broker attempts. The count
// being zero is the assertion that matters most in this file.
type recordingExecutor struct {
	mu      sync.Mutex
	applied []Change
	err     error
	delay   time.Duration
}

func (e *recordingExecutor) Apply(_ context.Context, change Change) (string, error) {
	if e.delay > 0 {
		time.Sleep(e.delay)
	}
	e.mu.Lock()
	e.applied = append(e.applied, change)
	e.mu.Unlock()
	if e.err != nil {
		return "", e.err
	}
	return "https://github.com/octocat/demo/issues/1#issuecomment-1", nil
}

func (e *recordingExecutor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.applied)
}

func newBroker(t *testing.T) (*Broker, *recordingExecutor) {
	t.Helper()
	exec := &recordingExecutor{}
	return NewBroker(&fakeRepoResolver{repo: "octocat/demo"}, exec), exec
}

func commentChange() Change {
	return Change{
		Kind:      ProposalComment,
		Repo:      "octocat/demo",
		Issue:     7,
		Body:      "Looks like a duplicate of #1.",
		Rationale: "Same stack trace.",
	}
}

// --- the core promise --------------------------------------------------------

// Proposing must never touch GitHub. This is the property the entire template
// rests on: an agent can describe a change, and describing it changes nothing.
func TestPropose_NeverCallsGitHub(t *testing.T) {
	broker, exec := newBroker(t)

	for _, change := range []Change{
		commentChange(),
		{Kind: ProposalLabels, Repo: "octocat/demo", Issue: 2, AddLabels: []string{"bug"}},
		{Kind: ProposalState, Repo: "octocat/demo", Issue: 3, State: "closed", StateReason: "duplicate"},
	} {
		if _, err := broker.Propose("ws-1", change); err != nil {
			t.Fatalf("Propose(%s) error: %v", change.Kind, err)
		}
	}

	if exec.count() != 0 {
		t.Fatalf("proposing performed %d GitHub write(s); it must perform none", exec.count())
	}
	if len(broker.List("ws-1")) != 3 {
		t.Fatalf("expected 3 pending proposals, got %d", len(broker.List("ws-1")))
	}
}

func TestConfirm_AppliesTheChangeExactlyOnce(t *testing.T) {
	broker, exec := newBroker(t)
	p, err := broker.Propose("ws-1", commentChange())
	if err != nil {
		t.Fatalf("Propose error: %v", err)
	}

	applied, err := broker.Confirm(context.Background(), "ws-1", p.ID, p.Hash)
	if err != nil {
		t.Fatalf("Confirm error: %v", err)
	}
	if applied.Status != ProposalApplied {
		t.Fatalf("status = %q, want applied", applied.Status)
	}
	if applied.AppliedURL == "" {
		t.Fatal("an applied proposal should carry the resulting URL")
	}
	if exec.count() != 1 {
		t.Fatalf("expected exactly 1 write, got %d", exec.count())
	}
	// What reached GitHub must be exactly what was reviewed.
	if exec.applied[0].Body != commentChange().Body {
		t.Fatalf("posted body = %q, want the reviewed text", exec.applied[0].Body)
	}
}

// A second confirmation must not post a second comment.
func TestConfirm_DoubleConfirmDoesNotDoublePost(t *testing.T) {
	broker, exec := newBroker(t)
	p, _ := broker.Propose("ws-1", commentChange())

	if _, err := broker.Confirm(context.Background(), "ws-1", p.ID, p.Hash); err != nil {
		t.Fatalf("first Confirm error: %v", err)
	}
	_, err := broker.Confirm(context.Background(), "ws-1", p.ID, p.Hash)
	if !errors.Is(err, ErrProposalNotDraft) {
		t.Fatalf("expected ErrProposalNotDraft on re-confirm, got %v", err)
	}
	if exec.count() != 1 {
		t.Fatalf("expected 1 write after a double confirm, got %d", exec.count())
	}
}

// Concurrent confirmations race for the same proposal; exactly one may win.
// The Draft->Executing claim under the lock is what makes this true rather
// than merely unlikely.
func TestConfirm_ConcurrentConfirmsPostOnce(t *testing.T) {
	exec := &recordingExecutor{delay: 20 * time.Millisecond}
	broker := NewBroker(&fakeRepoResolver{repo: "octocat/demo"}, exec)
	p, _ := broker.Propose("ws-1", commentChange())

	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	wg.Add(racers)
	for i := range racers {
		go func() {
			defer wg.Done()
			_, errs[i] = broker.Confirm(context.Background(), "ws-1", p.ID, p.Hash)
		}()
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected exactly 1 successful confirm, got %d", succeeded)
	}
	if exec.count() != 1 {
		t.Fatalf("expected exactly 1 GitHub write, got %d", exec.count())
	}
}

// --- staleness ---------------------------------------------------------------

// A confirmation carries the hash of what the user read. If the proposal is
// not that any more, the confirmation is not valid for it.
func TestConfirm_RejectsStaleHash(t *testing.T) {
	broker, exec := newBroker(t)
	p, _ := broker.Propose("ws-1", commentChange())

	_, err := broker.Confirm(context.Background(), "ws-1", p.ID, "a-hash-from-some-earlier-version")
	if !errors.Is(err, ErrProposalChanged) {
		t.Fatalf("expected ErrProposalChanged, got %v", err)
	}
	if exec.count() != 0 {
		t.Fatal("a stale confirmation must not reach GitHub")
	}
	// The proposal stays confirmable, so the user can re-review and approve.
	got, _ := broker.Get("ws-1", p.ID)
	if got.Status != ProposalDraft {
		t.Fatalf("status = %q, want the proposal still awaiting confirmation", got.Status)
	}
}

// An absent hash must be refused rather than waved through -- otherwise the
// "you approved this exact content" guarantee would be optional.
func TestConfirm_RejectsEmptyHash(t *testing.T) {
	broker, exec := newBroker(t)
	p, _ := broker.Propose("ws-1", commentChange())

	if _, err := broker.Confirm(context.Background(), "ws-1", p.ID, ""); !errors.Is(err, ErrProposalChanged) {
		t.Fatalf("expected an empty hash to be refused, got %v", err)
	}
	if exec.count() != 0 {
		t.Fatal("an unbound confirmation must not reach GitHub")
	}
}

// Every field the user reads must be covered by the hash, including the
// rationale they weighed when deciding.
func TestChangeHash_CoversEveryReviewedField(t *testing.T) {
	base := commentChange()
	baseHash := base.Hash()

	mutations := map[string]func(*Change){
		"body":         func(c *Change) { c.Body = "Different text entirely." },
		"issue":        func(c *Change) { c.Issue = 8 },
		"repo":         func(c *Change) { c.Repo = "octocat/other" },
		"kind":         func(c *Change) { c.Kind = ProposalLabels },
		"rationale":    func(c *Change) { c.Rationale = "A different reason." },
		"state":        func(c *Change) { c.State = "closed" },
		"state reason": func(c *Change) { c.StateReason = "not_planned" },
		"add labels":   func(c *Change) { c.AddLabels = []string{"bug"} },
		"remove label": func(c *Change) { c.RemoveLabels = []string{"wontfix"} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if changed.Hash() == baseHash {
				t.Fatalf("changing the %s did not change the hash; an edit could slip past confirmation", name)
			}
		})
	}
}

// Label lists must not hash ambiguously: adding "a","b" is not the same
// proposal as adding "ab", nor as removing them.
func TestChangeHash_DistinguishesLabelBoundaries(t *testing.T) {
	split := Change{Kind: ProposalLabels, Repo: "octocat/demo", Issue: 1, AddLabels: []string{"a", "b"}}
	joined := Change{Kind: ProposalLabels, Repo: "octocat/demo", Issue: 1, AddLabels: []string{"ab"}}
	if split.Hash() == joined.Hash() {
		t.Fatal(`["a","b"] and ["ab"] must not hash alike`)
	}

	added := Change{Kind: ProposalLabels, Repo: "octocat/demo", Issue: 1, AddLabels: []string{"bug"}}
	removed := Change{Kind: ProposalLabels, Repo: "octocat/demo", Issue: 1, RemoveLabels: []string{"bug"}}
	if added.Hash() == removed.Hash() {
		t.Fatal("adding and removing the same label must not hash alike")
	}
}

// --- repository scoping ------------------------------------------------------

// The agent supplies the repo, so it is checked rather than trusted.
func TestPropose_RefusesAnotherRepository(t *testing.T) {
	broker, exec := newBroker(t)

	change := commentChange()
	change.Repo = "someone/private"
	_, err := broker.Propose("ws-1", change)
	if !errors.Is(err, ErrProposalRepo) {
		t.Fatalf("expected ErrProposalRepo, got %v", err)
	}
	if exec.count() != 0 {
		t.Fatal("a refused proposal must not reach GitHub")
	}
	if len(broker.List("ws-1")) != 0 {
		t.Fatal("a refused proposal must not be recorded")
	}
}

func TestPropose_RefusesWhenNoRepositoryIsBound(t *testing.T) {
	broker := NewBroker(&fakeRepoResolver{}, &recordingExecutor{})
	if _, err := broker.Propose("ws-1", commentChange()); !errors.Is(err, ErrProposalRepo) {
		t.Fatalf("expected ErrProposalRepo with no binding, got %v", err)
	}
}

// A workspace re-pointed between proposal and confirmation must not execute
// against the new repository, nor quietly against the old one.
func TestConfirm_RefusesWhenTheBindingChanged(t *testing.T) {
	resolver := &fakeRepoResolver{repo: "octocat/demo"}
	exec := &recordingExecutor{}
	broker := NewBroker(resolver, exec)
	p, _ := broker.Propose("ws-1", commentChange())

	resolver.repo = "octocat/somewhere-else"

	_, err := broker.Confirm(context.Background(), "ws-1", p.ID, p.Hash)
	if !errors.Is(err, ErrProposalRepo) {
		t.Fatalf("expected ErrProposalRepo, got %v", err)
	}
	if exec.count() != 0 {
		t.Fatal("nothing may reach GitHub after the binding moved")
	}
}

// A proposal belongs to its workspace; another workspace cannot confirm it.
func TestConfirm_RefusesAcrossWorkspaces(t *testing.T) {
	broker, exec := newBroker(t)
	p, _ := broker.Propose("ws-1", commentChange())

	if _, err := broker.Confirm(context.Background(), "ws-2", p.ID, p.Hash); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("expected ErrProposalNotFound across workspaces, got %v", err)
	}
	if exec.count() != 0 {
		t.Fatal("a cross-workspace confirmation must not reach GitHub")
	}
	if _, ok := broker.Get("ws-2", p.ID); ok {
		t.Fatal("Get must not leak another workspace's proposal")
	}
}

// --- rejection and lifecycle -------------------------------------------------

// Rejecting must leave no trace on GitHub -- as far as the repository is
// concerned, the proposal never existed.
func TestReject_LeavesNothingOnGitHub(t *testing.T) {
	broker, exec := newBroker(t)
	p, _ := broker.Propose("ws-1", commentChange())

	rejected, err := broker.Reject("ws-1", p.ID)
	if err != nil {
		t.Fatalf("Reject error: %v", err)
	}
	if rejected.Status != ProposalRejected {
		t.Fatalf("status = %q, want rejected", rejected.Status)
	}
	if exec.count() != 0 {
		t.Fatal("rejecting must not reach GitHub")
	}
	// And a rejected proposal cannot then be confirmed.
	if _, err := broker.Confirm(context.Background(), "ws-1", p.ID, p.Hash); !errors.Is(err, ErrProposalNotDraft) {
		t.Fatalf("expected a rejected proposal to be unconfirmable, got %v", err)
	}
	if exec.count() != 0 {
		t.Fatal("confirming after rejection must not reach GitHub")
	}
}

func TestConfirm_RejectsExpiredProposal(t *testing.T) {
	exec := &recordingExecutor{}
	broker := NewBroker(&fakeRepoResolver{repo: "octocat/demo"}, exec)
	p, _ := broker.Propose("ws-1", commentChange())

	// Jump past the TTL.
	broker.now = func() time.Time { return time.Now().Add(proposalTTL + time.Hour) }

	if _, err := broker.Confirm(context.Background(), "ws-1", p.ID, p.Hash); !errors.Is(err, ErrProposalExpired) {
		t.Fatalf("expected ErrProposalExpired, got %v", err)
	}
	if exec.count() != 0 {
		t.Fatal("an expired proposal must not reach GitHub")
	}
	got, _ := broker.Get("ws-1", p.ID)
	if got.Status != ProposalExpired {
		t.Fatalf("status = %q, want expired", got.Status)
	}
}

// A failed write is retryable: the proposal returns to a confirmable state
// rather than being lost, and the error is recorded.
func TestConfirm_FailedWriteIsRetryable(t *testing.T) {
	exec := &recordingExecutor{err: fmt.Errorf("github says no")}
	broker := NewBroker(&fakeRepoResolver{repo: "octocat/demo"}, exec)
	p, _ := broker.Propose("ws-1", commentChange())

	if _, err := broker.Confirm(context.Background(), "ws-1", p.ID, p.Hash); err == nil {
		t.Fatal("expected the write failure to surface")
	}
	got, _ := broker.Get("ws-1", p.ID)
	if got.Status != ProposalFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.LastError == "" {
		t.Fatal("a failed proposal should record why")
	}

	// Retry succeeds and posts exactly once more.
	exec.err = nil
	if _, err := broker.Confirm(context.Background(), "ws-1", p.ID, p.Hash); err != nil {
		t.Fatalf("retry error: %v", err)
	}
	if exec.count() != 2 {
		t.Fatalf("expected 2 attempts (1 failed, 1 successful), got %d", exec.count())
	}
}

// --- validation --------------------------------------------------------------

func TestPropose_RejectsUnrenderableChanges(t *testing.T) {
	broker, _ := newBroker(t)
	cases := map[string]Change{
		"no issue number": {Kind: ProposalComment, Repo: "octocat/demo", Body: "hi"},
		"empty comment":   {Kind: ProposalComment, Repo: "octocat/demo", Issue: 1, Body: "   "},
		"no labels":       {Kind: ProposalLabels, Repo: "octocat/demo", Issue: 1},
		"bad state":       {Kind: ProposalState, Repo: "octocat/demo", Issue: 1, State: "sideways"},
		"unknown kind":    {Kind: "merge_pull_request", Repo: "octocat/demo", Issue: 1},
		"negative issue":  {Kind: ProposalComment, Repo: "octocat/demo", Issue: -1, Body: "hi"},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := broker.Propose("ws-1", change); err == nil {
				t.Fatal("expected the proposal to be rejected")
			}
		})
	}
}

func TestDescribe_SummarizesEachKind(t *testing.T) {
	cases := []struct {
		change Change
		want   string
	}{
		{commentChange(), "Comment on #7"},
		{Change{Kind: ProposalLabels, Issue: 2, AddLabels: []string{"bug"}}, "Labels on #2: add bug"},
		{Change{Kind: ProposalLabels, Issue: 2, RemoveLabels: []string{"wontfix"}}, "Labels on #2: remove wontfix"},
		{Change{Kind: ProposalState, Issue: 3, State: "closed"}, "Close #3"},
		{Change{Kind: ProposalState, Issue: 3, State: "open"}, "Reopen #3"},
	}
	for _, tc := range cases {
		if got := tc.change.Describe(); got != tc.want {
			t.Errorf("Describe() = %q, want %q", got, tc.want)
		}
	}
}

// The summary is for a list view; it must never be the only thing a user sees,
// so the proposal always carries the literal content too.
func TestProposal_CarriesLiteralContentNotJustASummary(t *testing.T) {
	broker, _ := newBroker(t)
	p, _ := broker.Propose("ws-1", commentChange())

	if !strings.Contains(p.Change.Body, "duplicate of #1") {
		t.Fatalf("the proposal must carry the literal comment text, got %q", p.Change.Body)
	}
	if p.Summary == "" || p.Hash == "" {
		t.Fatal("a proposal needs both a summary and a hash to be reviewable")
	}
}
