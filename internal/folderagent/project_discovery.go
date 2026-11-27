package folderagent

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ProjectDiscovery is constructed by the verified Documents Manager only. All reads are
// bounded metadata listings, including when a caller rechecks a previous choice.
type ProjectDiscovery struct{ Root, Witness, Binding string }
type DiscoveryFolder struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}
type CourseSuggestion struct {
	Folder                 string `json:"folder"`
	Ref                    string `json:"ref"`
	Syllabus               string `json:"syllabus"`
	Structure              string `json:"structure"`
	Identity               string `json:"-"`
	parentWitness, witness string
}
type ProjectLook struct {
	Folders     []DiscoveryFolder  `json:"folders,omitempty"`
	Suggestions []CourseSuggestion `json:"suggestions,omitempty"`
	Partial     bool               `json:"partial"`
}

func projectEntry(e workspace.DirectoryEntry) bool {
	return e.Version.Identity != "" && !e.Version.Placeholder && len(e.Name) <= 100 &&
		projectLabel.MatchString(e.Name) && !strings.HasSuffix(e.Name, " ") &&
		!folderdigest.IsSkippedFolder(e.Name) && !sensitive.ContainsSecretLikeText(e.Name) &&
		!strings.EqualFold(e.Name, "My Notes") && !strings.EqualFold(e.Name, "Ori Workspaces")
}
func (d ProjectDiscovery) folderRef(rel, id string) string {
	return documentsRef("project-folder-v1", d.Binding, d.Witness, rel, id)
}
func (d ProjectDiscovery) Folders() (ProjectLook, error) {
	listing, err := workspace.ListWitnessedPage(d.Root, "", d.Witness, d.Witness)
	if err != nil {
		return ProjectLook{}, err
	}
	out := ProjectLook{Partial: listing.Truncated || listing.Incomplete, Folders: []DiscoveryFolder{{"Documents", d.folderRef("", d.Witness)}}}
	for _, e := range listing.Entries {
		if e.Kind == "folder" && projectEntry(e) {
			out.Folders = append(out.Folders, DiscoveryFolder{"Documents/" + e.Name, d.folderRef(e.Name, e.Version.Identity)})
		}
	}
	return out, nil
}

// Resolve the chosen parent by its live witnessed entry, never a client path.
func (d ProjectDiscovery) parent(ref string) (string, string, error) {
	if ref == d.folderRef("", d.Witness) {
		return "", d.Witness, nil
	}
	l, err := workspace.ListWitnessedPage(d.Root, "", d.Witness, d.Witness)
	if err != nil {
		return "", "", err
	}
	for _, e := range l.Entries {
		if e.Kind == "folder" && projectEntry(e) && ref == d.folderRef(e.Name, e.Version.Identity) {
			return e.Name, e.Version.Identity, nil
		}
	}
	return "", "", ErrWrongState
}

func (d ProjectDiscovery) Find(ctx context.Context, ref string, ignored map[string]bool) (ProjectLook, error) {
	parent, id, err := d.parent(ref)
	if err != nil {
		return ProjectLook{}, err
	}
	listing, err := workspace.ListWitnessedPage(d.Root, parent, d.Witness, id)
	if err != nil {
		return ProjectLook{}, err
	}
	out := ProjectLook{Partial: listing.Truncated || listing.Incomplete}
	probes := 0
	for _, e := range listing.Entries {
		if err := ctx.Err(); err != nil {
			return ProjectLook{}, err
		}
		if e.Kind != "folder" || !projectEntry(e) {
			continue
		}
		// An independently re-verified Documents workspace still opens the same
		// physical course, but its review token is bound to this manager's receipt.
		identity := documentsRef("course-folder-v1", d.Root, d.Witness, id, e.RelativePath, e.Version.Identity)
		if ignored[identity] {
			continue
		}
		if probes == 12 {
			out.Partial = true
			break
		}
		probes++
		children, err := workspace.ListWitnessedPage(d.Root, e.RelativePath, d.Witness, e.Version.Identity)
		if err != nil {
			out.Partial = true
			continue
		}
		out.Partial = out.Partial || children.Truncated || children.Incomplete
		c := CourseSuggestion{Folder: e.RelativePath, Identity: identity, parentWitness: id, witness: e.Version.Identity}
		for _, child := range children.Entries {
			if !projectEntry(child) {
				continue
			}
			name := strings.ToLower(child.Name)
			ext := filepath.Ext(name)
			if child.Kind == "file" && c.Syllabus == "" && (ext == ".pdf" || ext == ".txt" || ext == ".md" || ext == ".docx") && strings.TrimSuffix(name, ext) == "syllabus" {
				c.Syllabus = child.Name
			}
			if child.Kind == "folder" && c.Structure == "" {
				switch name {
				case "labs", "assignments", "lectures", "coursework":
					c.Structure = child.Name
				}
			}
		}
		if c.Syllabus == "" || c.Structure == "" {
			continue
		}
		c.Ref = documentsRef(ref, identity, c.Syllabus, c.Structure)
		out.Suggestions = append(out.Suggestions, c)
		if len(out.Suggestions) == 2 {
			break
		}
	}
	// In particular, don't let a renamed parent inherit a former child's evidence.
	if parent != "" {
		chain, e := workspace.WitnessedFolderChain(d.Root, parent, d.Witness)
		if e != nil || len(chain) != 1 || chain[0].Identity != id {
			return ProjectLook{}, ErrWrongState
		}
	}
	return out, nil
}

func (c CourseSuggestion) Name() string { return path.Base(c.Folder) }

// Current checks only the reviewed directory chain, not its siblings or files.
func (d ProjectDiscovery) Current(c CourseSuggestion) bool {
	chain, err := workspace.WitnessedFolderChain(d.Root, c.Folder, d.Witness)
	if err != nil || len(chain) == 0 || chain[len(chain)-1].Identity != c.witness {
		return false
	}
	return len(chain) == 1 && c.parentWitness == d.Witness || len(chain) == 2 && chain[0].Identity == c.parentWitness
}
