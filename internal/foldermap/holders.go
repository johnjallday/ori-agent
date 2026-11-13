package foldermap

import (
	"sort"
	"strings"
)

// ProjectHolder is a direct child recorded by the walk. Index is its place in
// Map.Folders, never a browser-supplied path. Counts cover found projects only.
type ProjectHolder struct {
	Index  int
	Name   string
	Count  int
	Shapes []ShapeCount
}

// ProjectHolders groups saved project evidence by its first folder. A direct
// child that is itself a project counts as its own holder. Unopened trees do
// not invent projects. Results are largest first with deterministic name ties.
func ProjectHolders(m *Map) []ProjectHolder {
	if m == nil {
		return nil
	}
	indexes := map[string]int{}
	for index, folder := range m.Folders {
		if folder.Path != "" && folder.Depth() == 1 {
			indexes[folder.Path] = index
		}
	}
	groups := map[string]*ProjectHolder{}
	for _, project := range FactsFrom(m).Members {
		name, _, _ := strings.Cut(project.Path, "/")
		index, recorded := indexes[name]
		if !recorded {
			continue
		}
		holder := groups[name]
		if holder == nil {
			holder = &ProjectHolder{Index: index, Name: name}
			groups[name] = holder
		}
		holder.Count++
		found := false
		for i := range holder.Shapes {
			if holder.Shapes[i].Shape == project.Shape {
				holder.Shapes[i].Count++
				found = true
				break
			}
		}
		if !found {
			holder.Shapes = append(holder.Shapes, ShapeCount{Shape: project.Shape, Count: 1})
		}
	}
	var out []ProjectHolder
	for _, holder := range groups {
		sort.Slice(holder.Shapes, func(i, j int) bool {
			if holder.Shapes[i].Count != holder.Shapes[j].Count {
				return holder.Shapes[i].Count > holder.Shapes[j].Count
			}
			return holder.Shapes[i].Shape < holder.Shapes[j].Shape
		})
		out = append(out, *holder)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}
