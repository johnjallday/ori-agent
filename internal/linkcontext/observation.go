// Package linkcontext defines the bounded, metadata-only record Ori keeps of
// a link the user attaches when talking to the assistant: what the page or
// repository says about itself, never what it contains. It deliberately holds
// no page text, README, issue text, or HTML; reading a page is the new
// workspace's own first task, with its own consent.
package linkcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	Version = 1
	// MaxBytes bounds the whole observation as JSON. A larger record is a
	// refusal, never a truncated prompt.
	MaxBytes = 4096
	// MaxTextRunes bounds the title and the description, each.
	MaxTextRunes = 200
	MaxTopics    = 10
	MaxTopicLen  = 50
	// MaxURLLength is the longest address accepted anywhere in this package.
	MaxURLLength = 2000
	// HeldTTL is how long a link the composer attached stays held on the
	// server without being used, the same as a folder selection.
	HeldTTL = 30 * time.Minute
)

// Kind is what the link turned out to be.
type Kind string

const (
	// KindGitHubRepo is a repository on github.com, observed through the API.
	KindGitHubRepo Kind = "github_repo"
	// KindDocument is a file such as a PDF: known by its address, not read.
	KindDocument Kind = "document"
	// KindPage is any other page.
	KindPage Kind = "page"
)

// Reachability says whether a repository could be confirmed to exist.
type Reachability string

const (
	ReachableYes     Reachability = "yes"
	ReachableNo      Reachability = "no"
	ReachableUnknown Reachability = "unknown"
)

var ErrInvalid = errors.New("link observation is invalid")

// Repo is what GitHub says about a repository. Description is the repository's
// own one-line description; nothing from its README or issues is here.
type Repo struct {
	Owner         string       `json:"owner"`
	Name          string       `json:"name"`
	FullName      string       `json:"full_name"`
	Description   string       `json:"description,omitempty"`
	Language      string       `json:"language,omitempty"`
	Topics        []string     `json:"topics,omitempty"`
	DefaultBranch string       `json:"default_branch,omitempty"`
	Private       bool         `json:"private,omitempty"`
	Archived      bool         `json:"archived,omitempty"`
	OpenIssues    int          `json:"open_issues"`
	PushedAt      string       `json:"pushed_at,omitempty"` // a date, YYYY-MM-DD
	Reachable     Reachability `json:"reachable"`
}

// Observation is a dated record of one link: enough to choose a workspace for
// it and to name that workspace, and nothing that was behind the address.
type Observation struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	URL         string `json:"url"`
	Host        string `json:"host"`
	Kind        Kind   `json:"kind"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Readable reports whether the page said anything about itself. False
	// means only the address could be read.
	Readable  bool      `json:"readable"`
	FetchedAt time.Time `json:"fetched_at"`
	Repo      *Repo     `json:"repo,omitempty"`
}

// Validate checks the caps and the enums. A record that fails here must not
// be stored or shown to a model.
func (o Observation) Validate() error {
	if o.Version != Version {
		return fmt.Errorf("%w: version", ErrInvalid)
	}
	if strings.TrimSpace(o.ID) == "" || len(o.ID) > 64 {
		return fmt.Errorf("%w: id", ErrInvalid)
	}
	if strings.TrimSpace(o.URL) == "" || len(o.URL) > MaxURLLength {
		return fmt.Errorf("%w: url", ErrInvalid)
	}
	if strings.TrimSpace(o.Host) == "" || len(o.Host) > 253 {
		return fmt.Errorf("%w: host", ErrInvalid)
	}
	switch o.Kind {
	case KindGitHubRepo, KindDocument, KindPage:
	default:
		return fmt.Errorf("%w: kind", ErrInvalid)
	}
	if !boundedText(o.Title) || !boundedText(o.Description) {
		return fmt.Errorf("%w: text", ErrInvalid)
	}
	if o.FetchedAt.IsZero() {
		return fmt.Errorf("%w: fetched_at", ErrInvalid)
	}
	if (o.Kind == KindGitHubRepo) != (o.Repo != nil) {
		return fmt.Errorf("%w: repo", ErrInvalid)
	}
	if o.Repo != nil {
		if err := o.Repo.validate(); err != nil {
			return err
		}
	}
	data, err := json.Marshal(o)
	if err != nil || len(data) > MaxBytes {
		return fmt.Errorf("%w: size", ErrInvalid)
	}
	return nil
}

func (r Repo) validate() error {
	if !ValidRepoName(r.Owner, r.Name) || r.FullName != r.Owner+"/"+r.Name {
		return fmt.Errorf("%w: repo name", ErrInvalid)
	}
	if !boundedText(r.Description) || !boundedText(r.Language) || !boundedText(r.DefaultBranch) {
		return fmt.Errorf("%w: repo text", ErrInvalid)
	}
	if len(r.Topics) > MaxTopics {
		return fmt.Errorf("%w: topics", ErrInvalid)
	}
	for _, topic := range r.Topics {
		if topic == "" || utf8.RuneCountInString(topic) > MaxTopicLen || !boundedText(topic) {
			return fmt.Errorf("%w: topic", ErrInvalid)
		}
	}
	if r.OpenIssues < 0 {
		return fmt.Errorf("%w: open issues", ErrInvalid)
	}
	if r.PushedAt != "" {
		if _, err := time.Parse("2006-01-02", r.PushedAt); err != nil {
			return fmt.Errorf("%w: pushed_at", ErrInvalid)
		}
	}
	switch r.Reachable {
	case ReachableYes, ReachableNo, ReachableUnknown:
	default:
		return fmt.Errorf("%w: reachable", ErrInvalid)
	}
	return nil
}

// DisplayName is what the pane and the transcript call the link: its title,
// else the repository's full name, else its host.
func (o Observation) DisplayName() string {
	if title := strings.TrimSpace(o.Title); title != "" {
		return title
	}
	if o.Repo != nil && o.Repo.FullName != "" {
		return o.Repo.FullName
	}
	return o.Host
}

// Text bounds a string the way every text field here is bounded: controls
// stripped, whitespace collapsed, at most MaxTextRunes runes.
func Text(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > MaxTextRunes {
		runes = runes[:MaxTextRunes]
	}
	return strings.TrimSpace(string(runes))
}

func boundedText(value string) bool {
	if utf8.RuneCountInString(value) > MaxTextRunes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
