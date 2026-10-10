package skills

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MarketplaceQueryRunes    = 256
	MarketplaceCandidates    = 8
	MarketplaceOutputBytes   = 128 << 10
	MarketplaceSearchTimeout = 20 * time.Second
	MarketplaceAPI           = "https://skills.sh"
)

// MarketplaceMatch is catalog metadata, not an inspected skill or installation.
// The URL is constructed from a validated package, never copied from CLI prose.
type MarketplaceMatch struct {
	Package    string `json:"package"`
	Repository string `json:"repository"`
	Skill      string `json:"skill"`
	URL        string `json:"url,omitempty"`
	Installs   string `json:"installs,omitempty"`
}

type MarketplaceSearchResult struct {
	State          string             `json:"state"`
	Reason         string             `json:"reason,omitempty"`
	Results        []MarketplaceMatch `json:"results"`
	ObservedAt     time.Time          `json:"observed_at"`
	RuntimeVersion string             `json:"runtime_version,omitempty"`
	SourceHash     string             `json:"source_hash,omitempty"`
	Revision       string             `json:"revision,omitempty"`
	Truncated      bool               `json:"truncated"`
}

// MarketplaceCatalog is deliberately search-only. Install/update/remove retain
// their existing owner and runner; this interface cannot invoke them.
type MarketplaceCatalog interface {
	Search(context.Context, string, int) MarketplaceSearchResult
}

type MarketplaceSearcher struct {
	resolve func() (marketplaceRuntime, string)
	run     func(context.Context, marketplaceRuntime, string) (string, error)
}

func NewMarketplaceSearcher() *MarketplaceSearcher {
	return &MarketplaceSearcher{resolve: resolveMarketplaceRuntime, run: runMarketplaceSearch}
}

// ValidateMarketplaceQuery refuses rather than truncates or changes egress data.
// A caller must review the exact returned query before invoking Search.
func ValidateMarketplaceQuery(query string) error {
	if !utf8.ValidString(query) || strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > MarketplaceQueryRunes {
		return errors.New("a nonempty query of at most 256 characters is required")
	}
	for _, ch := range query {
		if unicode.IsControl(ch) || unicode.Is(unicode.Cf, ch) {
			return errors.New("query contains control characters")
		}
	}
	for _, field := range strings.Fields(query) {
		if strings.HasPrefix(field, "-") {
			return errors.New("query must not contain command options")
		}
	}
	return nil
}

func (s *MarketplaceSearcher) Search(ctx context.Context, query string, limit int) MarketplaceSearchResult {
	result := MarketplaceSearchResult{State: "unavailable", Results: []MarketplaceMatch{}}
	if ValidateMarketplaceQuery(query) != nil {
		result.State, result.Reason = "invalid_query", "invalid_query"
		return result
	}
	if ctx.Err() != nil {
		result.Reason = "cancelled"
		return result
	}
	runtime, reason := s.resolve()
	if reason != "" {
		result.State, result.Reason = "missing_runtime", reason
		return result
	}
	result.RuntimeVersion = runtime.version
	ctx, cancel := context.WithTimeout(ctx, MarketplaceSearchTimeout)
	defer cancel()
	output, err := s.run(ctx, runtime, query)
	if err != nil {
		switch {
		case errors.Is(err, errMarketplaceOutputLimit):
			result.Reason, result.Truncated = "output_limit", true
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			result.Reason = "timeout"
		case errors.Is(ctx.Err(), context.Canceled):
			result.Reason = "cancelled"
		default:
			result.Reason = "search_failed"
		}
		return result // Failed output is never evidence or diagnostic provider input.
	}
	if len(output) > MarketplaceOutputBytes {
		result.Reason, result.Truncated = "output_limit", true
		return result
	}
	result = ParseMarketplaceSearchOutput(output, limit)
	result.RuntimeVersion = runtime.version
	result.ObservedAt = time.Now().UTC()
	return result
}

var (
	marketplaceANSI     = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	marketplaceSpec     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9._-]*$`)
	marketplaceInstalls = regexp.MustCompile(`\b([0-9][0-9.,]*(?:[KMB])?\s+installs)\b`)
)

// ParseMarketplaceSearchOutput is shared by the HTTP browser and assistant.
// skills 1.7.2 prints "No skills found" on some service failures too: zero
// matches is explicitly unverified, never a successful empty search receipt.
func ParseMarketplaceSearchOutput(output string, limit int) MarketplaceSearchResult {
	result := MarketplaceSearchResult{State: "malformed_output", Reason: "unrecognized_output", Results: []MarketplaceMatch{}}
	if !utf8.ValidString(output) || len(output) > MarketplaceOutputBytes {
		return result
	}
	if limit <= 0 || limit > MarketplaceCandidates {
		limit = MarketplaceCandidates
	}
	cleaned := marketplaceANSI.ReplaceAllString(output, "")
	seen := map[string]bool{}
	malformed := false
	for _, raw := range strings.Split(cleaned, "\n") {
		line := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(raw), "│└├─•·"))
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		spec := fields[0]
		if !marketplaceSpec.MatchString(spec) || len(spec) > 200 {
			if strings.Contains(spec, "@") && !strings.Contains(spec, "://") && spec != "<owner/repo@skill>" {
				malformed = true
			}
			continue
		}
		if seen[spec] {
			continue
		}
		seen[spec] = true
		if len(result.Results) == limit {
			result.Truncated = true
			continue
		}
		repo, name, _ := strings.Cut(spec, "@")
		match := MarketplaceMatch{Package: spec, Repository: repo, Skill: name, URL: MarketplaceAPI + "/" + repo + "/" + name}
		if installs := marketplaceInstalls.FindStringSubmatch(line); len(installs) > 1 {
			match.Installs = installs[1]
		}
		result.Results = append(result.Results, match)
	}
	if len(result.Results) > 0 {
		result.State, result.Reason = "available", ""
		if malformed || result.Truncated {
			result.State, result.Reason = "partial", "incomplete_listing"
		}
	} else if strings.Contains(cleaned, "No skills found") {
		result.State, result.Reason = "unavailable", "unverified_empty"
	}
	return result
}
