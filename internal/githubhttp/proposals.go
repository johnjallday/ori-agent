package githubhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The confirm-gated write broker.
//
// Everything this template writes to GitHub goes through here, and there is
// deliberately no second route. An agent can create a proposal; only a
// user-confirmed proposal reaches GitHub. That is enforced structurally rather
// than by instruction: the tools that mutate issues are classified `external`
// (see toolpolicy.go), which every autonomy policy denies, so an agent cannot
// invoke them at all -- it can only describe what it would like to happen.
//
// Modeled on mailbox.Broker, whose send path solves the same problem for
// email: an atomic Draft->Executing claim under the lock, a payload hash that
// binds a confirmation to the exact text the user reviewed, single-use
// semantics, and expiry.
type (
	// ProposalStatus is where a proposal is in its lifecycle.
	ProposalStatus string
	// ProposalKind is the sort of change a proposal would make.
	ProposalKind string
)

const (
	ProposalDraft     ProposalStatus = "draft"
	ProposalExecuting ProposalStatus = "executing" // in-flight; rejects concurrent confirms
	ProposalApplied   ProposalStatus = "applied"   // terminal
	ProposalFailed    ProposalStatus = "failed"    // retryable
	ProposalRejected  ProposalStatus = "rejected"  // terminal; the user said no
	ProposalExpired   ProposalStatus = "expired"
)

const (
	// ProposalComment posts a comment on an issue.
	ProposalComment ProposalKind = "comment"
	// ProposalLabels adds and/or removes labels on an issue.
	ProposalLabels ProposalKind = "labels"
	// ProposalState closes or reopens an issue.
	ProposalState ProposalKind = "state"
)

// proposalTTL bounds how long a draft stays confirmable. A proposal describes
// a repository at a moment in time; approving one from days ago is unlikely to
// mean what the user thought it meant.
const proposalTTL = 24 * time.Hour

var (
	ErrProposalNotFound   = errors.New("github: proposal not found")
	ErrProposalNotDraft   = errors.New("github: proposal is not awaiting confirmation")
	ErrProposalExpired    = errors.New("github: proposal has expired")
	ErrProposalChanged    = errors.New("github: proposal changed since it was reviewed")
	ErrProposalRepo       = errors.New("github: proposal targets a different repository")
	ErrBrokerUnconfigured = errors.New("github: proposal broker is not configured")
)

// Change is exactly what a proposal would do. Every field is shown to the user
// verbatim before they approve it -- the PRD requires the literal comment text
// and the literal label, not a summary of them.
type Change struct {
	Kind ProposalKind `json:"kind"`
	// Repo is the "owner/name" this change targets. It is stored on the
	// proposal rather than looked up at execution time so a confirmation can
	// be checked against the workspace's binding as it stands *now*.
	Repo string `json:"repo"`
	// Issue is the issue number the change applies to.
	Issue int `json:"issue"`
	// Body is the literal comment text, for ProposalComment.
	Body string `json:"body,omitempty"`
	// AddLabels and RemoveLabels are the literal label names, for
	// ProposalLabels.
	AddLabels    []string `json:"add_labels,omitempty"`
	RemoveLabels []string `json:"remove_labels,omitempty"`
	// State is "closed" or "open", for ProposalState. StateReason is
	// GitHub's close reason ("completed", "not_planned", "duplicate").
	State       string `json:"state,omitempty"`
	StateReason string `json:"state_reason,omitempty"`
	// Rationale is the agent's one-line explanation, shown alongside the
	// change. It is never sent to GitHub.
	Rationale string `json:"rationale,omitempty"`
}

// Hash is the fingerprint a confirmation is bound to. It covers every field
// that changes what would reach GitHub -- and deliberately includes Rationale,
// because the rationale is part of what the user read when deciding.
func (c Change) Hash() string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			// The length prefix stops ("ab","c") and ("a","bc") hashing
			// alike, which would let an edit slip past the check.
			fmt.Fprintf(h, "%d:%s|", len(p), p)
		}
	}
	write(string(c.Kind), c.Repo, fmt.Sprint(c.Issue), c.Body, c.State, c.StateReason, c.Rationale)
	write(c.AddLabels...)
	write("--")
	write(c.RemoveLabels...)
	return hex.EncodeToString(h.Sum(nil))
}

