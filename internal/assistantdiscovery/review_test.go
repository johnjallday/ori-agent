package assistantdiscovery

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

type scopeAuthority struct {
	mu      sync.Mutex
	current Scope
	revoked bool
}

func (a *scopeAuthority) ValidateResearchScope(_ context.Context, scope Scope) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.revoked || scope != a.current {
		return errors.New("private owner diagnostics")
	}
	return nil
}
func (a *scopeAuthority) revoke() { a.mu.Lock(); a.revoked = true; a.mu.Unlock() }

func reviewFixture() (Scope, *scopeAuthority, *ReviewGate) {
	scope := Scope{Owner: "local", HQ: "hq", Profile: "assistant", StateVersion: 2, Conversation: "canonical-thread", ConversationRevision: "original-message-range", ContextDigest: "accepted-app-context"}
	authority := &scopeAuthority{current: scope}
	return scope, authority, NewReviewGate(authority)
}

func TestResearchReviewRequiresCanonicalValidatorAndExactMinimalSupportedLookup(t *testing.T) {
	scope, _, gate := reviewFixture()
	for _, lookup := range []Lookup{
		{Operation: "shell", Query: "run something"}, {Operation: "web_search", Query: "telegram"},
		{Operation: "skills_catalog", Query: "api_key=secret"}, {Operation: "skills_catalog", Query: "telegram --install"},
		{Operation: "skills_catalog", Query: "telegram", URL: "https://example.com/extra"},
		{Operation: "public_document", URL: "http://127.0.0.1/private"}, {Operation: "public_document", URL: "https://user:password@example.com/docs"},
	} {
		if _, err := gate.Prepare(context.Background(), scope, lookup); err == nil {
			t.Fatalf("unsupported/private lookup minted review: %+v", lookup)
		}
	}
	if len(gate.pending) != 0 {
		t.Fatal("invalid lookup created pending permission")
	}
	if _, err := NewReviewGate(nil).Prepare(context.Background(), scope, Lookup{Operation: "skills_catalog", Query: "telegram"}); err == nil {
		t.Fatal("missing validator failed open")
	}
	missing := scope
	missing.Conversation = ""
	if _, err := gate.Prepare(context.Background(), missing, Lookup{Operation: "skills_catalog", Query: "telegram"}); err == nil {
		t.Fatal("new/unpersisted conversation minted permission")
	}
	review, err := gate.Prepare(context.Background(), scope, Lookup{Operation: "skills_catalog", Query: "  Telegram communities  "})
	if err != nil || review.Lookup.Query != "  Telegram communities  " || review.Destination != "https://skills.sh/api/search" || len(review.Token) != 43 || len(review.Digest) != 64 || review.RedirectPolicy != "none" {
		t.Fatalf("review: %+v %v", review, err)
	}
}

func TestResearchReviewRefusesTamperingForeignScopeReplayAndChangedContext(t *testing.T) {
	scope, _, gate := reviewFixture()
	lookup := Lookup{Operation: "public_document", URL: "https://example.com/docs"}
	review, err := gate.Prepare(context.Background(), scope, lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Review){
		func(r *Review) { r.Lookup.URL = "https://example.com/other" }, func(r *Review) { r.Lookup.Operation = "skills_catalog" },
		func(r *Review) { r.Digest = "forged" }, func(r *Review) { r.Token = "forged" },
		func(r *Review) { r.Destination = "https://evil.example.com/docs" }, func(r *Review) { r.RedirectPolicy = "any" },
		func(r *Review) { r.ExpiresAt = r.ExpiresAt.Add(time.Hour) },
	} {
		changed := review
		mutate(&changed)
		if _, err := gate.Approve(context.Background(), scope, changed); err == nil {
			t.Fatalf("changed review approved: %+v", changed)
		}
	}
	for _, mutate := range []func(*Scope){
		func(s *Scope) { s.Owner = "foreign" }, func(s *Scope) { s.HQ = "replaced" }, func(s *Scope) { s.Profile = "replaced" },
		func(s *Scope) { s.StateVersion++ }, func(s *Scope) { s.Conversation = "foreign" }, func(s *Scope) { s.ContextDigest = "different page" },
		func(s *Scope) { s.Subject = "other workspace" }, func(s *Scope) { s.SubjectVersion++ }, func(s *Scope) { s.FolderDigest = "changed focus" },
	} {
		changed := scope
		mutate(&changed)
		if _, err := gate.Approve(context.Background(), changed, review); err == nil {
			t.Fatalf("foreign/changed scope approved: %+v", changed)
		}
	}
	authorization, err := gate.Approve(context.Background(), scope, review)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Approve(context.Background(), scope, review); err == nil {
		t.Fatal("token replay approved")
	}
	if got, err := authorization.claim(context.Background()); err != nil || got != lookup {
		t.Fatalf("claim: %+v %v", got, err)
	}
	if _, err := authorization.claim(context.Background()); err == nil {
		t.Fatal("authorization object replay approved")
	}
	if _, err := (&Authorization{}).claim(context.Background()); err == nil {
		t.Fatal("forged authorization approved")
	}
}

