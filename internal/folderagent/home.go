package folderagent

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Bounds of the Home stage's scoped scans (PRD FR37). The library's own scan
// budgets still apply to each scan.
const (
	// MaxScopedScans bounds the scoped scans one Set it up commits.
	MaxScopedScans = 48
	// HomeGoal is the folder workspace's goal once it joined a Home (FR39).
	HomeGoal = "Watch this folder for the Home"
)

// LibraryScope is one scope the Home's library issued for the root: its id
// and its folder relative to the root.
type LibraryScope struct {
	ID             string
	RelativeFolder string
}

// ProjectParents are the folders, relative to the fed folder, whose scoped
// listing reaches the projects the map found two or more folders down. A
// root listing already reaches projects one folder down.
func ProjectParents(facts foldermap.Facts) []string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range facts.Members {
		if p.Depth < 2 {
			continue
		}
		parent := path.Dir(p.Path)
		if !seen[parent] {
			seen[parent] = true
			out = append(out, parent)
		}
	}
	sort.Strings(out)
	return out
}

// NextScopes picks the library scopes to scan next: for every folder still
// needed, the scope for that folder itself, or else the deepest issued scope
// above it (scanning it issues deeper scopes). Scanned scopes are skipped.
func NextScopes(needed []string, scopes []LibraryScope, scanned map[string]bool) []LibraryScope {
	byFolder := make(map[string]LibraryScope, len(scopes))
	for _, s := range scopes {
		folder := strings.Trim(path.Clean(strings.ReplaceAll(s.RelativeFolder, "\\", "/")), "/")
		if folder == "." || folder == "" || s.ID == "" {
			continue
		}
		byFolder[folder] = LibraryScope{ID: s.ID, RelativeFolder: folder}
	}
	picked := make(map[string]bool)
	var out []LibraryScope
	for _, want := range needed {
		for at := want; at != "." && at != ""; at = path.Dir(at) {
			s, ok := byFolder[at]
			if !ok {
				continue
			}
			if !scanned[s.RelativeFolder] && !picked[s.RelativeFolder] {
				picked[s.RelativeFolder] = true
				out = append(out, s)
			}
			break
		}
	}
	return out
}

// Unreached are the needed folders no scanned scope covered exactly.
func Unreached(needed []string, scanned map[string]bool) []string {
	var out []string
	for _, folder := range needed {
		if !scanned[folder] {
			out = append(out, folder)
		}
	}
	return out
}

// HomeReceiptLine says how many songs the shelf lists under the folder and
// how many folders holding songs no listing reached (PRD FR37).
func HomeReceiptLine(listed, unreached int, facts foldermap.Facts) string {
	shape := folderdigest.Shape("")
	if facts.Portfolio != nil {
		shape = facts.Portfolio.Shape
	}
	line := fmt.Sprintf("%d %s listed on the shelf", listed, foldermap.ShapeNoun(shape, listed))
	if unreached > 0 {
		line += fmt.Sprintf(" · %d %s not reached yet", unreached, plural(unreached, "folder", "folders"))
	}
	return line
}

// homeLines are the Home stage's plan lines (PRD FR37, FR38).
func homeLines(rec Record, home folderlook.Candidate) []personalassistant.FolderPlanLine {
	name := home.Name
	if name == "" {
		name = "Home"
	}
	var lines []personalassistant.FolderPlanLine
	if home.Action != folderlook.ActionAlreadyHere {
		lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanHome, Name: "Creates your " + name,
			Detail: "Its provider is checked against the reviewed release, and its roles are filled."})
	}
	lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanLibrary,
		Name: "Adds " + rec.FolderName + " to your " + name})
	songs := 0
	if rec.Facts.Portfolio != nil {
		songs = rec.Facts.Portfolio.Count
	}
	noun := foldermap.ShapeNoun("", songs)
	if rec.Facts.Portfolio != nil {
		noun = foldermap.ShapeNoun(rec.Facts.Portfolio.Shape, songs)
	}
	lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanSongs,
		Name: fmt.Sprintf("Lists %d %s on its shelf", songs, noun)})
	if n := len(ProjectParents(rec.Facts)); n > 0 {
		lines = append(lines, personalassistant.FolderPlanLine{Kind: FolderPlanScopes,
			Name:   fmt.Sprintf("Looks into %d %s for %s", n, plural(n, "folder", "folders"), foldermap.ShapeNoun(folderdigest.Shape(shapeOf(rec)), 2)),
			Detail: "Only the folders the walk found holding them, within the library's own scan limits."})
	}
	lines = append(lines, personalassistant.FolderPlanLine{Kind: FolderPlanPlace,
		Name:   "Places " + rec.WorkspaceName + " inside your " + name,
		Detail: rec.AgentName + " stays its agent; the Home's Library Manager keeps the shelf up to date."})
	return lines
}

func shapeOf(rec Record) string {
	if rec.Facts.Portfolio != nil {
		return string(rec.Facts.Portfolio.Shape)
	}
	return ""
}

// Plan line kinds of the Home stage.
const (
	FolderPlanScopes = "scopes"
	FolderPlanPlace  = "place"
)
