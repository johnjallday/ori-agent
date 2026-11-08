package folderlook

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// Bounds of the reference material.
const (
	maxReferenceFolders  = 200
	maxReferenceProjects = 60
	MaxAskFolders        = 5
	HintMaxRunes         = 200
)

// ReferenceInput is the host's view of the folder for one turn.
type ReferenceInput struct {
	FolderName string
	Brief      string
	Map        *foldermap.Map
	Facts      foldermap.Facts
	// StageLine is Ori's default plan, in its words; Steps are the steps the
	// agent may name in its own plan.
	StageLine  string
	Steps      []Step
	Candidates []Candidate
	AskAllowed bool
	// Hint is the owner's typed answer to the previous turn's question.
	Hint string
}

// BuildReference lays the map, the facts and the candidates out as one
// delimited block (PRD FR7). Folder names are data inside the block; the
// boundary is random so a folder name cannot close it.
func BuildReference(in ReferenceInput) string {
	boundary := "FOLDER-MAP-" + randomHex(6)
	clean := func(text string) string {
		return strings.ReplaceAll(oneLine(text), boundary, "")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<<<%s\n", boundary)
	fmt.Fprintf(&b, "Fed folder: %s\n", clean(in.FolderName))
	if in.Brief != "" {
		fmt.Fprintf(&b, "The owner's brief: %s\n", clean(in.Brief))
	}
	if in.Hint != "" {
		fmt.Fprintf(&b, "The owner answered your question: %s\n", clean(in.Hint))
	}
	fmt.Fprintf(&b, "Facts Ori found: %s\n", clean(foldermap.FactsLine(in.Facts)))
	if len(in.Steps) == 0 {
		b.WriteString("Steps Ori can run here: none. Say so under Plan and name no handle.\n")
	} else {
		b.WriteString("Steps Ori can run here (handle · what it does · why it is available). Name a step in your plan by its handle in square brackets; Ori runs only these:\n")
		for _, s := range in.Steps {
			notes := ""
			if s.Default {
				notes += " · in Ori's default plan"
			}
			if s.Excludes != "" {
				notes += " · cannot run together with [" + s.Excludes + "]"
			}
			fmt.Fprintf(&b, "- [%s] · %s · %s%s\n", s.Handle, clean(s.Title), clean(s.Why), notes)
		}
	}
	if in.StageLine != "" {
		fmt.Fprintf(&b, "Ori's default plan, used if you name no step: %s\n", clean(in.StageLine))
	}
	if in.Facts.Stopped != "" {
		fmt.Fprintf(&b, "The walk stopped early (%s); folders marked unopened were not listed.\n", in.Facts.Stopped)
	}
	projects := append(projectsOf(in.Facts), in.Facts.Members...)
	if len(projects) > 0 {
		b.WriteString("\nProjects (folder · kind):\n")
		for i, p := range projects {
			if i == maxReferenceProjects {
				fmt.Fprintf(&b, "… and %d more\n", len(projects)-i)
				break
			}
			where := p.Path
			if where == "" {
				where = "(the fed folder)"
			}
			fmt.Fprintf(&b, "- %s · %s %s\n", clean(where), clean(foldermap.FormatName(p.Marker)), foldermap.ShapeNoun(p.Shape, 1))
		}
	}
	if in.Map != nil {
		b.WriteString("\nFolders (path · state · files · subfolders):\n")
		folders := referenceFolders(in.Map)
		for _, f := range folders {
			where := f.Path
			if where == "" {
				where = `""`
			}
			fmt.Fprintf(&b, "- %s · %s · %d · %d\n", clean(where), f.State, f.Files, f.Subfolders)
		}
		if n := len(in.Map.Folders) - len(folders); n > 0 {
			fmt.Fprintf(&b, "… and %d more folders not shown\n", n)
		}
	}
	b.WriteString("\nTool candidates (choose only these ids):\n")
	if len(in.Candidates) == 0 {
		b.WriteString("- none\n")
	}
	for _, c := range in.Candidates {
		required := ""
		if c.Required {
			required = " (required, already in the plan)"
		}
		fmt.Fprintf(&b, "- %s · %s · %s%s\n", c.ID, clean(c.Name), clean(c.Detail), required)
	}
	switch {
	case in.AskAllowed && !in.Facts.Recognized() && (len(in.Facts.Unopened) > 0 || len(in.Facts.Large) > 0):
		b.WriteString("\nOri recognized nothing yet and left folders unopened. Open the most promising unopened folders with folder_look first. ")
		b.WriteString("If you still cannot tell what this folder is for, ask the owner one question: which of the unopened folders to look into first.\n")
	case in.AskAllowed && in.Facts.ShapesTie():
		b.WriteString("\nTwo kinds of project tie. If you cannot tell which matters most here, ask the owner one question about it.\n")
	case in.AskAllowed:
		b.WriteString("\nYou may ask the owner one question.\n")
	default:
		b.WriteString("\nThe facts are clear: do not ask a question.\n")
	}
	if in.AskAllowed {
		if picks := AskFolders(in.Facts); len(picks) > 0 {
			names := make([]string, len(picks))
			for i, pick := range picks {
				names[i] = clean(pick.Name)
			}
			fmt.Fprintf(&b, "If you ask, the owner answers by picking one of these folders, or by typing one line: %s. Name only these folders in your question.\n", strings.Join(names, ", "))
		}
	}
	fmt.Fprintf(&b, "%s>>>\n", boundary)
	return b.String()
}

func projectsOf(f foldermap.Facts) []foldermap.Project {
	if f.RootProject == nil {
		return nil
	}
	return []foldermap.Project{*f.RootProject}
}

// referenceFolders picks at most maxReferenceFolders folders: the shallow
// ones first, then unopened and large ones, then the rest, in walk order.
func referenceFolders(m *foldermap.Map) []foldermap.Folder {
	if len(m.Folders) <= maxReferenceFolders {
		return m.Folders
	}
	ranked := append([]foldermap.Folder(nil), m.Folders...)
	rank := func(f foldermap.Folder) int {
		switch {
		case f.Depth() <= 1:
			return 0
		case f.State == foldermap.StateUnopened || f.State == foldermap.StateLarge:
			return 1
		case f.IsProject():
			return 2
		}
		return 3
	}
	sort.SliceStable(ranked, func(i, j int) bool { return rank(ranked[i]) < rank(ranked[j]) })
	return ranked[:maxReferenceFolders]
}

// AskAllowed is the host's gate on the one question (PRD FR26): two shapes
// tie for the stage, or nothing was recognized (including a walk that ended
// with folders unopened and nothing recognized).
func AskAllowed(f foldermap.Facts) bool {
	return f.ShapesTie() || !f.Recognized()
}

// AskFolder is one pickable answer: a folder name shown, and the map's
// reference the host resolves (never shown).
type AskFolder struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

// AskFolders are up to five pickable answers: the top-level folders that
// still hold unopened or large folders (a top-level folder that is itself
// unopened answers for itself). Look further on one opens what is still
// unopened inside it first.
func AskFolders(f foldermap.Facts) []AskFolder {
	var out []AskFolder
	seen := make(map[string]bool)
	for _, rel := range append(append([]string(nil), f.Unopened...), f.Large...) {
		area := strings.SplitN(rel, "/", 2)[0]
		if area == "" || seen[area] {
			continue
		}
		seen[area] = true
		out = append(out, AskFolder{Name: path.Base(area), Ref: area})
		if len(out) == MaxAskFolders {
			break
		}
	}
	return out
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
