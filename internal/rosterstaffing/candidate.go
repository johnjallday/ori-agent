// Package rosterstaffing owns read-only, evidence-bounded staffing advice. It
// has no writer, tool runner, credential tester or assignment authority.
package rosterstaffing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/store"
)

const (
	MaxCandidates   = 100
	MaxPurposeRunes = 400
	MaxCapabilities = 32
	MaxRoles        = 16
	MaxAlternatives = 3
	MaxPromptBytes  = 64 * 1024
)

// Evidence is host-generated; model replies may cite IDs but cannot author
// reasons. Value is declared metadata, never observed experience or success.
type Evidence struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type Candidate struct {
	Handle       string     `json:"handle"`
	Name         string     `json:"-"` // UI only: names must not substitute for purpose.
	Source       string     `json:"source"`
	Ready        bool       `json:"configured"` // configuration only; no live credentials tested
	Evidence     []Evidence `json:"evidence"`
	Gaps         []string   `json:"gaps"`
	Witness      string     `json:"-"`
	capabilities map[string]bool
}

type Coverage struct {
	Complete            bool `json:"complete"`
	AssignmentsComplete bool `json:"assignments_complete"`
	Inspected           int  `json:"inspected"`
	Included            int  `json:"included"`
}

type Roster struct {
	Candidates []Candidate `json:"candidates"`
	Coverage   Coverage    `json:"coverage"`
}

// Assignment is supplied only by a trusted owner-scoped metadata adapter. It
// must prove the source witness, not just a matching name or customized copy.
// A nil adapter means unknown, never "no other assignments".
type Assignment struct {
	Witness        string
	RoleID         string
	WorkspaceLabel string
}

type Readers struct {
	Profiles        func(context.Context) store.StaffingSnapshot
	Assignments     func(context.Context, string) ([]Assignment, bool)
	ModelConfigured func(provider, model string) bool
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

// Collect examines the full snapshot before imposing a model budget. Coverage
// loss is explicit: even a useful partial match is not an exhaustive ranking.
func Collect(ctx context.Context, owner string, readers Readers) Roster {
	out := Roster{}
	if strings.TrimSpace(owner) == "" || readers.Profiles == nil || ctx.Err() != nil {
		return out
	}
	snapshot := readers.Profiles(ctx)
	out.Coverage.Complete = snapshot.Complete
	assignments := []Assignment(nil)
	if readers.Assignments != nil {
		assignments, out.Coverage.AssignmentsComplete = readers.Assignments(ctx, owner)
	}
	counts := map[string]int{}
	for _, p := range snapshot.Profiles {
		counts[strings.ToLower(p.Name)]++
	}
	for _, p := range snapshot.Profiles {
		if ctx.Err() != nil {
			return Roster{}
		}
		out.Coverage.Inspected++
		if p.Witness == "" || (p.Source != "root" && p.Source != "legacy") || counts[strings.ToLower(p.Name)] != 1 {
			out.Coverage.Complete = false
			continue
		}
		name := safeText(p.Name, 120)
		if name == "" {
			out.Coverage.Complete = false
			continue
		}
		hash := sha256.Sum256([]byte(owner + "\x00" + p.Witness + "\x00" + p.Name))
		c := Candidate{Handle: "c_" + hex.EncodeToString(hash[:12]), Name: name, Source: p.Source, Witness: p.Witness, capabilities: map[string]bool{}}
		c.Ready = !p.Paused && readers.ModelConfigured != nil && readers.ModelConfigured(p.Provider, p.Model)
		if !c.Ready {
			c.Gaps = append(c.Gaps, "configuration_unavailable")
		}
		add := func(kind, value string) {
			c.Evidence = append(c.Evidence, Evidence{ID: fmt.Sprintf("%s_e%d", c.Handle, len(c.Evidence)+1), Kind: kind, Value: value})
		}
		if purpose := safeText(p.Purpose, MaxPurposeRunes); purpose != "" {
			add("declared_purpose", purpose)
		} else {
			c.Gaps = append(c.Gaps, "purpose_unknown")
		}
		if safeIdentifier(p.Role) {
			add("declared_role", p.Role)
		}
		capabilities := append([]string(nil), p.Capabilities...)
		sort.Strings(capabilities)
		for _, capability := range capabilities {
			if !safeIdentifier(capability) || c.capabilities[capability] {
				continue
			}
			if len(c.capabilities) == MaxCapabilities {
				c.Gaps = append(c.Gaps, "capability_metadata_partial")
				break
			}
			c.capabilities[capability] = true
			add("declared_capability", capability)
		}
		if !out.Coverage.AssignmentsComplete {
			c.Gaps = append(c.Gaps, "assignments_unknown")
		}
		count := 0
		for _, assignment := range assignments {
			if assignment.Witness != p.Witness || !safeIdentifier(assignment.RoleID) {
				continue
			}
			label := safeText(assignment.WorkspaceLabel, 120)
			if label == "" {
				continue
			}
			if count == 8 {
				c.Gaps = append(c.Gaps, "assignment_metadata_partial")
				break
			}
			add("current_assignment", assignment.RoleID+" — "+label)
			count++
		}
		out.Candidates = append(out.Candidates, c)
	}
	// Prefer substantive metadata when coverage is limited, never agent names.
	sort.SliceStable(out.Candidates, func(i, j int) bool {
		a, b := out.Candidates[i], out.Candidates[j]
		if a.Ready != b.Ready {
			return a.Ready
		}
		if hasPurpose(a) != hasPurpose(b) {
			return hasPurpose(a)
		}
		return a.Handle < b.Handle
	})
	if len(out.Candidates) > MaxCandidates {
		out.Candidates = out.Candidates[:MaxCandidates]
		out.Coverage.Complete = false
	}
	out.Coverage.Included = len(out.Candidates)
	return out
}

func safeIdentifier(value string) bool {
	return identifier.MatchString(value) && safeText(value, 80) == value
}

func hasPurpose(c Candidate) bool {
	for _, e := range c.Evidence {
		if e.Kind == "declared_purpose" {
			return true
		}
	}
	return false
}

// Authored descriptions are allowed, but never a route for obvious private
// paths/config references or credential-shaped strings. Redact the entire
// field rather than trying to produce a misleading partial purpose.
func safeText(value string, limit int) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, marker := range []string{"/", "\\", "sk-", "api_key", "api key", "token=", "password", "secret", "ori_local_config_id", "bearer "} {
		if strings.Contains(lower, marker) {
			return ""
		}
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ""
		}
	}
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}
