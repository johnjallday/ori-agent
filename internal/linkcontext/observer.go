package linkcontext

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/publicread"
	"github.com/johnjallday/ori-agent/internal/urlsafety"
)

// Why an address was refused. Each carries copy the user can read; a handler
// maps them with Refusal.
var (
	ErrURLTooLong = errors.New("link context: address is too long")
	ErrURLUnsafe  = errors.New("link context: address is not allowed")
	ErrURLEmpty   = errors.New("link context: address is required")
)

// Refusal turns a refused attach into the sentence the user sees. ok == false
// means the error is not a refusal of the address itself.
func Refusal(err error) (message string, ok bool) {
	switch {
	case errors.Is(err, ErrURLEmpty):
		return "Paste a link first.", true
	case errors.Is(err, ErrURLTooLong):
		return fmt.Sprintf("Links longer than %d characters are not accepted.", MaxURLLength), true
	case errors.Is(err, ErrURLUnsafe):
		return "That address can’t be used here. Only public http or https links without a token or key in them are accepted.", true
	}
	return "", false
}

// Page is what a fetcher learned about a non-repository address.
type Page struct {
	// URL is the final address after any redirect the fetcher allowed.
	URL         string
	Kind        Kind
	Title       string
	Description string
	Readable    bool
}

// PageFetcher reads one public page's title and description. It reports a
// page that could not be read as Readable == false, not as an error; an error
// means the address itself was refused on the way (a redirect to a private
// host, for example).
type PageFetcher interface {
	Fetch(ctx context.Context, target *url.URL) (Page, error)
}

// UnreadablePages is the fetcher of an observer that cannot read pages: every
// non-repository link attaches by its address alone.
type UnreadablePages struct{}

func (UnreadablePages) Fetch(_ context.Context, target *url.URL) (Page, error) {
	return Page{URL: target.String(), Kind: KindPage}, nil
}

// Observer turns an address into an Observation: the repository path for
// github.com repositories, the page path for everything else.
type Observer struct {
	repos RepoResolver
	pages PageFetcher
	now   func() time.Time
	newID func() string
}

// NewObserver builds an observer. A nil resolver attaches repositories by
// name only; a nil fetcher attaches pages by address only.
func NewObserver(repos RepoResolver, pages PageFetcher) *Observer {
	if pages == nil {
		pages = UnreadablePages{}
	}
	return &Observer{repos: repos, pages: pages, now: time.Now, newID: uuid.NewString}
}

// WithClock fixes the observer's clock and id source for tests.
func (o *Observer) WithClock(now func() time.Time, newID func() string) *Observer {
	if now != nil {
		o.now = now
	}
	if newID != nil {
		o.newID = newID
	}
	return o
}

// Observe records what the address says about itself. The address must be a
// public http or https link of at most MaxURLLength characters; anything
// else is refused and nothing is fetched.
func (o *Observer) Observe(ctx context.Context, rawURL string) (*Observation, error) {
	if o == nil {
		return nil, ErrNoRepoResolver
	}
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return nil, ErrURLEmpty
	}
	if len(raw) > MaxURLLength || utf8.RuneCountInString(raw) > MaxURLLength {
		return nil, ErrURLTooLong
	}
	// A clone address (git@github.com:owner/name.git) is not a URL at all, so
	// repositories are recognized before the address is parsed as one.
	if owner, name, ok := ParseGitHubRepo(raw); ok {
		return o.observeRepo(ctx, owner, name)
	}
	parsed, err := urlsafety.Parse(raw, urlsafety.Policy{BlockPrivateHosts: true})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrURLUnsafe, err)
	}
	parsed.Fragment = ""
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.User != nil {
		return nil, fmt.Errorf("%w: credentials in the address", ErrURLUnsafe)
	}
	// The address itself reaches the model, so one that carries a token, a
	// signature, or a key is refused before anything is fetched.
	if _, err := publicread.ValidateURL(parsed.String()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrURLUnsafe, err)
	}
	page, err := o.pages.Fetch(ctx, parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrURLUnsafe, err)
	}
	finalURL := strings.TrimSpace(page.URL)
	if finalURL == "" {
		finalURL = parsed.String()
	}
	final, err := urlsafety.Parse(finalURL, urlsafety.Policy{BlockPrivateHosts: true})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrURLUnsafe, err)
	}
	kind := page.Kind
	if kind != KindDocument {
		kind = KindPage
	}
	observation := &Observation{
		Version: Version, ID: o.newID(), URL: final.String(), Host: strings.ToLower(final.Hostname()), Kind: kind,
		Title: Text(page.Title), Description: Text(page.Description), Readable: page.Readable, FetchedAt: o.now().UTC(),
	}
	if len(observation.URL) > MaxURLLength {
		return nil, ErrURLTooLong
	}
	if err := observation.Validate(); err != nil {
		return nil, err
	}
	return observation, nil
}

func (o *Observer) observeRepo(ctx context.Context, owner, name string) (*Observation, error) {
	repo := Repo{Owner: owner, Name: name, FullName: owner + "/" + name, Reachable: ReachableUnknown}
	if o.repos != nil {
		resolved, err := o.repos.Resolve(ctx, owner, name)
		if err != nil {
			return nil, err
		}
		repo = resolved
	}
	observation := &Observation{
		Version: Version, ID: o.newID(), URL: RepoURL(owner, name), Host: githubHost, Kind: KindGitHubRepo,
		Title: repo.FullName, Description: repo.Description, Readable: repo.Reachable == ReachableYes,
		FetchedAt: o.now().UTC(), Repo: &repo,
	}
	if err := observation.Validate(); err != nil {
		return nil, err
	}
	return observation, nil
}

func utf8RuneCount(s string) int { return utf8.RuneCountInString(s) }