// Describe renders the change as the one-line summary a list view shows. The
// full text is always available on the proposal itself; this never replaces
// showing it before approval.
func (c Change) Describe() string {
	switch c.Kind {
	case ProposalComment:
		return fmt.Sprintf("Comment on #%d", c.Issue)
	case ProposalLabels:
		var parts []string
		if len(c.AddLabels) > 0 {
			parts = append(parts, "add "+strings.Join(c.AddLabels, ", "))
		}
		if len(c.RemoveLabels) > 0 {
			parts = append(parts, "remove "+strings.Join(c.RemoveLabels, ", "))
		}
		return fmt.Sprintf("Labels on #%d: %s", c.Issue, strings.Join(parts, "; "))
	case ProposalState:
		if c.State == "closed" {
			return fmt.Sprintf("Close #%d", c.Issue)
		}
		return fmt.Sprintf("Reopen #%d", c.Issue)
	default:
		return fmt.Sprintf("Change to #%d", c.Issue)
	}
}

// Proposal is an inert description of a change. It reaches GitHub only via
// Confirm.
type Proposal struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Change      Change         `json:"change"`
	Status      ProposalStatus `json:"status"`
	Hash        string         `json:"hash"`
	Summary     string         `json:"summary"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	ExpiresAt   time.Time      `json:"expires_at"`
	// AppliedURL is the GitHub URL of the resulting comment or issue.
	AppliedURL string `json:"applied_url,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

func (p *Proposal) clone() *Proposal {
	cp := *p
	cp.Change.AddLabels = append([]string(nil), p.Change.AddLabels...)
	cp.Change.RemoveLabels = append([]string(nil), p.Change.RemoveLabels...)
	return &cp
}

// RepoResolver reports the repository a workspace is currently bound to. The
// broker consults it at confirm time rather than trusting the proposal, so a
// proposal created before the binding changed cannot execute against the new
// repository -- or against the old one.
type RepoResolver interface {
	BoundRepo(workspaceID string) (string, bool)
}

// Executor performs the confirmed change. Split out so the broker's rules can
// be tested without touching GitHub, and so there is exactly one
// implementation that talks to the API.
type Executor interface {
	Apply(ctx context.Context, change Change) (url string, err error)
}

// Broker owns every proposal and is the only path to a GitHub write.
type Broker struct {
	mu        sync.Mutex
	proposals map[string]*Proposal
	repos     RepoResolver
	executor  Executor
	now       func() time.Time
}

// NewBroker builds the broker.
func NewBroker(repos RepoResolver, executor Executor) *Broker {
	return &Broker{
		proposals: make(map[string]*Proposal),
		repos:     repos,
		executor:  executor,
		now:       time.Now,
	}
}

// Propose records an inert draft. It performs no GitHub call of any kind --
// that is the property the whole design rests on, and there is a test asserting
// it.
func (b *Broker) Propose(workspaceID string, change Change) (*Proposal, error) {
	if b == nil {
		return nil, ErrBrokerUnconfigured
	}
	if err := validateChange(change); err != nil {
		return nil, err
	}

	// A proposal may only ever target the workspace's own repository. The
	// agent supplies the repo, so it is checked here rather than trusted --
	// and checked again at confirm time, because the binding can change in
	// between.
	bound, ok := b.boundRepo(workspaceID)
	if !ok {
		return nil, fmt.Errorf("%w: this workspace has no repository bound", ErrProposalRepo)
	}
	if !strings.EqualFold(strings.TrimSpace(change.Repo), bound) {
		return nil, fmt.Errorf("%w: %q is not %s", ErrProposalRepo, change.Repo, bound)
	}
	change.Repo = bound

	now := b.now().UTC()
	proposal := &Proposal{
		ID:          uuid.NewString(),
		WorkspaceID: workspaceID,
		Change:      change,
		Status:      ProposalDraft,
		Hash:        change.Hash(),
		Summary:     change.Describe(),
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(proposalTTL),
	}

	b.mu.Lock()
	b.proposals[proposal.ID] = proposal
	out := proposal.clone()
	b.mu.Unlock()
	return out, nil
}

