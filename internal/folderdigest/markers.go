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
	// ProjectFormat is inert, host-owned catalog identity for a recognized
	// project marker. It grants no plugin, project connection or execution.
	ProjectFormat string
	// ProjectBundle allows a recognized project format to be a directory
	// marker. Other project-format globs match regular files only.
	ProjectBundle bool
	// Facts, when set, says where a project file of this format keeps its
	// tempo, length and track count. It is inert: generic code interprets it,
	// and a Home reads facts only with the user's song-details consent.
	Facts *ProjectFacts
}

// ProjectFacts describes, as data, where three song facts sit in a
// block-structured text project file: a block opens with a "<NAME …" line
// and closes with a ">" line, and an attribute is a "KEY value …" line. The
// root is the block the file opens with. Every field is a block name or an
// attribute key; nothing here is a pattern or code.
type ProjectFacts struct {
	// Root is the first word of the file's first non-empty line ("<NAME").
	Root string
	// Tempo is the root's attribute whose first value is the base tempo.
	Tempo string
	// TempoChanges is the block in the root holding tempo points; TempoPoint
	// is its point attribute, whose second value is a tempo.
	TempoChanges string
	TempoPoint   string
	// Track is a block in the root; each one is one track.
	Track string
	// Item is a block in a track; ItemStart + ItemLength is where it ends.
	Item       string
	ItemStart  string
	ItemLength string
}

// FactsMarker returns the project marker of format that declares facts, so
// a caller can read and name that format's files without naming it.
func FactsMarker(format string) (Marker, bool) {
	if format == "" {
		return Marker{}, false
	}
	for _, marker := range Markers {
		if marker.ProjectFormat == format && marker.Facts != nil && !marker.ProjectBundle && marker.Kind == MarkerGlob {
			return marker, true
		}
	}
	return Marker{}, false
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
		if m.ProjectFormat != "" && isDir && !m.ProjectBundle {
			return false
		}
		// Project extension globs match case-insensitively,
		// while exact-name manifests above keep their original case.
		ok, err := filepath.Match(strings.ToLower(m.Name), strings.ToLower(name))
		return err == nil && ok
	}
	return false
}

// ProjectFormatOption describes a catalog-only marker choice, not an
// installed blueprint, project creator, or permission to read a DAW file.
type ProjectFormatOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ProjectFormatOptions exposes bounded choices from the same marker table
// used by discovery/validation, without hardcoding plugin names in the Home.
func ProjectFormatOptions() []ProjectFormatOption {
	options := make([]ProjectFormatOption, 0, 8)
	seen := make(map[string]bool)
	for _, marker := range Markers {
		if marker.ProjectFormat != "" && !seen[marker.ProjectFormat] && len(options) < 16 {
			options = append(options, ProjectFormatOption{ID: marker.ProjectFormat, Label: marker.Label})
			seen[marker.ProjectFormat] = true
		}
	}
	return options
}

// IntegrationKeyForProjectFormat names the reviewed integration whose project
// files are of this catalog format, from the same host-owned table discovery
// uses: a marker of that format whose extension one capability offer lists as
// supported by its integration. A format no reviewed integration supports (for
// example Logic or Ableton today) returns false, so nothing is ever offered as
// a cure for it.
func IntegrationKeyForProjectFormat(format string) (string, bool) {
	if format == "" {
		return "", false
	}
	for _, row := range capabilityRows {
		if row.Offer == nil || row.Offer.IntegrationKey == "" {
			continue
		}
		for _, marker := range row.Markers {
			if marker.ProjectFormat != format {
				continue
			}
			for _, extension := range row.Offer.ProjectExtensions {
				if strings.EqualFold(marker.Name, "*"+extension) {
					return row.Offer.IntegrationKey, true
				}
			}
		}
	}
	return "", false
}

// MatchMarker returns the highest-precedence marker row that one entry
// satisfies, if any. Callers keep the first hit across a folder's entries in
// table order, which markerRank makes cheap.
// KnownProjectFormat validates a catalog format against the one host-owned
// marker table, rather than hardcoding any plugin domain into callers.
func KnownProjectFormat(format string) bool {
	if format == "" {
		return false
	}
	for _, marker := range Markers {
		if marker.ProjectFormat == format {
			return true
		}
	}
	return false
}

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
