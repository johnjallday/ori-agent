package foldermap

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// Stage names what a folder's workspace becomes on Set it up. Stages describe
// what the workspace is, never how deep the folder goes (PRD § H).
type Stage string

const (
	// StageFolder: nothing recognized; the workspace stays a folder with its
	// agent.
	StageFolder Stage = "folder"
	// StageTools: a project at the fed folder itself, or loose files to tidy;
	// Set it up installs or connects tools for this workspace.
	StageTools Stage = "tools"
	// StageGroup: projects below the fed folder and no Home applies; the
	// workspace is promoted to a group with one member per project.
	StageGroup Stage = "group"
	// StageHome: a portfolio of a shape with a Home provider; the folder joins
	// (or creates) that Home as a root.
	StageHome Stage = "home"
)

// StageFor decides the stage from the facts alone. Home wins over Group
// because a portfolio's songs belong on the Home's shelf, not in a group;
// Group wins over Tools only when the fed folder is not itself a project,
// which the closed-project rule already guarantees (a project's insides are
// never walked).
//
// A group needs at least GroupMinMembers projects; one project below the
// fed folder makes the workspace that project's (the Tools stage).
func StageFor(f Facts) Stage {
	switch {
	case f.Portfolio != nil:
		return StageHome
	case len(f.Members) >= GroupMinMembers:
		return StageGroup
	case f.RootProject != nil, len(f.Members) == 1, f.Dump:
		return StageTools
	}
	return StageFolder
}

// SoleProject is the one project the Tools stage is about: the project at
// the fed folder, or the only project below it.
func (f Facts) SoleProject() *Project {
	if f.RootProject != nil {
		return f.RootProject
	}
	if len(f.Members) == 1 {
		p := f.Members[0]
		return &p
	}
	return nil
}

// ShapesTie reports whether the two largest shapes have the same number of
// projects, which leaves the stage's shape for the user to settle.
func (f Facts) ShapesTie() bool {
	totals := f.ShapeTotals()
	return len(totals) >= 2 && totals[0].Count == totals[1].Count
}

// shapeNouns is how the facts line counts projects of a shape.
var shapeNouns = map[folderdigest.Shape][2]string{
	folderdigest.ShapeAudio:      {"song", "songs"},
	folderdigest.ShapeCode:       {"software project", "software projects"},
	folderdigest.ShapeManuscript: {"manuscript", "manuscripts"},
	folderdigest.ShapeCorpus:     {"research collection", "research collections"},
	folderdigest.ShapeNotes:      {"notes vault", "notes vaults"},
}

// ShapeNoun is the plain word for count projects of a shape.
func ShapeNoun(shape folderdigest.Shape, count int) string {
	nouns, ok := shapeNouns[shape]
	if !ok {
		nouns = [2]string{"project", "projects"}
	}
	if count == 1 {
		return nouns[0]
	}
	return nouns[1]
}

// FormatName is the application a project format belongs to ("Logic Pro"),
// from the host's tool table; "" when the table has none.
func FormatName(marker string) string {
	if !strings.HasPrefix(marker, "*.") {
		return ""
	}
	if tool, ok := folderdigest.ToolForExtension(filepath.Ext(marker)); ok {
		return tool.ToolName
	}
	return ""
}

// FactsLine is the host's one-line summary: "7 <App> songs · 1 Logic Pro
// song · 2 folders with songs · 1 empty folder". It names no path and no
// file, and reads the same on every run over the same map.
func FactsLine(f Facts) string {
	markerFor := make(map[ShapeCount]string)
	for _, p := range append(f.Members, rootProjects(f)...) {
		key := ShapeCount{Shape: p.Shape, Format: p.Format}
		if _, ok := markerFor[key]; !ok {
			markerFor[key] = p.Marker
		}
	}
	var parts []string
	for _, s := range f.Shapes {
		noun := ShapeNoun(s.Shape, s.Count)
		if name := FormatName(markerFor[ShapeCount{Shape: s.Shape, Format: s.Format}]); name != "" && s.Format != "" {
			noun = name + " " + noun
		}
		parts = append(parts, fmt.Sprintf("%d %s", s.Count, noun))
	}
	if n := len(f.ProjectAreas); n > 0 {
		noun := "projects"
		if totals := f.ShapeTotals(); len(totals) == 1 {
			noun = ShapeNoun(totals[0].Shape, 2)
		}
		parts = append(parts, fmt.Sprintf("%d %s with %s", n, plural(n, "folder", "folders"), noun))
	}
	if f.Dump {
		parts = append(parts, fmt.Sprintf("%d loose files of %d kinds", f.RootLooseFiles, f.RootLooseKinds))
	}
	if n := len(f.EmptyFolders); n > 0 {
		parts = append(parts, fmt.Sprintf("%d empty %s", n, plural(n, "folder", "folders")))
	}
	if n := len(f.Unopened); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s not opened yet", n, plural(n, "folder", "folders")))
	}
	if n := len(f.Large); n > 0 {
		parts = append(parts, fmt.Sprintf("%d large %s not opened", n, plural(n, "folder", "folders")))
	}
	if len(parts) == 0 {
		if f.Folders <= 1 && f.RootLooseFiles == 0 {
			return "This folder is empty"
		}
		return fmt.Sprintf("Nothing Ori recognizes yet · %d %s · %d %s here", f.Folders, plural(f.Folders, "folder", "folders"), f.RootLooseFiles, plural(f.RootLooseFiles, "file", "files"))
	}
	return strings.Join(parts, " · ")
}

func rootProjects(f Facts) []Project {
	if f.RootProject == nil {
		return nil
	}
	return []Project{*f.RootProject}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
