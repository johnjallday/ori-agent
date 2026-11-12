package foldermap

import "github.com/johnjallday/ori-agent/internal/folderdigest"

// FormatCount is how many projects of one format a map holds. Name is the
// application the host's tool table gives that format ("" when it names none),
// so a sentence can say whose files they are without this package naming one.
type FormatCount struct {
	Format string
	Name   string
	Count  int
}

// Readability splits the projects of the map's portfolio shape (the shape a
// Home would hold) by whether their files can be read offline: Readable counts
// those whose format the host's table gives a reading grammar, Unreadable
// lists the rest by format, largest first. It is a count of what is on disk,
// not of what any Home has read, and it opens no file.
type Readability struct {
	Shape      folderdigest.Shape
	Readable   int
	Unreadable []FormatCount
}

// UnreadableTotal is how many projects have no offline grammar, in all formats.
func (r Readability) UnreadableTotal() int {
	total := 0
	for _, u := range r.Unreadable {
		total += u.Count
	}
	return total
}

// Readability reports the map's portfolio shape split by offline readability.
// A map with no portfolio (no Home would hold these projects) reports nothing.
func (f Facts) Readability() Readability {
	if f.Portfolio == nil {
		return Readability{}
	}
	out := Readability{Shape: f.Portfolio.Shape}
	markers := f.formatMarkers(out.Shape)
	for _, s := range f.Shapes {
		if s.Shape != out.Shape || s.Count <= 0 {
			continue
		}
		if _, ok := folderdigest.FactsMarker(s.Format); ok {
			out.Readable += s.Count
			continue
		}
		out.Unreadable = append(out.Unreadable, FormatCount{Format: s.Format, Name: FormatName(markers[s.Format]), Count: s.Count})
	}
	return out
}

// formatMarkers maps each format of one shape to a marker that names it, for
// the tool table's application name. The first project of a format wins.
func (f Facts) formatMarkers(shape folderdigest.Shape) map[string]string {
	markers := make(map[string]string)
	note := func(p Project) {
		if p.Shape != shape || p.Format == "" {
			return
		}
		if _, ok := markers[p.Format]; !ok {
			markers[p.Format] = p.Marker
		}
	}
	for _, p := range f.Members {
		note(p)
	}
	if f.RootProject != nil {
		note(*f.RootProject)
	}
	return markers
}
