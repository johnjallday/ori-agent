package foldermap

import (
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// Project is a folder with a project marker directly inside it.
type Project struct {
	Path   string             `json:"path"`
	Name   string             `json:"name"`
	Shape  folderdigest.Shape `json:"shape"`
	Format string             `json:"format,omitempty"`
	Marker string             `json:"marker"`
	Depth  int                `json:"depth"`
}

// ShapeCount is how many projects of one shape (and format) the map holds.
type ShapeCount struct {
	Shape  folderdigest.Shape `json:"shape"`
	Format string             `json:"format,omitempty"`
	Count  int                `json:"count"`
}

// Portfolio is a shape with a Home provider that has at least
// PortfolioMinProjects projects in the map.
type Portfolio struct {
	Shape            folderdigest.Shape `json:"shape"`
	Count            int                `json:"count"`
	HomeProviderKey  string             `json:"home_provider_key"`
	HomeProviderName string             `json:"home_provider_name,omitempty"`
	HomeName         string             `json:"home_name,omitempty"`
}

// Facts are what the host concludes from the map; a model never asserts
// them (FR14). They are recomputed from the map after every walk or look.
type Facts struct {
	// RootProject is set when a marker sits directly in the fed folder: the
	// workspace is that project (the Tools stage).
	RootProject *Project `json:"root_project,omitempty"`
	// Members are projects below the fed folder, at any depth (the Group
	// stage), in walk order.
	Members []Project `json:"members,omitempty"`
	// Shapes counts every project (root and members) by shape and format,
	// largest first.
	Shapes []ShapeCount `json:"shapes,omitempty"`
	// Portfolio is the largest shape with a Home provider and enough
	// projects (the Home stage).
	Portfolio *Portfolio `json:"portfolio,omitempty"`
	// ProjectAreas are the distinct top-level folders that hold member
	// projects deeper down ("Albums", "Picker Songs").
	ProjectAreas []string `json:"project_areas,omitempty"`
	// Dump applies the digest's thresholds to the fed folder's own files.
	Dump           bool `json:"dump,omitempty"`
	RootLooseFiles int  `json:"root_loose_files"`
	RootLooseKinds int  `json:"root_loose_kinds"`
	// EmptyFolders lists walked folders with no files and no subfolders.
	EmptyFolders []string  `json:"empty_folders,omitempty"`
	Newest       time.Time `json:"newest,omitzero"`
	Folders      int       `json:"folders"`
	Entries      int       `json:"entries"`
	Unopened     []string  `json:"unopened,omitempty"`
	Large        []string  `json:"large,omitempty"`
	Unreadable   int       `json:"unreadable,omitempty"`
	Skipped      int       `json:"skipped,omitempty"`
	Stopped      string    `json:"stopped,omitempty"`
}

// Recognized reports whether the map holds any project.
func (f Facts) Recognized() bool { return f.RootProject != nil || len(f.Members) > 0 }

// ProjectCount is the number of projects, root included.
func (f Facts) ProjectCount() int {
	n := len(f.Members)
	if f.RootProject != nil {
		n++
	}
	return n
}

// FactsFrom computes the facts from a map.
func FactsFrom(m *Map) Facts {
	var facts Facts
	if m == nil {
		return facts
	}
	facts.Folders = len(m.Folders)
	facts.Entries = m.Entries
	facts.Skipped = m.SkippedLinks + m.SkippedFolders + m.SkippedHidden
	facts.Stopped = m.Stopped

	counts := make(map[ShapeCount]int)
	var order []ShapeCount
	areas := make(map[string]bool)
	for _, f := range m.Folders {
		if f.Newest.After(facts.Newest) {
			facts.Newest = f.Newest
		}
		switch f.State {
		case StateUnopened:
			facts.Unopened = append(facts.Unopened, f.Path)
		case StateLarge:
			facts.Large = append(facts.Large, f.Path)
		case StateUnreadable:
			facts.Unreadable++
		case StateWalked:
			if f.Path != "" && f.Files == 0 && f.Subfolders == 0 {
				facts.EmptyFolders = append(facts.EmptyFolders, f.Path)
			}
		}
		if f.Path == "" {
			facts.RootLooseFiles = f.Files
			facts.RootLooseKinds = f.Kinds
		}
		if !f.IsProject() {
			continue
		}
		p := Project{Path: f.Path, Name: f.Name(), Shape: f.Shape, Format: f.Format, Marker: f.Marker, Depth: f.Depth()}
		if f.Path == "" {
			root := p
			facts.RootProject = &root
		} else {
			facts.Members = append(facts.Members, p)
			if p.Depth >= 2 {
				areas[strings.SplitN(p.Path, "/", 2)[0]] = true
			}
		}
		key := ShapeCount{Shape: f.Shape, Format: f.Format}
		if _, seen := counts[key]; !seen {
			order = append(order, key)
		}
		counts[key]++
	}
	for _, key := range order {
		key.Count = counts[key]
		facts.Shapes = append(facts.Shapes, key)
	}
	sort.SliceStable(facts.Shapes, func(i, j int) bool { return facts.Shapes[i].Count > facts.Shapes[j].Count })
	for area := range areas {
		facts.ProjectAreas = append(facts.ProjectAreas, area)
	}
	sort.Strings(facts.ProjectAreas)
	facts.Dump = facts.RootProject == nil && facts.RootLooseFiles >= DumpMinLooseFiles && facts.RootLooseKinds >= DumpMinKinds
	facts.Portfolio = portfolioFrom(facts.Members)
	return facts
}

// ShapeTotals sums project counts per shape across formats, largest first.
func (f Facts) ShapeTotals() []ShapeCount {
	totals := make(map[folderdigest.Shape]int)
	var order []folderdigest.Shape
	for _, s := range f.Shapes {
		if _, seen := totals[s.Shape]; !seen {
			order = append(order, s.Shape)
		}
		totals[s.Shape] += s.Count
	}
	out := make([]ShapeCount, 0, len(order))
	for _, shape := range order {
		out = append(out, ShapeCount{Shape: shape, Count: totals[shape]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// portfolioFrom picks the shape with the most member projects among shapes
// whose capability row names a Home provider.
func portfolioFrom(members []Project) *Portfolio {
	byShape := make(map[folderdigest.Shape]int)
	var order []folderdigest.Shape
	for _, p := range members {
		if _, seen := byShape[p.Shape]; !seen {
			order = append(order, p.Shape)
		}
		byShape[p.Shape]++
	}
	var best *Portfolio
	for _, shape := range order {
		count := byShape[shape]
		if count < PortfolioMinProjects {
			continue
		}
		row, ok := folderdigest.CapabilityForShape(shape)
		if !ok || row.Offer == nil || row.Offer.HomeProviderKey == "" {
			continue
		}
		if best == nil || count > best.Count {
			best = &Portfolio{Shape: shape, Count: count, HomeProviderKey: row.Offer.HomeProviderKey,
				HomeProviderName: row.Offer.HomeProviderName, HomeName: row.Offer.HomeName}
		}
	}
	return best
}
