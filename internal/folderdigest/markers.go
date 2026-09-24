package folderdigest

import (
	"path/filepath"
	"strings"
)

// MarkerKind says how a marker row is matched against a directory entry.
type MarkerKind string

const (
	// MarkerFile matches a regular file with exactly this name.
	MarkerFile MarkerKind = "file"
	// MarkerDir matches a directory with exactly this name.
	MarkerDir MarkerKind = "dir"
	// MarkerGlob matches any entry, file or bundle directory, whose name fits
	// the pattern (filepath.Match syntax).
	MarkerGlob MarkerKind = "glob"
)

// Shape is what a marker says the folder is for. FR28 maps a shape to the
// blueprint a workspace created from the folder starts with.
type Shape string

const (
	ShapeCode       Shape = "code"
	ShapeAudio      Shape = "audio"
	ShapeManuscript Shape = "manuscript"
	ShapeCorpus     Shape = "corpus"
	ShapeNotes      Shape = "notes"
)

// Marker is one row of the host-owned project marker table (FR14). A
// candidate "has a marker" when an entry matching the row exists directly
// inside it.
type Marker struct {
	Name  string
	Kind  MarkerKind
	Shape Shape
	// Label is the plain-words name used in reason lines and evidence
	// summaries ("marker: git repository").
	Label string
}

// Markers is the marker table. Order is precedence: when a folder carries
// several markers the first matching row decides its shape, so the more
// specific shapes (a REAPER session, a manuscript, a corpus, a notes vault)
// come before the generic code markers a thesis or a vault may also carry
// (a .git directory, for instance). Adding a marker is a table edit.
var Markers = []Marker{
	// Audio
	{Name: "*.rpp", Kind: MarkerGlob, Shape: ShapeAudio, Label: "REAPER session"},
	{Name: "*.logicx", Kind: MarkerGlob, Shape: ShapeAudio, Label: "Logic Pro project"},
	{Name: "*.als", Kind: MarkerGlob, Shape: ShapeAudio, Label: "Ableton Live set"},
	// Manuscript
	{Name: "main.tex", Kind: MarkerFile, Shape: ShapeManuscript, Label: "LaTeX manuscript"},
	{Name: "chapters", Kind: MarkerDir, Shape: ShapeManuscript, Label: "chapters folder"},
	{Name: "outline.md", Kind: MarkerFile, Shape: ShapeManuscript, Label: "outline"},
	// Corpus
	{Name: "*.bib", Kind: MarkerGlob, Shape: ShapeCorpus, Label: "bibliography"},
	// Notes
	{Name: ".obsidian", Kind: MarkerDir, Shape: ShapeNotes, Label: "Obsidian vault"},
	// Code
	{Name: ".git", Kind: MarkerDir, Shape: ShapeCode, Label: "git repository"},
	{Name: "package.json", Kind: MarkerFile, Shape: ShapeCode, Label: "Node.js package"},
	{Name: "go.mod", Kind: MarkerFile, Shape: ShapeCode, Label: "Go module"},
	{Name: "Cargo.toml", Kind: MarkerFile, Shape: ShapeCode, Label: "Rust crate"},
	{Name: "pyproject.toml", Kind: MarkerFile, Shape: ShapeCode, Label: "Python project"},
	{Name: "requirements.txt", Kind: MarkerFile, Shape: ShapeCode, Label: "Python project"},
	{Name: "*.xcodeproj", Kind: MarkerGlob, Shape: ShapeCode, Label: "Xcode project"},
}

// matches reports whether one directory entry satisfies the marker row.
func (m Marker) matches(name string, isDir bool) bool {
	switch m.Kind {
	case MarkerFile:
		return !isDir && name == m.Name
	case MarkerDir:
		return isDir && name == m.Name
	case MarkerGlob:
		ok, err := filepath.Match(m.Name, name)
		return err == nil && ok
	}
	return false
}

// MatchMarker returns the highest-precedence marker row that one entry
// satisfies, if any. Callers keep the first hit across a folder's entries in
// table order, which markerRank makes cheap.
func MatchMarker(name string, isDir bool) (Marker, bool) {
	for _, m := range Markers {
		if m.matches(name, isDir) {
			return m, true
		}
	}
	return Marker{}, false
}

// markerRank is the row index of a marker, used to keep the most specific
// marker when a folder carries several.
func markerRank(m Marker) int {
	for i, row := range Markers {
		if row.Name == m.Name && row.Kind == m.Kind {
			return i
		}
	}
	return len(Markers)
}

// ToolMatch says which candidate attribute a tool row is compared against.
type ToolMatch string

const (
	// ToolByExtension compares the row's value against a dominant or present
	// file extension (".rpp").
	ToolByExtension ToolMatch = "extension"
	// ToolByMarker compares the row's value against a marker name ("go.mod").
	ToolByMarker ToolMatch = "marker"
)

// Tool is one row of the shared host-owned tool table (FR36). The saved-app
// producer and the folder producer both read it, so a tool is added once.
type Tool struct {
	Match ToolMatch
	Value string
	// ToolID is the stable identity used to dedupe proposals across producers.
	ToolID string
	// ToolName is the display name used in the hypothesis text.
	ToolName string
	// HypothesisText is the candidate fact proposed to the dossier.
	HypothesisText string
}

// Tools is the tool table. Values are lower-case; extensions carry the dot.
var Tools = []Tool{
	{Match: ToolByExtension, Value: ".rpp", ToolID: "reaper", ToolName: "REAPER", HypothesisText: "REAPER may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".logicx", ToolID: "logic-pro", ToolName: "Logic Pro", HypothesisText: "Logic Pro may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".als", ToolID: "ableton-live", ToolName: "Ableton Live", HypothesisText: "Ableton Live may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".tex", ToolID: "latex", ToolName: "LaTeX", HypothesisText: "LaTeX may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".ipynb", ToolID: "jupyter", ToolName: "Jupyter", HypothesisText: "Jupyter may be one of the tools you use."},
	{Match: ToolByMarker, Value: "go.mod", ToolID: "go", ToolName: "Go", HypothesisText: "Go may be one of the tools you use."},
	{Match: ToolByMarker, Value: "package.json", ToolID: "nodejs", ToolName: "Node.js", HypothesisText: "Node.js may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".fig", ToolID: "figma", ToolName: "Figma", HypothesisText: "Figma may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".psd", ToolID: "photoshop", ToolName: "Photoshop", HypothesisText: "Photoshop may be one of the tools you use."},
	{Match: ToolByExtension, Value: ".bib", ToolID: "reference-manager", ToolName: "a reference manager", HypothesisText: "A reference manager may be one of the tools you use."},
}

// ToolForExtension returns the tool row for a file extension (".rpp" or
// "rpp"), if the table has one.
func ToolForExtension(ext string) (Tool, bool) {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	for _, t := range Tools {
		if t.Match == ToolByExtension && t.Value == ext {
			return t, true
		}
	}
	return Tool{}, false
}

// ToolForMarker returns the tool row for a marker name ("go.mod"), if the
// table has one. Glob markers ("*.rpp") are looked up by their extension.
func ToolForMarker(m Marker) (Tool, bool) {
	if m.Kind == MarkerGlob {
		return ToolForExtension(filepath.Ext(m.Name))
	}
	name := strings.ToLower(m.Name)
	for _, t := range Tools {
		if t.Match == ToolByMarker && t.Value == name {
			return t, true
		}
	}
	return Tool{}, false
}

// ToolByID returns the tool row with the given identity.
func ToolByID(id string) (Tool, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, t := range Tools {
		if t.ToolID == id {
			return t, true
		}
	}
	return Tool{}, false
}
