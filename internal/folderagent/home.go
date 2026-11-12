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

// HomeRolesLine is the one hire line of a plan that cannot name the Home's
// roles before the Home exists (PRD FR5); the run names each role as it hires.
const HomeRolesLine = "Fills the Home's roles"

// HomeHiresOutcome is the outcome item for HomeRolesLine: the number of
// agents is not known until the Home exists, so it is not stated.
const HomeHiresOutcome = "Hires the Home's agents"

// createsOutcome is the outcome item for the line that creates the Home. The
// outcome items are kept short so the line fits one row of the card: the run
// line itself keeps its full words under "Show all".
func createsOutcome(homeName string) string { return "Creates " + homeName }

// hiresOutcome is the outcome item shared by the n hire lines of a Home whose
// missing roles are known: "Hires 3 agents".
func hiresOutcome(n int) string {
	return fmt.Sprintf("Hires %d %s", n, plural(n, "agent", "agents"))
}

// homeLines are the Home stage's plan lines (PRD FR1, FR37, FR38), one per
// action the run takes: the provider's install when it is not ready, the
// Home's creation when there is none, a hire per role the Home still lacks,
// then the folder, its listing and the placement. A record looked at before
// the host reported Home facts keeps the generic creation line.
func homeLines(rec Record, home folderlook.Candidate) []personalassistant.FolderPlanLine {
	name := homeName(home)
	var lines []personalassistant.FolderPlanLine
	facts := home.Home
	if facts == nil {
		if home.Action != folderlook.ActionAlreadyHere {
			lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanHome, Name: "Creates your " + name,
				Outcome: createsOutcome(name),
				Detail:  "Its provider is checked against the reviewed release, and its roles are filled."})
		}
	} else {
		if !facts.ProviderReady {
			provider := facts.ProviderName
			if provider == "" {
				provider = "the " + name + "'s plugin"
			}
			// A detail: the install block lists the plugin once, for the button
			// to confirm, so the outcome line does not repeat it.
			lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanProvider, Name: "Installs " + provider,
				Detail: "The reviewed release, checked before it is installed."})
		}
		if !facts.HomeExists {
			lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanHome, Name: "Creates your " + name,
				Outcome: createsOutcome(name)})
		}
		if facts.RolesKnown {
			// One outcome item for every hire, whatever the number of roles.
			hires := hiresOutcome(len(facts.MissingRoles))
			for _, role := range facts.MissingRoles {
				lines = append(lines, personalassistant.FolderPlanLine{Kind: FolderPlanHire, Name: "Hires " + role, Outcome: hires})
			}
		} else {
			lines = append(lines, personalassistant.FolderPlanLine{Kind: FolderPlanHire, Name: HomeRolesLine, Outcome: HomeHiresOutcome,
				Detail: "Each role is named as it is hired."})
		}
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
		Name:    fmt.Sprintf("Lists %d %s on its shelf", songs, noun),
		Outcome: fmt.Sprintf("Lists %d %s", songs, noun)})
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

// homeName is the Home's name as the plan words it.
func homeName(home folderlook.Candidate) string {
	if home.Name == "" {
		return "Home"
	}
	return home.Name
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
	// FolderPlanHire is a hire line: "Hires <role>" or HomeRolesLine.
	FolderPlanHire = "hire"
)
