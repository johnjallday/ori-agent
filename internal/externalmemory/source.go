package externalmemory

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ParseError records one file a source could not read or parse. It names the
// file and a short reason; it never carries entry content (FR16).
type ParseError struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Source reads one agent's memory into entries. Implementations only read:
// they never create, modify, or delete anything under their root (FR12).
type Source interface {
	// ID is one of the Source* constants.
	ID() string
	// Location is the folder or file Ori looks for, shown on the source card.
	Location() string
	// Available reports whether Location exists.
	Available() bool
	// Read parses every memory file. A file that fails is reported as a
	// ParseError and skipped; err is reserved for the source as a whole.
	Read(ctx context.Context) ([]Entry, []ParseError, error)
}

// Registry holds the sources in display order.
type Registry struct {
	sources map[string]Source
}

// NewRegistry builds a registry. A nil source is ignored; a second source with
// the same id replaces the first.
func NewRegistry(sources ...Source) *Registry {
	r := &Registry{sources: map[string]Source{}}
	for _, source := range sources {
		if source != nil {
			r.sources[source.ID()] = source
		}
	}
	return r
}

// Get returns the source for id.
func (r *Registry) Get(id string) (Source, bool) {
	if r == nil {
		return nil, false
	}
	source, ok := r.sources[id]
	return source, ok
}

// All returns the registered sources in SourceIDs order.
func (r *Registry) All() []Source {
	if r == nil {
		return nil
	}
	out := make([]Source, 0, len(r.sources))
	for _, id := range SourceIDs {
		if source, ok := r.sources[id]; ok {
			out = append(out, source)
		}
	}
	return out
}

// pathExists reports whether path exists, following symlinks.
func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// sectionKeys hands out section keys unique within one file, suffixing a
// repeated key with ~2, ~3, … in order of appearance.
type sectionKeys map[string]int

func (k sectionKeys) next(key string) string {
	k[key]++
	if n := k[key]; n > 1 {
		return fmt.Sprintf("%s~%d", key, n)
	}
	return key
}