func TestResearchReviewEditExpiryCancellationAndRevocationFailClosed(t *testing.T) {
	scope, authority, gate := reviewFixture()
	now := time.Now()
	gate.now = func() time.Time { return now }
	old, err := gate.Prepare(context.Background(), scope, Lookup{Operation: "skills_catalog", Query: "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := gate.Prepare(context.Background(), scope, Lookup{Operation: "skills_catalog", Query: "community alternatives"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Approve(context.Background(), scope, old); err == nil {
		t.Fatal("superseded query replay approved")
	}
	now = now.Add(ReviewLifetime)
	if _, err := gate.Approve(context.Background(), scope, updated); err == nil {
		t.Fatal("expired lookup approved")
	}
	current, err := gate.Prepare(context.Background(), scope, old.Lookup)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Cancel(context.Background(), scope, current.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Approve(context.Background(), scope, current); err == nil {
		t.Fatal("cancelled lookup approved")
	}
	current, err = gate.Prepare(context.Background(), scope, old.Lookup)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := gate.Approve(context.Background(), scope, current)
	if err != nil {
		t.Fatal(err)
	}
	authority.revoke()
	if _, err := authorization.claim(context.Background()); err == nil {
		t.Fatal("revoked approval executed")
	}
	if _, err := gate.Prepare(context.Background(), scope, old.Lookup); err == nil {
		t.Fatal("revoked owner minted a lookup")
	}
}

func TestResearchReviewPendingStorageIsBoundedAndExpiresWithoutRestoration(t *testing.T) {
	scope, authority, gate := reviewFixture()
	now := time.Now()
	gate.now = func() time.Time { return now }
	for i := 0; i < maxPendingReviews; i++ {
		scope.Conversation = "thread-" + strconv.Itoa(i)
		authority.current = scope
		if _, err := gate.Prepare(context.Background(), scope, Lookup{Operation: "skills_catalog", Query: "telegram"}); err != nil {
			t.Fatal(err)
		}
	}
	scope.Conversation = "one-too-many"
	authority.current = scope
	if _, err := gate.Prepare(context.Background(), scope, Lookup{Operation: "skills_catalog", Query: "telegram"}); err == nil || len(gate.pending) != maxPendingReviews {
		t.Fatal("unbounded pending review cache")
	}
	now = now.Add(ReviewLifetime)
	review, err := gate.Prepare(context.Background(), scope, Lookup{Operation: "skills_catalog", Query: "telegram"})
	if err != nil || len(gate.pending) != 1 {
		t.Fatal("expired pending records were not discarded", err)
	}
	if _, err := NewReviewGate(authority).Approve(context.Background(), scope, review); err == nil {
		t.Fatal("restart/import resurrected approval")
	}
}

func TestResearchReviewConcurrentTabsCanConsumeOnlyOnce(t *testing.T) {
	scope, _, gate := reviewFixture()
	review, err := gate.Prepare(context.Background(), scope, Lookup{Operation: "skills_catalog", Query: "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	approved := make(chan *Authorization, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if authorization, err := gate.Approve(context.Background(), scope, review); err == nil {
				approved <- authorization
			}
		}()
	}
	wg.Wait()
	close(approved)
	if len(approved) != 1 {
		t.Fatalf("concurrent token consumed %d times", len(approved))
	}
}
