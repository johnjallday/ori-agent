package folderagent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/diridentity"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// HomeBranch describes saved observations, never a separate grant or workspace.
// Only ID is accepted from a client. Witness is private to plan execution.
type HomeBranch struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	Included bool   `json:"included"`
	Partial  bool   `json:"partial,omitempty"`
	witness  string
}

func branchHandle(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "branch:" + hex.EncodeToString(sum[:])
}

// bindHomeBranches retains the full portfolio decision. Only the observations
// used to word and execute deeper listings are restricted to the selection.
func bindHomeBranches(rec Record, plan *Plan, selected []string) foldermap.Facts {
	m := rec.Map
	if m == nil || rec.Facts.Portfolio == nil {
		return rec.Facts
	}
	root, ok := m.Folder("")
	if !ok || root.Witness == "" {
		return rec.Facts
	} // legacy look
	counts := map[string]map[string]int{}
	for _, p := range rec.Facts.Members {
		name, _ := foldermap.ProjectSection(p.Path)
		if name == "" {
			continue
		}
		if counts[name] == nil {
			counts[name] = map[string]int{}
		}
		label := foldermap.FormatName(p.Marker)
		if label == "" {
			label = foldermap.ShapeNoun(p.Shape, 2)
		}
		counts[name][label]++
	}
	if len(counts) == 0 {
		return rec.Facts
	}
	plan.BranchLook = branchHandle(rec.OwnerUserID, rec.WorkspaceID, root.Witness, m.Updated.UTC().String(), fmt.Sprint(m.Walks))
	plan.Selected = append(plan.Selected, plan.BranchLook)
	requested := map[string]bool{}
	explicit := false
	for _, id := range selected {
		if strings.HasPrefix(id, "branch:") {
			requested[id] = true
			explicit = true
		}
	}
	if explicit && !requested[plan.BranchLook] {
		plan.branchStale = true
	}
	delete(requested, plan.BranchLook)
	partial := map[string]bool{}
	for _, f := range m.Folders {
		if f.State == foldermap.StateUnreadable || f.State == foldermap.StateUnopened || f.State == foldermap.StateLarge {
			name, _, _ := strings.Cut(f.Path, "/")
			partial[name] = true
		}
	}
	for _, f := range m.Folders {
		if f.Depth() != 1 || f.IsProject() {
			continue
		}
		b := HomeBranch{Name: f.Path, Partial: partial[f.Path], witness: f.Witness}
		if formats := counts[f.Path]; len(formats) > 0 {
			var summaries []string
			for format, n := range formats {
				summaries = append(summaries, fmt.Sprintf("%d %s", n, format))
			}
			sort.Strings(summaries)
			b.Summary = strings.Join(summaries, ", ")
			if f.Witness != "" {
				b.ID = branchHandle(plan.BranchLook, f.Path, f.Witness)
				b.Included = !explicit || requested[b.ID]
				delete(requested, b.ID)
				if b.Included {
					plan.Selected = append(plan.Selected, b.ID)
				}
			} else {
				b.Partial = true
			}
		} else if b.Partial {
			b.Summary = "Not fully inspected"
		} else if f.State == foldermap.StateWalked && f.Empty {
			b.Summary = "Empty in the look"
		} else {
			continue
		}
		plan.Branches = append(plan.Branches, b)
	}
	if len(requested) > 0 {
		plan.branchStale = true
	}
	return plan.ListingFacts(rec.Facts)
}

// ListingFacts keeps direct members (always reached by the shallow root scan).
func (p Plan) ListingFacts(f foldermap.Facts) foldermap.Facts {
	if p.BranchLook == "" {
		return f
	}
	included := map[string]bool{}
	for _, b := range p.Branches {
		included[b.Name] = b.Included
	}
	members := f.Members
	f.Members = nil
	for _, m := range members {
		s, _ := foldermap.ProjectSection(m.Path)
		if m.Depth < 2 || (s != "" && included[s]) {
			f.Members = append(f.Members, m)
		}
	}
	if f.Portfolio != nil {
		copy := *f.Portfolio
		copy.Count = 0
		for _, m := range f.Members {
			if m.Shape == copy.Shape {
				copy.Count++
			}
		}
		f.Portfolio = &copy
	}
	return f
}

// CheckBranchWitnesses only stats the anchored root and the selected first-level
// directories from the saved look. A replacement (including a symlink) requires a new look.
func (p Plan) CheckBranchWitnesses(root string, m *foldermap.Map) error {
	if p.BranchLook == "" {
		return nil
	}
	if m == nil {
		return personalassistant.ErrFolderPlanChanged
	}
	matches := func(path, want string) bool {
		info, err := os.Lstat(path) // #nosec G703 -- host anchor and first-level names from its saved map, not request paths
		return err == nil && want != "" && diridentity.Of(info) == want
	}
	r, ok := m.Folder("")
	if !ok || !matches(root, r.Witness) {
		return personalassistant.ErrFolderPlanChanged
	}
	for _, b := range p.Branches {
		if b.Included && (filepath.Base(b.Name) != b.Name || b.Name == ".." || !matches(filepath.Join(root, b.Name), b.witness)) {
			return personalassistant.ErrFolderPlanChanged
		}
	}
	return nil
}
