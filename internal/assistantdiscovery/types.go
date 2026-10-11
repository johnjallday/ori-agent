// Package assistantdiscovery composes narrowly owned discovery readers. It has
// no installer, agent loadout, memory writer, MCP dispatcher or shell interface.
package assistantdiscovery

import "time"

const (
	MaxExternalOperations = 4
	MaxCandidates         = 8
	MaxResearchRunes      = 16000
	MaxExcerptRunes       = 4000
	MaxQueryRunes         = 256
	MaxURLBytes           = 2000
	MaxResponseBytes      = 1 << 20
	MaxRedirects          = 3
	HTTPTimeout           = 10 * time.Second
	TurnTimeout           = 45 * time.Second
)

type Availability string

const (
	Available       Availability = "available"
	Partial         Availability = "partial"
	Empty           Availability = "empty"
	Unavailable     Availability = "unavailable"
	MissingRuntime  Availability = "missing_runtime"
	DisabledSource  Availability = "disabled_source"
	MalformedOutput Availability = "malformed_output"
	StaleCache      Availability = "stale_cache"
)

type Observation string

const (
	Unknown     Observation = "unknown"
	Observed    Observation = "observed"
	NotObserved Observation = "not_observed"
)

// Readiness dimensions are independent. Installed metadata, a declared
// dependency, enabled configuration and granted/tested operation are different
// facts; no dimension is inferred from another one's display name or status.
type Readiness struct {
	Installed    Observation  `json:"installed"`
	Configured   Observation  `json:"configured"`
	Enabled      Observation  `json:"enabled"`
	Granted      Observation  `json:"granted"`
	Verified     Observation  `json:"verified"`
	Dependencies Dependencies `json:"dependencies"`
}

type Dependencies struct {
	State               string      `json:"state"` // unknown, declared; never "all satisfied"
	MCPServers          []string    `json:"mcp_servers,omitempty"`
	Tools               []string    `json:"tools,omitempty"`
	ConfigurationFields []string    `json:"configuration_fields,omitempty"`
	Completeness        Observation `json:"completeness"`
}

func UnknownReadiness() Readiness {
	return Readiness{Installed: Unknown, Configured: Unknown, Enabled: Unknown, Granted: Unknown, Verified: Unknown,
		Dependencies: Dependencies{State: "unknown", Completeness: Unknown}}
}

// Receipt describes actual completed metadata/document reads, not permission
// to read again. A listing is explicitly not document inspection. SourceID is
// stable identity; the per-turn citation Key will be issued by the host ledger.
// Excerpt is bounded transient tool data; persisted history uses references,
// never another copy of source text or an executable approval.
type Receipt struct {
	Key          string       `json:"key,omitempty"`
	SourceID     string       `json:"source_id"`
	CandidateID  string       `json:"candidate_id,omitempty"`
	Kind         string       `json:"kind"`
	Level        string       `json:"level"` // metadata or document
	URL          string       `json:"url,omitempty"`
	ReadAt       time.Time    `json:"read_at"`
	ObservedAt   time.Time    `json:"observed_at,omitzero"`
	Revision     string       `json:"revision,omitempty"`
	ContentHash  string       `json:"content_hash,omitempty"`
	Excerpt      string       `json:"excerpt,omitempty"`
	Truncated    bool         `json:"truncated"`
	Availability Availability `json:"availability"`
	Freshness    string       `json:"freshness"` // current_observation, compiled, cached, stale
}

type Candidate struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Package     string    `json:"package,omitempty"`
	URL         string    `json:"url,omitempty"`
	Readiness   Readiness `json:"readiness"`
	Receipt     Receipt   `json:"receipt"`
}

type Result struct {
	Availability Availability `json:"availability"`
	Reason       string       `json:"reason,omitempty"`
	Candidates   []Candidate  `json:"candidates"`
	Truncated    bool         `json:"truncated"`
	// Scope describes exactly which inventory/catalog was inspected, not a grant.
	Scope string `json:"scope"`
}
