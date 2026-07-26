package downloadsjanitor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This is the only code in the feature that opens a file, and it exists solely
// so an opted-in classifier can look at the first few kilobytes of a plain
// document to work out what it is.
//
// What it deliberately does not do, and will not be extended to do:
//
//   - Execute, import, mount, extract, or render anything. An archive, a disk
//     image, an installer, and an unsupported binary are all metadata-only, no
//     matter how much easier a peek inside would make classification (FR-58).
//   - Follow anything found inside a file. Links, includes, and instructions in
//     content are text to be read past, never acted on.
//   - Read on behalf of the agent. The Downloads Curator never receives a
//     read tool; this path is the classifier's, and its output goes to the
//     classifier (FR-113, FR-114).
//
// Enabling content inspection widens exactly one thing: the classifier may see
// a bounded extract of a plain-text-shaped file. It grants no new capability to
// anything else.

// Content-reading bounds. Small on purpose: the goal is "what kind of document
// is this", which the opening lines answer, and a smaller window is a smaller
// amount of the user's private data in play.
const (
	// MaxContentBytes is the most that is ever read from a file.
	MaxContentBytes = 8 * 1024
	// MaxExcerptRunes bounds what is passed to a classifier after extraction.
	MaxExcerptRunes = 1200
	// MinUsefulExcerptRunes is the shortest extract worth sending. Below this
	// the deterministic verdict stands: a handful of characters is not evidence.
	MinUsefulExcerptRunes = 24
)

// ErrContentNotPermitted reports a read that the current mode, consent state,
// or file type does not allow.
var ErrContentNotPermitted = errors.New("downloads janitor may not read this file's contents")

// readableContentExtensions is the closed set of file types a bounded extract
// may be taken from: plain, passive, text-shaped documents.
//
// Every entry here is a format that is inert when read as bytes. Office
// documents, PDFs, archives, and media are absent — not because their contents
// would be useless, but because getting text out of them means parsing or
// extracting, and a parser is exactly the kind of active handling this feature
// promised not to do.
var readableContentExtensions = map[string]struct{}{
	".txt": {}, ".md": {}, ".markdown": {}, ".rst": {}, ".log": {},
	".csv": {}, ".tsv": {}, ".json": {}, ".ndjson": {}, ".xml": {},
	".yaml": {}, ".yml": {}, ".ini": {}, ".conf": {}, ".cfg": {},
	".sql": {}, ".tex": {}, ".vcf": {}, ".ics": {}, ".srt": {}, ".vtt": {},
}

// ContentReadable reports whether a candidate's type permits a bounded extract.
func ContentReadable(candidate JanitorCandidate) bool {
	extension := strings.ToLower(strings.TrimSpace(candidate.Extension))
	if extension == "" {
		return false
	}
	_, ok := readableContentExtensions[extension]
	return ok
}

// ExtractForClassification reads a bounded excerpt from a candidate, for the
// classifier and nothing else.
//
// The candidate is revalidated immediately before the read — eligible, settled,
// still the same file — because content inspection is a second visit to a file
// that was inspected earlier, and everything may have changed since (FR-54).
func (s *Service) ExtractForClassification(settings JanitorSettings, candidate JanitorCandidate) (string, error) {
	if !settings.ContentMode.ReadsFileContent() {
		return "", fmt.Errorf("%w: content inspection is off", ErrContentNotPermitted)
	}
	if settings.RequiresContentConsent() {
		return "", fmt.Errorf("%w: this provider has not been confirmed", ErrContentNotPermitted)
	}
	if !ContentReadable(candidate) {
		return "", fmt.Errorf("%w: %s is not a plain text document", ErrContentNotPermitted, candidate.Display())
	}

	root, err := s.scannerFor().ResolveRoot(settings)
	if err != nil {
		return "", err
	}
	// The same identity check the mutation path performs. A file that changed
	// since it was proposed is not the file the user is reviewing.
	current, err := currentFingerprint(root, candidate.Name)
	if err != nil {
		return "", fmt.Errorf("%w: the file is no longer available", ErrContentNotPermitted)
	}
	if !candidate.Fingerprint.Matches(current) {
		return "", fmt.Errorf("%w: the file changed since it was scanned", ErrContentNotPermitted)
	}
	if current.Size > int64(MaxContentBytes)*64 {
		// Very large files are metadata-only: the opening bytes of a
		// multi-megabyte log say little, and the size itself is a signal.
		return "", fmt.Errorf("%w: the file is too large to sample", ErrContentNotPermitted)
	}

	path := filepath.Join(root, candidate.Name)
	// Opened read-only, with O_NOFOLLOW so a symlink swapped in after the
	// fingerprint check cannot redirect the read elsewhere.
	file, err := os.OpenFile(path, os.O_RDONLY|openNoFollow, 0) // #nosec G304 -- the stored candidate name inside the approved root
	if err != nil {
		return "", fmt.Errorf("%w: the file could not be read", ErrContentNotPermitted)
	}
	defer func() { _ = file.Close() }()

	buffer := make([]byte, MaxContentBytes)
	n, err := file.Read(buffer)
	if err != nil && n == 0 {
		return "", fmt.Errorf("%w: the file could not be read", ErrContentNotPermitted)
	}
	excerpt := sanitizeExcerpt(buffer[:n])
	if utf8.RuneCountInString(excerpt) < MinUsefulExcerptRunes {
		return "", fmt.Errorf("%w: there was not enough readable text to help", ErrContentNotPermitted)
	}
	return excerpt, nil
}

// sanitizeExcerpt turns raw bytes into text safe to put in a prompt and a log.
//
// Binary content yields nothing usable, which is the intended outcome: a file
// that is not really text should fall back to metadata rather than having its
// bytes reinterpreted as characters.
func sanitizeExcerpt(raw []byte) string {
	if !utf8.Valid(raw) {
		// Trim to the last valid rune boundary rather than rejecting outright:
		// an 8KB window will usually cut mid-character in a valid UTF-8 file.
		for len(raw) > 0 && !utf8.Valid(raw) {
			raw = raw[:len(raw)-1]
		}
	}
	var b strings.Builder
	for _, r := range string(raw) {
		switch {
		case r == '\n', r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r), isBidiControl(r):
			continue
		case r == utf8.RuneError:
			continue
		default:
			b.WriteRune(r)
		}
	}
	excerpt := strings.Join(strings.Fields(b.String()), " ")
	runes := []rune(excerpt)
	if len(runes) > MaxExcerptRunes {
		excerpt = string(runes[:MaxExcerptRunes])
	}
	return excerpt
}
