package assistantcontext

import (
	"regexp"
	"time"

	"github.com/johnjallday/ori-agent/internal/publicread"
)

// ResearchRef is historical reference metadata only: no excerpt, request,
// approval token, tool schema, command, credential or capability grant.
type ResearchRef struct {
	Key         string    `json:"key"`
	SourceID    string    `json:"source_id"`
	CandidateID string    `json:"candidate_id,omitempty"`
	Kind        string    `json:"kind"`
	Level       string    `json:"level"`
	Name        string    `json:"name"`
	Package     string    `json:"package,omitempty"`
	URL         string    `json:"url,omitempty"`
	ReadAt      time.Time `json:"read_at"`
	ObservedAt  time.Time `json:"observed_at,omitzero"`
	ContentHash string    `json:"content_hash,omitempty"`
	Revision    string    `json:"revision,omitempty"`
	Truncated   bool      `json:"truncated"`
	Freshness   string    `json:"freshness"`
	Cited       bool      `json:"cited,omitempty"`
}

var researchKey = regexp.MustCompile(`^S[1-9][0-9]{0,8}$`)
var researchID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validResearchRef(ref ResearchRef) bool {
	if !researchKey.MatchString(ref.Key) || !researchID.MatchString(ref.SourceID) || ref.ReadAt.IsZero() || len(ref.Name) > 480 || len(ref.Revision) > 512 || len(ref.Package) > 200 {
		return false
	}
	if ref.CandidateID != "" && !researchID.MatchString(ref.CandidateID) {
		return false
	}
	if ref.Level != "metadata" && ref.Level != "document" {
		return false
	}
	switch ref.Kind {
	case "installed_skill_metadata", "mcp_configuration_metadata", "builtin_listing", "cached_listing", "skill_catalog_listing", "public_search_listing", "public_document":
	default:
		return false
	}
	switch ref.Freshness {
	case "current_observation", "compiled", "cached", "stale":
	default:
		return false
	}
	if ref.URL != "" {
		if _, err := publicread.ValidateURL(ref.URL); err != nil {
			return false
		}
	}
	return true
}
