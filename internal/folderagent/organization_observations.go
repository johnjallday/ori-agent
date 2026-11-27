package folderagent

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Keep only one bounded metadata baseline. Directory coverage must be complete
// on both visits; a missing name in a partial listing says nothing about moves.
type ReviewCoverage struct {
	Authority   string            `json:"authority"`
	At          time.Time         `json:"at"`
	Directories map[string]string `json:"directories"`
	Files       []ReviewSource    `json:"files"`
	Properties  map[string]string `json:"properties,omitempty"`
	Reviewed    map[string]bool   `json:"reviewed"`
}

func (c *ReviewCoverage) add(dir, witness string, listing workspace.DirectoryListing, rows []ReviewSource) {
	if listing.Truncated || len(listing.Entries) > 64 || witness == "" {
		return
	}
	for _, e := range listing.Entries {
		if e.Version.Identity == "" {
			return
		}
	}
	c.Directories[dir] = witness
	c.Files = append(c.Files, rows...)
}
func sameLocationCoverage(before, after *ReviewCoverage, dir string) bool {
	return before.Directories[dir] != "" && before.Directories[dir] == after.Directories[dir]
}
func observationChoices(before, after *ReviewCoverage, review string, known map[string]bool) []OrganizingDecision {
	if before == nil || before.Authority != after.Authority || known == nil {
		return nil
	}
	byID := map[string][]ReviewSource{}
	prior := map[string]int{}
	paths := map[string]bool{}
	for _, s := range after.Files {
		byID[s.Version.Identity] = append(byID[s.Version.Identity], s)
		paths[s.Path] = true
	}
	for _, s := range before.Files {
		prior[s.Version.Identity]++
	}
	var out []OrganizingDecision
	for _, old := range before.Files {
		v := old.Version
		matches := byID[v.Identity]
		if !before.Reviewed[old.Ref] || v.Identity == "" || prior[v.Identity] != 1 || len(matches) != 1 || v.Links != 1 || paths[old.Path] || known[v.Identity] {
			continue
		}
		next := matches[0]
		nv := next.Version
		if nv.Links != 1 || v.Modified != nv.Modified || v.Bytes != nv.Bytes || v.Placeholder || nv.Placeholder || path.Base(old.Path) != path.Base(next.Path) || !sameLocationCoverage(before, after, path.Dir(old.Path)) || !sameLocationCoverage(before, after, path.Dir(next.Path)) {
			continue
		}
		after.Reviewed[next.Ref] = true
		property := before.Properties[old.Ref]
		if property != "" {
			if after.Properties == nil {
				after.Properties = map[string]string{}
			}
			after.Properties[next.Ref] = property
		}
		out = append(out, OrganizingDecision{ID: documentsRef(review, old.Ref, next.Ref), Kind: "observed", Property: property, FileKey: documentsRef(after.Authority, v.Identity), Time: after.At, Sources: []string{old.Ref, next.Ref}, Proposed: old.Path, Choice: next.Path, Outcome: "Observed on disk"})
	}
	return out
}

var propertyLabel = regexp.MustCompile(`^[A-Z][a-z]{1,29} (?:Street|Avenue|Lane|Road|Drive)$`)
var propertyMention = regexp.MustCompile(`\b[A-Z][a-z]{1,29} (?:Street|Avenue|Lane|Road|Drive)\b`)

func invoiceProperty(text string) string {
	if !literalTerm(text, "invoice") {
		return ""
	}
	names := propertyMention.FindAllString(text, -1)
	if len(names) == 0 {
		return ""
	}
	for _, n := range names {
		if n != names[0] {
			return ""
		}
	}
	return names[0]
}

type PlacementSuggestion struct {
	Remembered  bool     `json:"remembered,omitempty"`
	Convention  bool     `json:"convention,omitempty"`
	Destination string   `json:"destination"`
	Support     []string `json:"support"`
}

// Resolve the exact convention afresh. Its absence, ambiguous provenance,
// or any edit disqualifies support that came from the earlier wording.
func placementFor(rec *Record, auth string, item ReviewedDocument, c DocumentsConvention) *PlacementSuggestion {
	if item.Source.Example || item.Property == "" {
		return nil
	}
	if !c.Conflict && c.Text != "" && c.Proof != "" && item.ConventionPin == c.Proof {
		return &PlacementSuggestion{Destination: path.Join(item.Property, "Invoices"), Convention: true}
	}
	return explainPlacement(rec, auth, item.Property)
}
func bindConventionPlacements(raw string, files []ReviewedDocument, c DocumentsConvention) {
	if c.Text == "" || c.Conflict {
		return
	}
	var response struct {
		Placements []struct {
			Ref    string `json:"ref"`
			Folder string `json:"folder"`
		} `json:"placements"`
	}
	if json.Unmarshal([]byte(reviewJSON(raw)), &response) != nil || len(response.Placements) > len(files) {
		return
	}
	for i := range files {
		f := &files[i]
		if f.Property == "" || f.Source.Example {
			continue
		}
		for _, p := range response.Placements {
			if p.Ref == fmt.Sprintf("s%d", i+1) && p.Folder == path.Join(f.Property, "Invoices") {
				f.ConventionPin = c.Proof
			}
		}
	}
}

// Count occasions, not file count. A property pattern needs two distinct
// approved reviews AND two different properties with intentional placements.
func explainPlacement(r *Record, authority, property string) *PlacementSuggestion {
	if property == "" {
		return nil
	}
	ids := map[string]string{}
	latest := map[string]string{}
	for _, run := range r.OrganizationRuns {
		if run.Authority != authority {
			continue
		}
		for _, d := range run.Result.Decisions {
			if d.Kind == "observed" && d.FileKey != "" {
				latest[d.FileKey] = d.ID
			}
		}
	}
	for _, run := range r.OrganizationRuns {
		if run.Authority != authority {
			continue
		}
		for _, d := range run.Result.Decisions {
			if d.Kind != "observed" || !d.Intentional || d.Exception {
				continue
			}
			parts := strings.Split(d.Choice, "/")
			if len(parts) != 3 || d.Property != parts[0] || latest[d.FileKey] != d.ID || parts[1] != "Invoices" || !propertyLabel.MatchString(parts[0]) || ids[parts[0]] != "" {
				continue
			}
			ids[parts[0]] = d.ID
			break
		}
	}
	if len(ids) < 2 {
		return nil
	}
	var support []string
	for _, id := range ids {
		support = append(support, id)
	}
	sort.Strings(support)
	return &PlacementSuggestion{Destination: path.Join(property, "Invoices"), Support: support}
}
