package downloadsjanitor

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Version 1's categories are a fixed, closed set. They are not user-editable
// and not learned: a closed set is what lets the API accept a category ID
// instead of a destination path, which is what keeps a client from ever
// choosing where a file lands.
const (
	CategoryDocuments  Category = "documents"
	CategoryImages     Category = "images"
	CategoryAudio      Category = "audio"
	CategoryVideo      Category = "video"
	CategoryArchives   Category = "archives"
	CategoryInstallers Category = "installers"
	CategoryData       Category = "data"
	// CategoryOther is the honest fallback for anything unrecognized or
	// low-confidence. It is never a guess dressed up as a decision — candidates
	// routed here are marked "Needs review" (FR-51).
	CategoryOther Category = "other"
)

// CategoryDefinition is one category's stable identity and display data.
type CategoryDefinition struct {
	// ID is the stable key clients submit and the server validates against.
	ID Category `json:"id"`
	// FolderName is the directory created directly beneath the filing root. It
	// is a constant per category — never derived from user or model input.
	FolderName string `json:"folder_name"`
	// Label and Description are for the review UI.
	Label       string `json:"label"`
	Description string `json:"description"`
}

// CategoryRegistry is the complete version 1 catalog, in display order.
var CategoryRegistry = []CategoryDefinition{
	{CategoryDocuments, "Documents", "Documents", "PDFs, text, and office documents"},
	{CategoryImages, "Images", "Images", "Photos, screenshots, and graphics"},
	{CategoryAudio, "Audio", "Audio", "Music, recordings, and sound files"},
	{CategoryVideo, "Video", "Video", "Movies and video clips"},
	{CategoryArchives, "Archives", "Archives", "Zip files, disk images, and other bundles"},
	{CategoryInstallers, "Installers", "Installers", "App installers and packages"},
	{CategoryData, "Data", "Data", "Spreadsheets, exports, and structured data"},
	{CategoryOther, "Other", "Other", "Anything Ori could not confidently place"},
}

// ErrUnknownCategory reports a category ID outside the fixed version 1 set.
// Rejecting rather than defaulting matters: an unrecognized category must never
// quietly become a folder name.
var ErrUnknownCategory = errors.New("unknown downloads janitor category")

// LookupCategory resolves and validates a category ID from untrusted input
// (an API request or a model response). Matching is case-insensitive and
// trimmed; anything else is rejected.
func LookupCategory(id string) (CategoryDefinition, error) {
	normalized := Category(strings.ToLower(strings.TrimSpace(id)))
	for _, definition := range CategoryRegistry {
		if definition.ID == normalized {
			return definition, nil
		}
	}
	return CategoryDefinition{}, fmt.Errorf("%w: %q", ErrUnknownCategory, id)
}

// ValidCategory reports whether id is one of the fixed categories.
func ValidCategory(id Category) bool {
	_, err := LookupCategory(string(id))
	return err == nil
}

// DestinationDir returns the absolute directory an approved move would file
// into: <root>/<filingRoot>/<Category>.
//
// Every component is server-derived — the configured root, the configured
// filing-root name, and a constant folder name looked up from the registry.
// No caller-supplied string reaches this path, which is why a traversal or an
// absolute-path injection has nothing to attach to.
func DestinationDir(settings JanitorSettings, category Category) (string, error) {
	definition, err := LookupCategory(string(category))
	if err != nil {
		return "", err
	}
	filingRoot := settings.FilingRootPath()
	if filingRoot == "" {
		return "", fmt.Errorf("%w: this workspace has no configured folder", ErrInvalidSettings)
	}
	destination := filepath.Join(filingRoot, definition.FolderName)

	// Belt and braces: prove the result is still inside the filing root. The
	// inputs are all constants today, so this can only fire if someone later
	// makes one of them dynamic — which is exactly when it should fire.
	if !withinRoot(filingRoot, destination) {
		return "", fmt.Errorf("%w: destination escapes the filing folder", ErrUnknownCategory)
	}
	return destination, nil
}

// withinRoot reports whether path is root itself or lexically inside it.
func withinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
