package secondbrain

import (
	"io/fs"
	"sort"
	"strings"
)

// MaxPeopleListed bounds the People listing (Issue 776 FR22).
const MaxPeopleListed = 100

// PersonNote is one note in People: its name from the file name, and whose it
// is from the marker's first bytes. Nothing else is read.
type PersonNote struct {
	Name string
	// Kind is the marker's kind (person, seed) when the note opens with Ori's
	// marker, or "user".
	Kind string
	// File is the note's file name, for an Obsidian link.
	File string
}

// People is the bounded listing of People/*.md.
type People struct {
	People  []PersonNote
	Partial bool
}

// ListPeople lists People/*.md in a folder Ori set up: links, hidden entries,
// folders and README are skipped; at most MaxPeopleListed, sorted by name.
// Each note is opened no-follow only to peek at its marker.
func ListPeople(root string) (People, error) {
	pinned, err := pinFolder(root)
	if err != nil {
		return People{}, err
	}
	defer func() { _ = pinned.Close() }()
	dir, entries := listFolder(pinned, FolderPeople)
	if dir == nil {
		return People{People: []PersonNote{}}, nil
	}
	defer func() { _ = dir.Close() }()
	out := People{People: []PersonNote{}}
	names := make([]string, 0, len(entries))
	for index, entry := range entries {
		if index == maxWrittenListed {
			out.Partial = true
			break
		}
		name := entry.Name()
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() || strings.HasPrefix(name, ".") ||
			!IsMarkdown(name) || strings.EqualFold(name, projectNoteName) {
			continue
		}
		names = append(names, name)
	}
	sort.Slice(names, func(a, b int) bool { return strings.ToLower(names[a]) < strings.ToLower(names[b]) })
	for _, name := range names {
		if len(out.People) == MaxPeopleListed {
			out.Partial = true
			break
		}
		if info, err := dir.Lstat(name); err != nil || !info.Mode().IsRegular() {
			continue
		}
		kind := NoteKindUser
		if _, marked, ok := markedNote(dir, name); ok {
			kind = marked
		}
		stem := strings.TrimSuffix(strings.TrimSuffix(name, ".md"), ".markdown")
		out.People = append(out.People, PersonNote{Name: stem, Kind: kind, File: name})
	}
	return out, nil
}
