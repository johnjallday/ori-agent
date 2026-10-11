package assistantdiscovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/publicsearch"
	"github.com/johnjallday/ori-agent/internal/skills"
)

const ReviewLifetime = 5 * time.Minute
const maxPendingReviews = 256

var ErrReviewRefused = errors.New("research review expired, changed, or unavailable")
var ErrLookupUnsupported = errors.New("research operation is not supported")

// Scope is derived by the host from fresh canonical ownership and accepted
// page/folder context, never decoded as browser/model authority. A current
// request must reproduce the entire scope to approve its exact pending lookup.
type Scope struct {
	Owner                string
	HQ                   string
	Profile              string
	StateVersion         int64
	Conversation         string
	ConversationRevision string
	ContextDigest        string
	Location             string
	LocationVersion      int64
	Subject              string
	SubjectVersion       int64
	FolderDigest         string
}

type Lookup struct {
	Operation     string `json:"operation"`
	Query         string `json:"query,omitempty"`
	URL           string `json:"url,omitempty"`
	SourceID      string `json:"source_id,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
}

// Review goes only to the user's review UI. The token/digest are never tool
// output, transcript data, a model instruction, or an importable approval.
type Review struct {
	Token          string    `json:"token"`
	Digest         string    `json:"digest"`
	Lookup         Lookup    `json:"lookup"`
	Destination    string    `json:"destination"`
	RedirectPolicy string    `json:"redirect_policy"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type ScopeValidator interface {
	ValidateResearchScope(context.Context, Scope) error
}

type pendingReview struct {
	scope   Scope
	lookup  Lookup
	digest  string
	expires time.Time
}

// ReviewGate holds bounded ephemeral, single-use offers, not execution grants
// in memory/transcripts. Restart discards them. Each conversation has one
// current review; editing/replacing it invalidates the older token immediately.
type ReviewGate struct {
	mu        sync.Mutex
	pending   map[[32]byte]pendingReview
	validator ScopeValidator
	now       func() time.Time
}

func NewReviewGate(validator ScopeValidator) *ReviewGate {
	return &ReviewGate{pending: map[[32]byte]pendingReview{}, validator: validator, now: time.Now}
}

func validateLookup(lookup Lookup) (destination, redirects string, err error) {
	if lookup.Operation != "mcp_catalog_refresh" && lookup.Operation != "web_search" && (lookup.SourceID != "" || lookup.SourceVersion != "") {
		return "", "", ErrReviewRefused
	}
	switch lookup.Operation {
	case "skills_catalog":
		if lookup.URL != "" || ValidatePublicQuery(lookup.Query) != nil {
			return "", "", ErrReviewRefused
		}
		if _, err := skills.MarketplaceSearchURL(lookup.Query); err != nil {
			return "", "", ErrReviewRefused
		}
		return "https://skills.sh/api/search", "none", nil
	case "mcp_catalog_refresh":
		if lookup.SourceID == "" || len(lookup.SourceID) > 128 || len(lookup.SourceVersion) != 64 || lookup.Query != "" && ValidatePublicQuery(lookup.Query) != nil {
			return "", "", ErrReviewRefused
		}
		if target, err := PublicURL(lookup.URL); err == nil {
			return target, "none", nil
		}
		return "", "", ErrReviewRefused
	case "web_search":
		if lookup.URL != "" || lookup.SourceID != publicsearch.PublicSearchInstance || lookup.SourceVersion != publicSearchVersion || ValidatePublicQuery(lookup.Query) != nil {
			return "", "", ErrReviewRefused
		}
		return publicsearch.PublicSearchDestination, "none", nil
	case "public_document":
		if lookup.Query != "" {
			return "", "", ErrReviewRefused
		}
		target, err := PublicURL(lookup.URL)
		if err != nil {
			return "", "", ErrReviewRefused
		}
		return target, "at most 3 server redirects within this exact origin; no cookies or credentials", nil
	default:
		return "", "", ErrLookupUnsupported
	}
}

// ValidateLookup validates proposal data only; it never approves or reads.
func ValidateLookup(lookup Lookup) error { _, _, err := validateLookup(lookup); return err }

func validScope(scope Scope) bool {
	return scope.Owner != "" && scope.HQ != "" && scope.Profile != "" && scope.Conversation != "" && scope.ConversationRevision != "" && scope.ContextDigest != "" && scope.StateVersion >= 0
}

func (g *ReviewGate) check(ctx context.Context, scope Scope) error {
	if g == nil || g.validator == nil || !validScope(scope) || ctx.Err() != nil {
		return ErrReviewRefused
	}
	if g.validator.ValidateResearchScope(ctx, scope) != nil || ctx.Err() != nil {
		return ErrReviewRefused
	}
	return nil
}

// Source-bound operations additionally require their actual owner to resolve
// the current enabled instance/configuration, not a model/display-name claim.
type LookupValidator interface {
	ValidateResearchLookup(context.Context, Lookup) error
}

func (g *ReviewGate) checkLookup(ctx context.Context, lookup Lookup) error {
	if lookup.Operation != "mcp_catalog_refresh" && lookup.Operation != "web_search" {
		return nil
	}
	validator, ok := g.validator.(LookupValidator)
	if !ok || validator.ValidateResearchLookup(ctx, lookup) != nil {
		return ErrReviewRefused
	}
	return nil
}

func (g *ReviewGate) Prepare(ctx context.Context, scope Scope, lookup Lookup) (Review, error) {
	destination, redirects, err := validateLookup(lookup)
	if err != nil {
		return Review{}, err
	}
	if err := g.check(ctx, scope); err != nil {
		return Review{}, err
	}
	if err := g.checkLookup(ctx, lookup); err != nil {
		return Review{}, err
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return Review{}, ErrReviewRefused
	}
	token := base64.RawURLEncoding.EncodeToString(entropy[:])
	now := g.now()
	expires := now.Add(ReviewLifetime)
	content, _ := json.Marshal(struct {
		Scope   Scope
		Lookup  Lookup
		Expires time.Time
	}{scope, lookup, expires})
	digest := contentHash(string(content))
	g.mu.Lock()
	defer g.mu.Unlock()
	for key, pending := range g.pending {
		if !now.Before(pending.expires) || pending.scope.Owner == scope.Owner && pending.scope.Conversation == scope.Conversation {
			delete(g.pending, key)
		}
	}
	if len(g.pending) >= maxPendingReviews {
		return Review{}, ErrReviewRefused
	}
	g.pending[sha256.Sum256([]byte(token))] = pendingReview{scope: scope, lookup: lookup, digest: digest, expires: expires}
	return Review{Token: token, Digest: digest, Lookup: lookup, Destination: destination, RedirectPolicy: redirects, ExpiresAt: expires.UTC()}, nil
}

// Approve is called only by the explicit user-review HTTP path, never by a
// model tool or fetched instructions. A query edit must Prepare a new exact
// review first. Failed/tampered approval does not authorize anything.
func (g *ReviewGate) Approve(ctx context.Context, scope Scope, review Review) (*Authorization, error) {
	if len(review.Token) != 43 || g.check(ctx, scope) != nil || g.checkLookup(ctx, review.Lookup) != nil {
		return nil, ErrReviewRefused
	}
	key := sha256.Sum256([]byte(review.Token))
	g.mu.Lock()
	defer g.mu.Unlock()
	pending, found := g.pending[key]
	destination, redirects, err := validateLookup(pending.lookup)
	if !found || err != nil || pending.scope != scope || pending.lookup != review.Lookup || pending.digest != review.Digest ||
		review.Destination != destination || review.RedirectPolicy != redirects || !review.ExpiresAt.Equal(pending.expires) || !g.now().Before(pending.expires) {
		return nil, ErrReviewRefused
	}
	delete(g.pending, key)
	return &Authorization{gate: g, scope: scope, lookup: pending.lookup, expires: pending.expires}, nil
}

func (g *ReviewGate) Cancel(ctx context.Context, scope Scope, token string) error {
	if len(token) != 43 || g.check(ctx, scope) != nil {
		return ErrReviewRefused
	}
	key := sha256.Sum256([]byte(token))
	g.mu.Lock()
	defer g.mu.Unlock()
	if pending, ok := g.pending[key]; !ok || pending.scope != scope {
		return ErrReviewRefused
	}
	delete(g.pending, key)
	return nil
}

// Authorization is opaque outside this package and itself single-use. Merely
// retaining/replaying a review or a previously claimed Authorization cannot
// perform another read. No exported method returns an approval token to tools.
type Authorization struct {
	mu      sync.Mutex
	used    bool
	gate    *ReviewGate
	scope   Scope
	lookup  Lookup
	expires time.Time
}

func (a *Authorization) claim(ctx context.Context) (Lookup, error) {
	if a == nil {
		return Lookup{}, ErrReviewRefused
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.used || a.gate == nil {
		return Lookup{}, ErrReviewRefused
	}
	a.used = true
	if !a.gate.now().Before(a.expires) || a.gate.check(ctx, a.scope) != nil || a.gate.checkLookup(ctx, a.lookup) != nil {
		return Lookup{}, ErrReviewRefused
	}
	return a.lookup, nil
}

func (a *Authorization) revalidate(ctx context.Context) error {
	if a == nil || a.gate == nil {
		return ErrReviewRefused
	}
	return a.gate.check(ctx, a.scope)
}