// Get returns one proposal.
func (b *Broker) Get(workspaceID, id string) (*Proposal, bool) {
	if b == nil {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.proposals[id]
	if !ok || p.WorkspaceID != workspaceID {
		return nil, false
	}
	return p.clone(), true
}

// List returns a workspace's proposals, pending ones first.
func (b *Broker) List(workspaceID string) []*Proposal {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var pending, done []*Proposal
	for _, p := range b.proposals {
		if p.WorkspaceID != workspaceID {
			continue
		}
		if p.Status == ProposalDraft || p.Status == ProposalFailed {
			pending = append(pending, p.clone())
			continue
		}
		done = append(done, p.clone())
	}
	return append(pending, done...)
}

// Reject marks a proposal declined. Nothing reaches GitHub, and nothing is
// left behind there -- a rejected proposal never existed as far as the
// repository is concerned.
func (b *Broker) Reject(workspaceID, id string) (*Proposal, error) {
	if b == nil {
		return nil, ErrBrokerUnconfigured
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.proposals[id]
	if !ok || p.WorkspaceID != workspaceID {
		return nil, ErrProposalNotFound
	}
	if p.Status == ProposalDraft || p.Status == ProposalFailed {
		p.Status = ProposalRejected
		p.UpdatedAt = b.now().UTC()
	}
	return p.clone(), nil
}

// Confirm is the single, atomic path from a proposal to a GitHub write.
//
// It rejects a proposal that is not awaiting confirmation, has expired, was
// edited since the user reviewed it, or targets a repository this workspace is
// no longer bound to. The Draft->Executing transition happens under the lock,
// which is what makes a double-confirm impossible rather than merely unlikely:
// the second caller finds the proposal already claimed.
func (b *Broker) Confirm(ctx context.Context, workspaceID, id, expectedHash string) (*Proposal, error) {
	if b == nil || b.executor == nil || b.repos == nil {
		return nil, ErrBrokerUnconfigured
	}

	b.mu.Lock()
	p, ok := b.proposals[id]
	if !ok || p.WorkspaceID != workspaceID {
		b.mu.Unlock()
		return nil, ErrProposalNotFound
	}
	if p.Status != ProposalDraft && p.Status != ProposalFailed {
		b.mu.Unlock()
		return nil, ErrProposalNotDraft
	}
	if !b.now().Before(p.ExpiresAt) {
		p.Status = ProposalExpired
		p.UpdatedAt = b.now().UTC()
		b.mu.Unlock()
		return nil, ErrProposalExpired
	}
	// Bind the confirmation to the exact text the user read. An empty hash
	// is rejected rather than waved through: this is the check that makes
	// "you approved this specific content" true, so skipping it would make
	// the guarantee optional.
	if strings.TrimSpace(expectedHash) == "" || expectedHash != p.Hash {
		b.mu.Unlock()
		return nil, ErrProposalChanged
	}
	// Claim it: concurrent callers now see ProposalExecuting and are refused.
	p.Status = ProposalExecuting
	p.UpdatedAt = b.now().UTC()
	claimed := p.clone()
	b.mu.Unlock()

	// Re-check the binding immediately before the side effect. A workspace
	// re-pointed at another repository between proposal and confirmation
	// must not execute against either one.
	bound, hasRepo := b.boundRepo(workspaceID)
	if !hasRepo || !strings.EqualFold(claimed.Change.Repo, bound) {
		b.finalize(id, ProposalFailed, "", "this workspace is no longer bound to that repository")
		return nil, ErrProposalRepo
	}

	url, err := b.executor.Apply(ctx, claimed.Change)
	if err != nil {
		return b.finalize(id, ProposalFailed, "", err.Error()), err
	}
	return b.finalize(id, ProposalApplied, url, ""), nil
}

// finalize moves a claimed proposal to its terminal state.
func (b *Broker) finalize(id string, status ProposalStatus, url, lastErr string) *Proposal {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.proposals[id]
	if !ok {
		return nil
	}
	p.Status = status
	p.AppliedURL = url
	p.LastError = lastErr
	p.UpdatedAt = b.now().UTC()
	return p.clone()
}

func (b *Broker) boundRepo(workspaceID string) (string, bool) {
	repo, ok := b.repos.BoundRepo(workspaceID)
	if !ok {
		return "", false
	}
	owner, name, valid := SplitRepo(repo)
	if !valid {
		return "", false
	}
	return owner + "/" + name, true
}

// validateChange rejects a proposal that could not be rendered honestly to the
// user or executed unambiguously.
func validateChange(c Change) error {
	if c.Issue <= 0 {
		return fmt.Errorf("github: a proposal must name an issue number")
	}
	switch c.Kind {
	case ProposalComment:
		if strings.TrimSpace(c.Body) == "" {
			return fmt.Errorf("github: a comment proposal must carry the comment text")
		}
	case ProposalLabels:
		if len(c.AddLabels) == 0 && len(c.RemoveLabels) == 0 {
			return fmt.Errorf("github: a label proposal must add or remove at least one label")
		}
	case ProposalState:
		if c.State != "closed" && c.State != "open" {
			return fmt.Errorf("github: a state proposal must set state to closed or open")
		}
	default:
		return fmt.Errorf("github: %q is not a kind of change this workspace can propose", c.Kind)
	}
	return nil
}
