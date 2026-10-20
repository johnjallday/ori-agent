package resourcecatalog

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxTags      = 16
	maxTagChars  = 40
	MaxNoteBytes = 4000
)

// ErrAnnotationConflict is an annotation written against an older revision.
// The error carries the current annotation so nothing is merged silently.
var ErrAnnotationConflict = errors.New("this entry's notes changed since they were read")

// AnnotationConflictError holds the annotation as it is now.
type AnnotationConflictError struct {
	Current Annotation
}

func (e *AnnotationConflictError) Error() string { return ErrAnnotationConflict.Error() }

func (e *AnnotationConflictError) Unwrap() error { return ErrAnnotationConflict }

// AnnotationInput is the owner's whole annotation for one entry. BaseRevision
// is the revision it was read at (0 when the entry had none).
type AnnotationInput struct {
	Favorite     bool     `json:"favorite"`
	Tags         []string `json:"tags"`
	Note         string   `json:"note"`
	BaseRevision int64    `json:"base_revision"`
}

// SetAnnotation saves the owner's favorite, tags and note for one entry. It
// never touches what was observed, and works without the provider: the
// annotations are the owner's own and stay editable on history too.
func (s *Service) SetAnnotation(owner, studioID, entryID string, input AnnotationInput) (Annotation, error) {
	tags, note, err := normalizeAnnotation(input.Tags, input.Note)
	if err != nil {
		return Annotation{}, err
	}
	ref, err := s.resolveStudio(owner, studioID)
	if err != nil {
		return Annotation{}, err
	}
	var saved Annotation
	err = withLock(ref.paths.catalogDir, func() error {
		observations, err := readObservations(ref.paths)
		if err != nil {
			return err
		}
		if !hasEntry(observations, entryID) {
			return ErrNotFound
		}
		annotations, err := readAnnotations(ref.paths)
		if err != nil {
			return err
		}
		current, exists := annotations.Annotations[entryID]
		if (exists && current.Revision != input.BaseRevision) || (!exists && input.BaseRevision != 0) {
			return &AnnotationConflictError{Current: current}
		}
		annotations.Revision++
		saved = Annotation{Favorite: input.Favorite, Tags: tags, Note: note, Revision: current.Revision + 1, UpdatedAt: s.now()}
		if !saved.Favorite && len(saved.Tags) == 0 && saved.Note == "" {
			// An emptied annotation is removed rather than kept as a blank.
			delete(annotations.Annotations, entryID)
		} else {
			annotations.Annotations[entryID] = saved
		}
		annotations.SchemaVersion = DocumentSchemaVersion
		return writeDocument(ref.paths.annotations(), annotations)
	})
	return saved, err
}

// MoveAnnotation moves one entry's annotation to another present entry of the
// same category, kind and name: the owner's explicit answer to "this looks
// like the same resource, moved or reinstalled". Nothing moves on its own.
func (s *Service) MoveAnnotation(owner, studioID, fromID, toID string, baseRevision int64) (Annotation, error) {
	ref, err := s.resolveStudio(owner, studioID)
	if err != nil {
		return Annotation{}, err
	}
	var moved Annotation
	err = withLock(ref.paths.catalogDir, func() error {
		observations, err := readObservations(ref.paths)
		if err != nil {
			return err
		}
		from, fromFound := findEntry(observations, fromID)
		to, toFound := findEntry(observations, toID)
		if !fromFound || !toFound || fromID == toID || to.State != EntryPresent ||
			from.Category != to.Category || from.Kind != to.Kind || !strings.EqualFold(from.Name, to.Name) {
			return ErrInvalidRequest
		}
		annotations, err := readAnnotations(ref.paths)
		if err != nil {
			return err
		}
		current, exists := annotations.Annotations[fromID]
		if !exists || current.Revision != baseRevision {
			return &AnnotationConflictError{Current: current}
		}
		if target, occupied := annotations.Annotations[toID]; occupied {
			return &AnnotationConflictError{Current: target}
		}
		delete(annotations.Annotations, fromID)
		annotations.Revision++
		moved = current
		moved.Revision, moved.UpdatedAt = 1, s.now()
		annotations.Annotations[toID] = moved
		return writeDocument(ref.paths.annotations(), annotations)
	})
	return moved, err
}

// normalizeAnnotation trims tags, drops empty ones and repeats (first
// spelling wins, case-insensitively), and bounds every value.
func normalizeAnnotation(tags []string, note string) ([]string, string, error) {
	if len(tags) > MaxTags || len(note) > MaxNoteBytes || !utf8.ValidString(note) {
		return nil, "", ErrInvalidRequest
	}
	for _, r := range note {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return nil, "", ErrInvalidRequest
		}
	}
	clean := []string{}
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if !plainLine(tag, maxTagChars) {
			return nil, "", ErrInvalidRequest
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		clean = append(clean, tag)
	}
	return clean, strings.TrimSpace(note), nil
}

func hasEntry(observations Observations, entryID string) bool {
	_, found := findEntry(observations, entryID)
	return found
}

func findEntry(observations Observations, entryID string) (EntryRecord, bool) {
	for _, entry := range observations.Entries {
		if entry.EntryID == entryID {
			return entry, true
		}
	}
	return EntryRecord{}, false
}

// suggestedReplacement offers a present entry that looks like the same
// resource as an annotated one that is gone, withdrawn or from another
// setup. It is only a suggestion: moving the notes is the owner's action.
func (c catalogState) suggestedReplacement(entry EntryRecord) (EntryRecord, bool) {
	if _, annotated := c.annotations.Annotations[entry.EntryID]; !annotated {
		return EntryRecord{}, false
	}
	if c.access(entry) == AccessCurrent && entry.State == EntryPresent {
		return EntryRecord{}, false
	}
	var match EntryRecord
	matches := 0
	for _, candidate := range c.observations.Entries {
		if candidate.EntryID == entry.EntryID || candidate.State != EntryPresent || c.access(candidate) != AccessCurrent ||
			candidate.Category != entry.Category || candidate.Kind != entry.Kind || !strings.EqualFold(candidate.Name, entry.Name) {
			continue
		}
		if _, occupied := c.annotations.Annotations[candidate.EntryID]; occupied {
			continue
		}
		match = candidate
		matches++
	}
	return match, matches == 1
}
