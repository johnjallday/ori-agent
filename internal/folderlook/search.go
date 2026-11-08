package folderlook

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// Bounds of source (d), the marketplace search (PRD FR19).
const (
	MaxSearchWords      = 6
	MaxSearchCandidates = 12
	maxCandidateDetail  = 140
)

// SearchHit is one result of the existing marketplace searches, normalized
// by the host: a skill, an MCP server or a plugin.
type SearchHit struct {
	Kind        string // KindSkill, KindMCP or KindPlugin
	ID          string // the marketplace's own stable id
	Name        string
	Description string
	Marketplace string
}

// CandidateID is the stable candidate id of a search hit.
func (h SearchHit) CandidateID() string {
	return h.Kind + ":" + strings.ToLower(strings.TrimSpace(h.ID))
}

// Installed says what the user already has, by kind and marketplace id.
type Installed func(kind, id string) bool

// SearchWords are the words the host may search with: the tool table's own
// names for the formats found and each shape's label. Never a folder name
// or a file name (PRD FR19).
func SearchWords(facts foldermap.Facts) []string {
	var words []string
	seen := make(map[string]bool)
	add := func(word string) {
		word = strings.TrimSpace(word)
		key := strings.ToLower(word)
		if word == "" || seen[key] || len(words) >= MaxSearchWords {
			return
		}
		seen[key] = true
		words = append(words, word)
	}
	projects := append(facts.Members, projectsOf(facts)...)
	markers := make(map[foldermap.ShapeCount]string)
	for _, p := range projects {
		key := foldermap.ShapeCount{Shape: p.Shape, Format: p.Format}
		if _, ok := markers[key]; !ok {
			markers[key] = p.Marker
		}
	}
	for _, s := range facts.Shapes {
		add(foldermap.FormatName(markers[foldermap.ShapeCount{Shape: s.Shape, Format: s.Format}]))
	}
	for _, s := range facts.ShapeTotals() {
		if s.Shape != folderdigest.ShapeInbox {
			add(string(s.Shape))
		}
	}
	return words
}

// SearchCandidates turns search hits into inert candidates: deduplicated by
// id (and against the host's own candidates), already-installed ones marked
// "already here", ones the user declined before left out, at most
// MaxSearchCandidates, in a stable order.
func SearchCandidates(hits []SearchHit, existing []Candidate, installed Installed, declined func(id string) bool) []Candidate {
	seen := make(map[string]bool, len(existing))
	for _, c := range existing {
		seen[c.ID] = true
	}
	var out []Candidate
	sorted := append([]SearchHit(nil), hits...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return kindOrder(sorted[i].Kind) < kindOrder(sorted[j].Kind)
		}
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})
	for _, hit := range sorted {
		if hit.ID == "" || hit.Name == "" || kindOrder(hit.Kind) == len(searchKinds) {
			continue
		}
		id := hit.CandidateID()
		if seen[id] || (declined != nil && declined(id)) {
			continue
		}
		seen[id] = true
		c := Candidate{
			ID: id, Ref: strings.TrimSpace(hit.ID), Kind: hit.Kind, Name: oneLine(hit.Name), Source: SourceSearch, Action: ActionAdd,
			Detail: clip(oneLine(hit.Description), maxCandidateDetail), Marketplace: oneLine(hit.Marketplace),
		}
		if installed != nil && installed(hit.Kind, hit.ID) {
			c.Source, c.Action, c.Detail = SourceInstalled, ActionAlreadyHere, "already here"
		}
		out = append(out, c)
		if len(out) == MaxSearchCandidates {
			break
		}
	}
	return out
}

var searchKinds = []string{KindPlugin, KindSkill, KindMCP}

func kindOrder(kind string) int {
	for i, k := range searchKinds {
		if k == kind {
			return i
		}
	}
	return len(searchKinds)
}

func clip(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
