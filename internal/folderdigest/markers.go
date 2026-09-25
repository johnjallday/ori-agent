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

// The Markers table itself lives in tables.go, the package's data-only file.

// matches reports whether one directory entry satisfies the marker row.
func (m Marker) matches(name string, isDir bool) bool {
	switch m.Kind {
	case MarkerFile:
		return !isDir && name == m.Name
	case MarkerDir:
		return isDir && name == m.Name
	case MarkerGlob:
		// Project extension globs match case-insensitively,
		// while exact-name manifests above keep their original case.
		ok, err := filepath.Match(strings.ToLower(m.Name), strings.ToLower(name))
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
	// file extension (".tex").
	ToolByExtension ToolMatch = "extension"
	// ToolByMarker compares the row's value against a marker name ("go.mod").
	ToolByMarker ToolMatch = "marker"
	// ToolByApp has no file signal: the row is reached only through an
	// installed application's name.
	ToolByApp ToolMatch = "app"
)

// Tool is one row of the shared host-owned tool table (FR36). The saved-app
// producer and the folder producer both read it, so a tool is added once.
type Tool struct {
	Match ToolMatch
	Value string
	// AppNames are the application names the saved-app observation reports
	// for this tool, compared case-insensitively.
	AppNames []string
	// ToolID is the stable identity used to dedupe proposals across producers.
	ToolID string
	// ToolName is the display name used in the hypothesis text.
	ToolName string
	// HypothesisText is the candidate fact proposed to the dossier.
	HypothesisText string
}

// ProjectCapabilityFor returns an offer only when the project's marker (or
// dominant extension when unmarked) matches an eligible tool in that row.
// Shape alone is insufficient: sharing an audio shape does not make every
// tool eligible for the same integration. Portfolios use shape separately.
func ProjectCapabilityFor(shape Shape, markerName, dominantExtension string) (CapabilityRow, bool) {
	row, ok := CapabilityForShape(shape)
	if !ok || row.Offer == nil {
		return CapabilityRow{}, false
	}
	extension := strings.ToLower(dominantExtension)
	if markerName != "" {
		extension = ""
		for _, marker := range row.Markers {
			if marker.Name == markerName && marker.Kind == MarkerGlob {
				extension = strings.ToLower(filepath.Ext(marker.Name))
				break
			}
		}
	}
	for _, allowed := range row.Offer.ProjectExtensions {
		if extension == allowed {
			return row, true
		}
	}
	return CapabilityRow{}, false
}

// ShapeForExtension reports the host-recognized project shape of a picked
// file. Tool-only rows without a shape are not project evidence.
func ShapeForExtension(extension string) Shape {
	extension = strings.ToLower(extension)
	for _, row := range capabilityRows {
		if row.Shape == "" {
			continue
		}
		for _, marker := range row.Markers {
			if marker.Kind == MarkerGlob && strings.EqualFold(filepath.Ext(marker.Name), extension) {
				return row.Shape
			}
		}
		for _, tool := range row.Tools {
			if tool.Match == ToolByExtension && tool.Value == extension {
				return row.Shape
			}
		}
	}
	return ""
}

// ToolForApp returns the tool row an installed application's name belongs
// to, if the table has one.
func ToolForApp(appName string) (Tool, bool) {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return Tool{}, false
	}
	for _, t := range Tools {
		for _, name := range t.AppNames {
			if strings.EqualFold(name, appName) {
				return t, true
			}
		}
	}
	return Tool{}, false
}

// The Tools table itself lives in tables.go, the package's data-only file.

// ToolForExtension returns the tool row for a file extension (".tex" or
// "tex"), if the table has one.
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
// table has one. Glob markers ("*.bib") are looked up by their extension.
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
