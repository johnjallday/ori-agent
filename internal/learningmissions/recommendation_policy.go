package learningmissions

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

// Keep the bounded preference record below the existing 8-KiB storage limit.
const MaxRecommendationExclusions = 12
const MaxPolicyRecent = 100

// RecommendationExclusion is a durable owner choice, not model inference.
// An empty Topic matches every explicit focus in this workspace/structure.
// Until == 0 is a persistent decline; a positive value is a defer cooldown.
// Workspace identity deliberately survives reimport: reimport must not silently
// reset a person's preferences even though it cannot restore evidence authority.
type RecommendationExclusion struct {
	WorkspaceID string      `json:"studio_id"`
	Structure   StructureID `json:"structure"`
	Topic       string      `json:"topic,omitempty"`
	Until       int64       `json:"until_ms,omitempty"`
}

// Validate is shared with the preference owner before storing an exclusion.
func (e RecommendationExclusion) Validate() error {
	if !validID(e.WorkspaceID) || (e.Topic != "" && !validDigest(e.Topic)) || e.Until < 0 {
		return ErrInvalid
	}
	if _, err := Lookup(e.Structure, DefinitionVersion); err != nil {
		return ErrInvalid
	}
	return nil
}

func validPolicySignals(signals []string) bool {
	allowed := []string{"chosen_workspace", "ready_bounded_process", "chosen_structure", "explicit_focus_words_match", "no_recorded_practice_for_structure"}
	if len(signals) > len(allowed) {
		return false
	}
	seen := map[string]bool{}
	for _, signal := range signals {
		if !slices.Contains(allowed, signal) || seen[signal] {
			return false
		}
		seen[signal] = true
	}
	return true
}

func recommendationTopic(focus string) string {
	return digest([]byte(strings.ToLower(strings.Join(strings.Fields(focus), " "))))
}

// policyContext is host-only. Its counts come from the deduplicated evidence
// owner, never activity counts, generated copy, visits or elapsed time. A failed
// evidence read must leave PracticeKnown false, not manufacture a zero score.
type policyContext struct {
	WorkspaceIDs   []string
	Mode           string
	Active         bool
	ReviewableWork bool // prefer an existing qualifying review over more work
	Requested      StructureID
	Focus          string
	Effort         string
	Now            int64
	PracticeKnown  bool
	Practice       map[string]int
	Independent    map[string]int // current version's no-additional-Ori-coaching path only
	Recent         []StructureID
	Exclusions     []RecommendationExclusion
	AvoidRef       string
}

type rankedCandidate struct {
	Candidate EligibleCandidate
	Signals   []string
	focus     int
	needed    int
	effort    int
	repeated  int
}

// rankCandidates only orders already eligible canonical candidates. It grants
// no permission, rewrites no accepted definition and invokes no model. The
// caller must still revalidate the selected exact source before publication.
func rankCandidates(candidates []EligibleCandidate, p policyContext) []rankedCandidate {
	if p.Active || p.Mode == "off" || !slices.Contains([]string{"on_request", "automatic"}, p.Mode) ||
		len(p.WorkspaceIDs) == 0 || len(p.WorkspaceIDs) > MaxWorkspaces || len(candidates) > MaxCandidates ||
		p.Now <= 0 || !boundedText(p.Focus, 300, false) || !slices.Contains([]string{"small", "standard"}, p.Effort) ||
		len(p.Recent) > MaxPolicyRecent || len(p.Exclusions) > MaxRecommendationExclusions || !validSelection(p.Requested, p.AvoidRef) ||
		(p.ReviewableWork && p.Requested == "") {
		return nil
	}
	for _, exclusion := range p.Exclusions {
		if exclusion.Validate() != nil {
			return nil
		}
	}
	out := make([]rankedCandidate, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		structure, err := Lookup(candidate.Structure, DefinitionVersion)
		if err != nil || !slices.Contains(p.WorkspaceIDs, candidate.WorkspaceID) ||
			(p.Requested != "" && candidate.Structure != p.Requested) || candidate.Ref == p.AvoidRef || seen[candidate.Ref] ||
			!validID(candidate.Ref) || !validID(candidate.WorkspaceID) || !validID(candidate.Incarnation) ||
			!validDigest(candidate.ScopeDigest) || !boundedText(candidate.Name, 120, true) || !boundedText(candidate.ReviewedGoal, 300, false) ||
			(candidate.Structure != DelegateInspect && !validID(candidate.OriginalReceipt)) {
			continue
		}
		suppressed := false
		for _, exclusion := range p.Exclusions {
			if exclusion.WorkspaceID == candidate.WorkspaceID && exclusion.Structure == candidate.Structure &&
				(exclusion.Topic == "" || exclusion.Topic == recommendationTopic(p.Focus)) &&
				(exclusion.Until == 0 || exclusion.Until > p.Now) {
				suppressed = true
				break
			}
		}
		if suppressed {
			continue
		}
		seen[candidate.Ref] = true
		ranked := rankedCandidate{Candidate: candidate, Signals: []string{"chosen_workspace", "ready_bounded_process"}}
		if p.Requested != "" {
			ranked.Signals = append(ranked.Signals, "chosen_structure")
		}
		if explicitFocusOverlap(p.Focus, candidate.Name+" "+candidate.ReviewedGoal) {
			ranked.focus = 1
			ranked.Signals = append(ranked.Signals, "explicit_focus_words_match")
		}
		competency := map[StructureID]string{DelegateInspect: "delegate", ImproveResult: "improve", RepeatSuccess: "reuse"}[candidate.Structure]
		if p.PracticeKnown && p.Practice[competency] == 0 {
			ranked.needed = 1
			ranked.Signals = append(ranked.Signals, "no_recorded_practice_for_structure")
		}
		if p.Effort == "small" {
			ranked.effort = len(structure.Steps)
		}
		for _, recent := range p.Recent {
			if recent == candidate.Structure {
				ranked.repeated++
			}
		}
		out = append(out, ranked)
	}
	slices.SortStableFunc(out, func(a, b rankedCandidate) int {
		for _, value := range []int{cmp.Compare(b.focus, a.focus), cmp.Compare(b.needed, a.needed),
			cmp.Compare(a.effort, b.effort), cmp.Compare(a.repeated, b.repeated), cmp.Compare(string(a.Candidate.Structure), string(b.Candidate.Structure)), cmp.Compare(a.Candidate.Ref, b.Candidate.Ref)} {
			if value != 0 {
				return value
			}
		}
		return 0
	})
	return out
}

// This is a literal match against explicit words, not a semantic interest or
// ability classifier. Never claim inferred intent from a workspace name.
func explicitFocusOverlap(focus, text string) bool {
	words := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	}
	stop := []string{"a", "an", "and", "for", "in", "my", "of", "on", "the", "to", "with"}
	wanted := map[string]bool{}
	for _, word := range words(focus) {
		if !slices.Contains(stop, word) {
			wanted[word] = true
		}
	}
	for _, word := range words(text) {
		if wanted[word] {
			return true
		}
	}
	return false
}
