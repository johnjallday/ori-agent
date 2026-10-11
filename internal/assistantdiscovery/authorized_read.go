package assistantdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/publicread"
	"github.com/johnjallday/ori-agent/internal/publictext"
)

var ErrResearchBudget = errors.New("research budget exhausted")

// Inspect past the excerpt edge so a credential cut at that edge is withheld,
// not made invisible to the guard by truncation. The suffix is never delivered.
const excerptSecretLookahead = 4096

// TurnBudget is constructed by the turn owner once, shared by all rounds/calls.
// sharedEvidence charges the existing workspace evidence ledger too, so research
// cannot create another independent allowance. Zero/forged budgets fail closed.
type TurnBudget struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	operations     int
	runes          int
	sharedEvidence func(int) bool
}

func NewTurnBudget(parent context.Context, sharedEvidence func(int) bool) *TurnBudget {
	ctx, cancel := context.WithTimeout(parent, TurnTimeout)
	return &TurnBudget{ctx: ctx, cancel: cancel, sharedEvidence: sharedEvidence}
}

// Context shares the aggregate deadline with model/tool orchestration.
func (b *TurnBudget) Context() context.Context {
	if b == nil || b.ctx == nil {
		return nil
	}
	return b.ctx
}

func (b *TurnBudget) Close() {
	if b != nil && b.cancel != nil {
		b.cancel()
	}
}

func (b *TurnBudget) reserve() error {
	if b == nil || b.ctx == nil || b.sharedEvidence == nil {
		return ErrResearchBudget
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctx.Err() != nil || b.operations >= MaxExternalOperations {
		return ErrResearchBudget
	}
	b.operations++
	return nil
}

// ChargeLocal uses the same research and turn ledger allowance for metadata.
func (b *TurnBudget) ChargeLocal(result Result) bool { return b != nil && b.charge(result) }

func (b *TurnBudget) charge(result Result) bool {
	if b == nil || b.ctx == nil || b.sharedEvidence == nil {
		return false
	}
	body, err := json.Marshal(result)
	if err != nil {
		return false
	}
	size := utf8.RuneCount(body)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctx.Err() != nil || size > MaxResearchRunes-b.runes || !b.sharedEvidence(size) {
		return false
	}
	b.runes += size
	return true
}

// ReadAuthorized has no unapproved query/URL input. Only the exact opaque
// authorization created by a successful user-review approval is usable. It
// rechecks scope before the read and after completion, never performs inferred
// follow-ups, and returns no source evidence when the lease/budget is revoked.
func (s *Service) ReadAuthorized(ctx context.Context, approval *Authorization, budget *TurnBudget) (Result, error) {
	if ctx.Err() != nil {
		return Result{}, ErrReviewRefused
	}
	if err := budget.reserve(); err != nil {
		return Result{}, err
	}
	deadline, _ := budget.ctx.Deadline()
	callCtx, cancel := context.WithDeadline(ctx, deadline)
	stop := context.AfterFunc(budget.ctx, cancel)
	defer func() { stop(); cancel() }()
	lookup, err := approval.claim(callCtx)
	if err != nil {
		return Result{}, err
	}
	var result Result
	switch lookup.Operation {
	case "skills_catalog":
		if s == nil || s.searchApproved == nil {
			result = Result{Availability: Unavailable, Reason: "catalog_unavailable", Candidates: []Candidate{}, Scope: "skills.sh catalog listing only"}
		} else {
			result = skillsResult(s.searchApproved(callCtx, lookup.Query, MaxCandidates))
		}
	case "web_search":
		result = s.readPublicSearch(callCtx, lookup)
	case "public_document":
		result = s.readDocument(callCtx, lookup.URL)
	case "mcp_catalog_refresh":
		result = s.refreshRegistry(callCtx, lookup, approval)
	default:
		return Result{}, ErrLookupUnsupported
	}
	if ctx.Err() != nil || approval.revalidate(callCtx) != nil {
		return Result{}, ErrReviewRefused
	}
	probe := result
	probe.Candidates = append([]Candidate(nil), result.Candidates...)
	for i := range probe.Candidates {
		probe.Candidates[i].Receipt.Key = "S999999999"
	}
	if !budget.charge(probe) {
		return Result{}, ErrResearchBudget
	}
	return result, nil
}

func (s *Service) readDocument(ctx context.Context, target string) Result {
	result := Result{Availability: Unavailable, Candidates: []Candidate{}, Scope: "bounded public-document excerpt only"}
	if s == nil || s.documents == nil {
		result.Reason = "document_reader_unavailable"
		return result
	}
	response, err := s.documents.Read(ctx, publicread.Request{URL: target, ContentTypes: []string{"text/html", "application/xhtml+xml", "text/plain", "text/markdown", "text/x-markdown"}, SameOriginRedirects: true})
	if err != nil || len(response.Body) > MaxResponseBytes || response.ReadAt.IsZero() {
		result.Reason = "document_unavailable"
		return result
	}
	if !utf8.Valid(response.Body) {
		result.Reason = "non_text_document"
		return result
	}
	for _, ch := range string(response.Body) {
		if unicode.IsControl(ch) && ch != '\n' && ch != '\r' && ch != '\t' {
			result.Reason = "non_text_document"
			return result
		}
	}
	finalURL, err := PublicURL(response.URL)
	if err != nil {
		result.Reason = "invalid_document_destination"
		return result
	}
	original, _ := url.Parse(target)
	final, _ := url.Parse(finalURL)
	if final.Scheme != original.Scheme || !strings.EqualFold(final.Host, original.Host) {
		result.Reason = "invalid_document_destination"
		return result
	}
	title, excerpt, truncated := publictext.Extract(response.Body, response.ContentType, MaxExcerptRunes+excerptSecretLookahead)
	if secretLike(excerpt) {
		result.Reason = "source_content_withheld"
		return result
	}
	excerpt, shortened := referenceText(excerpt, MaxExcerptRunes)
	if strings.TrimSpace(excerpt) == "" {
		result.Reason = "no_readable_text"
		return result
	}
	name, shortTitle := referenceText(title, 120)
	if name == "" {
		name = "Public document"
	}
	candidate := Candidate{ID: identity("public_document", finalURL), Kind: "public_document", Name: name, URL: finalURL, Readiness: UnknownReadiness()}
	revision, _ := referenceText(response.ETag, 128)
	candidate.Receipt = Receipt{SourceID: identity(candidate.Kind, finalURL), CandidateID: candidate.ID, Kind: candidate.Kind, Level: "document", URL: finalURL,
		ReadAt: response.ReadAt, ObservedAt: response.ReadAt, Revision: revision, ContentHash: response.Hash, Excerpt: excerpt,
		Truncated: truncated || shortened || shortTitle, Availability: Available, Freshness: "current_observation"}
	result.Availability, result.Candidates = Available, []Candidate{candidate}
	return result
}
