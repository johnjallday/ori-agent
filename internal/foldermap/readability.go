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
// Home would hold) by whether their files can be read: Readable counts those
// whose format the caller says an installed app plugin reads, Unreadable lists
// the rest by format. It is a count of what is on disk, not of what any Home
// has read, and it opens no file.
type Readability struct {
	Shape      folderdigest.Shape
	Readable   int
	Unreadable []FormatCount
}

// UnreadableTotal is how many projects nothing reads, in all formats.
func (r Readability) UnreadableTotal() int {
	total := 0
	for _, u := range r.Unreadable {
		total += u.Count
	}
	return total
}

// PortfolioShape is the shape a Home would hold for this map, or "" when no
// Home would hold these projects.
func (f Facts) PortfolioShape() folderdigest.Shape {
	if f.Portfolio == nil {
		return ""
	}
	return f.Portfolio.Shape
}

// Readability reports the map's portfolio shape split by readability. readable
// says whether a project format is read by an installed app plugin; the host
// has no reader of its own, so a nil readable reads nothing. A map with no
// portfolio (no Home would hold these projects) reports nothing.
func (f Facts) Readability(readable func(format string) bool) Readability {
	if f.Portfolio == nil {
		return Readability{}
	}
	out := Readability{Shape: f.Portfolio.Shape}
	markers := f.formatMarkers(out.Shape)
	for _, s := range f.Shapes {
		if s.Shape != out.Shape || s.Count <= 0 {
			continue
		}
		if readable != nil && readable(s.Format) {
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
