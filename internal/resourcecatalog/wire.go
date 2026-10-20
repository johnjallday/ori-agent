package resourcecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// WireSchemaVersion is the schema of both operations' input and output.
const WireSchemaVersion = 1

// Bounds of the two operation answers. The plugin declares the same byte
// limits on its operations; the host applies its own regardless.
const (
	MaxSources              = 16
	MaxPageEntries          = 256
	MaxSourcesAnswerBytes   = 32 << 10
	MaxInventoryAnswerBytes = 256 << 10
	MaxNativeIDBytes        = 512
	MaxCursorBytes          = 512
	MaxEntryFacts           = 8
	MaxSkipReasons          = 8

	maxInstallationLabelChars = 80
	maxAppVersionChars        = 40
	maxSourceLabelChars       = 60
	maxLocationChars          = 512
	maxDetailChars            = 240
	maxNameChars              = 160
	maxFactValueChars         = 160
	maxDescriptionTextChars   = 500
	maxDescriptionSourceChars = 60
)

// Source access says what reading a source needs: its files, or a running
// application.
const (
	AccessFile = "file"
	AccessLive = "live"
)

// Source states describe a source as it is now. Only SourceReady can be read.
const (
	SourceReady          = "ready"
	SourceAbsent         = "absent"
	SourceDenied         = "denied"
	SourceUnsupported    = "unsupported"
	SourceAppClosed      = "app_closed"
	SourceReaderMissing  = "reader_missing"
	SourceReaderOutdated = "reader_outdated"
	SourceRemoteOff      = "web_remote_off"
)

// Inventory states. Anything but InventoryOK carries no entries and ends that
// source's read with the state as its outcome.
const (
	InventoryOK                  = "ok"
	InventoryInstallationChanged = "installation_changed"
	InventoryCursorReset         = "cursor_reset"
)

// Skip reasons a page may count.
const (
	SkipSymlink              = "symlink"
	SkipOutsideSource        = "outside_source"
	SkipUnreadable           = "unreadable"
	SkipUnsupportedName      = "unsupported_name"
	SkipTooLarge             = "too_large"
	SkipHidden               = "hidden"
	SkipExternalRegistration = "external_registration"
	SkipDepthLimit           = "depth_limit"
)

var (
	// ErrInvalidAnswer is an operation answer the host refuses whole.
	ErrInvalidAnswer = errors.New("resource catalog answer is invalid")

	installationIDPattern = regexp.MustCompile(`^inst_[A-Za-z0-9_-]{3,123}$`)

	sourceStates = map[string]bool{
		SourceReady: true, SourceAbsent: true, SourceDenied: true, SourceUnsupported: true,
		SourceAppClosed: true, SourceReaderMissing: true, SourceReaderOutdated: true, SourceRemoteOff: true,
	}
	inventoryStates = map[string]bool{
		InventoryOK: true, SourceAbsent: true, SourceDenied: true, SourceUnsupported: true,
		SourceAppClosed: true, SourceReaderMissing: true, SourceReaderOutdated: true, SourceRemoteOff: true,
		InventoryInstallationChanged: true, InventoryCursorReset: true,
	}
	skipReasons = map[string]bool{
		SkipSymlink: true, SkipOutsideSource: true, SkipUnreadable: true, SkipUnsupportedName: true,
		SkipTooLarge: true, SkipHidden: true, SkipExternalRegistration: true, SkipDepthLimit: true,
	}
)

// SourcesAnswer is the output of the sources operation.
type SourcesAnswer struct {
	SchemaVersion int          `json:"schema_version"`
	Installation  Installation `json:"installation"`
	Sources       []Source     `json:"sources"`
}

// Installation identifies one application resource folder on one computer.
type Installation struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Layout       string `json:"layout"`
	AppInstalled *bool  `json:"app_installed"`
	AppVersion   string `json:"app_version,omitempty"`
}

// Source is one place the plugin can read for one category. Location is for
// the owner's review only; it never reaches an agent.
type Source struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Label    string `json:"label"`
	Location string `json:"location"`
	Access   string `json:"access"`
	State    string `json:"state"`
	Detail   string `json:"detail,omitempty"`
}

// InventoryRequest is the input of the inventory operation.
type InventoryRequest struct {
	SchemaVersion  int    `json:"schema_version"`
	InstallationID string `json:"installation_id"`
	SourceID       string `json:"source_id"`
	Cursor         string `json:"cursor"`
	Limit          int    `json:"limit"`
}

// InventoryAnswer is one page of one source.
type InventoryAnswer struct {
	SchemaVersion  int              `json:"schema_version"`
	InstallationID string           `json:"installation_id"`
	SourceID       string           `json:"source_id"`
	State          string           `json:"state"`
	Entries        []InventoryEntry `json:"entries"`
	NextCursor     *string          `json:"next_cursor"`
	Coverage       *Coverage        `json:"coverage"`
}

// InventoryEntry is one observed resource. NativeID is the plugin's stable
// identity inside its source; the host derives the entry ID from it.
type InventoryEntry struct {
	NativeID    string       `json:"native_id"`
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	ModifiedAt  string       `json:"modified_at,omitempty"`
	Facts       []Fact       `json:"facts"`
	Description *Description `json:"description,omitempty"`
}

// Fact is one declared observed fact with its value.
type Fact struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// Description is text found in the resource's own metadata, with where it
// came from. It is shown as attributed, never as verification.
type Description struct {
	Text   string `json:"text"`
	Source string `json:"source"`
}

// Coverage counts what one page visited and skipped.
type Coverage struct {
	Visited int    `json:"visited"`
	Skipped []Skip `json:"skipped"`
}

// Skip is a count of items not cataloged for one reason.
type Skip struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// DecodeSourcesAnswer accepts a sources answer whole or not at all. Every
// source must belong to a declared category.
func DecodeSourcesAnswer(raw []byte, declaration *Declaration) (SourcesAnswer, error) {
	var answer SourcesAnswer
	if declaration == nil {
		return SourcesAnswer{}, fmt.Errorf("%w: no declaration", ErrInvalidAnswer)
	}
	if err := workspacecontinuity.DecodeDocument(raw, &answer, MaxSourcesAnswerBytes); err != nil {
		return SourcesAnswer{}, fmt.Errorf("%w: not the declared shape", ErrInvalidAnswer)
	}
	if err := answer.validate(declaration); err != nil {
		return SourcesAnswer{}, fmt.Errorf("%w: %v", ErrInvalidAnswer, err)
	}
	return answer, nil
}

func (a SourcesAnswer) validate(declaration *Declaration) error {
	installation := a.Installation
	switch {
	case a.SchemaVersion != WireSchemaVersion:
		return errors.New("schema_version must be 1")
	case !installationIDPattern.MatchString(installation.ID):
		return errors.New("installation id is not an opaque installation identifier")
	case !plainLine(installation.Label, maxInstallationLabelChars):
		return errors.New("installation label is not one plain line")
	case installation.Layout != "per_user" && installation.Layout != "portable":
		return errors.New("installation layout is unknown")
	case installation.AppInstalled == nil:
		return errors.New("installation must say whether the application is installed")
	case installation.AppVersion != "" && !plainLine(installation.AppVersion, maxAppVersionChars):
		return errors.New("application version is not one plain line")
	case len(a.Sources) == 0 || len(a.Sources) > MaxSources:
		return fmt.Errorf("sources must number 1 to %d", MaxSources)
	}
	seen := make(map[string]bool, len(a.Sources))
	for _, source := range a.Sources {
		if !localIDPattern.MatchString(source.ID) || seen[source.ID] {
			return errors.New("source ids must be unique identifiers")
		}
		seen[source.ID] = true
		if _, ok := declaration.Category(source.Category); !ok {
			return errors.New("a source names an undeclared category")
		}
		switch {
		case !plainLine(source.Label, maxSourceLabelChars):
			return errors.New("source label is not one plain line")
		case !plainLine(source.Location, maxLocationChars):
			return errors.New("source location is not one plain line")
		case source.Access != AccessFile && source.Access != AccessLive:
			return errors.New("source access is unknown")
		case !sourceStates[source.State]:
			return errors.New("source state is unknown")
		case source.Detail != "" && !plainLine(source.Detail, maxDetailChars):
			return errors.New("source detail is not one plain line")
		}
	}
	return nil
}

// Source returns the answered source with id.
func (a SourcesAnswer) Source(id string) (Source, bool) {
	for _, source := range a.Sources {
		if source.ID == id {
			return source, true
		}
	}
	return Source{}, false
}

// NewInventoryRequest builds the input for one page.
func NewInventoryRequest(installationID, sourceID, cursor string, limit int) (InventoryRequest, error) {
	request := InventoryRequest{SchemaVersion: WireSchemaVersion, InstallationID: installationID, SourceID: sourceID, Cursor: cursor, Limit: limit}
	switch {
	case !installationIDPattern.MatchString(installationID):
		return InventoryRequest{}, errors.New("installation id is not an opaque installation identifier")
	case !localIDPattern.MatchString(sourceID):
		return InventoryRequest{}, errors.New("source id is not an identifier")
	case !validCursor(cursor, true):
		return InventoryRequest{}, errors.New("cursor is not an opaque cursor")
	case limit < 1 || limit > MaxPageEntries:
		return InventoryRequest{}, fmt.Errorf("limit must be 1 to %d", MaxPageEntries)
	}
	return request, nil
}

// DecodeInventoryAnswer accepts one page whole or not at all. It must answer
// the request it was asked, stay within the request's limit and use only
// kinds and facts its source's category declares.
func DecodeInventoryAnswer(raw []byte, declaration *Declaration, source Source, request InventoryRequest) (InventoryAnswer, error) {
	var answer InventoryAnswer
	if declaration == nil {
		return InventoryAnswer{}, fmt.Errorf("%w: no declaration", ErrInvalidAnswer)
	}
	if err := workspacecontinuity.DecodeDocument(raw, &answer, MaxInventoryAnswerBytes); err != nil {
		return InventoryAnswer{}, fmt.Errorf("%w: not the declared shape", ErrInvalidAnswer)
	}
	category, ok := declaration.Category(source.Category)
	if !ok || source.ID != request.SourceID {
		return InventoryAnswer{}, fmt.Errorf("%w: the source is not part of the declaration", ErrInvalidAnswer)
	}
	if err := answer.validate(category, request); err != nil {
		return InventoryAnswer{}, fmt.Errorf("%w: %v", ErrInvalidAnswer, err)
	}
	return answer, nil
}

func (a InventoryAnswer) validate(category Category, request InventoryRequest) error {
	switch {
	case a.SchemaVersion != WireSchemaVersion:
		return errors.New("schema_version must be 1")
	case a.InstallationID != request.InstallationID || a.SourceID != request.SourceID:
		return errors.New("the answer is about another installation or source")
	case !inventoryStates[a.State]:
		return errors.New("state is unknown")
	case a.Entries == nil || a.NextCursor == nil || a.Coverage == nil:
		return errors.New("entries, next_cursor and coverage are required")
	case !validCursor(*a.NextCursor, true):
		return errors.New("next_cursor is not an opaque cursor")
	case a.Coverage.Visited < 0 || a.Coverage.Skipped == nil || len(a.Coverage.Skipped) > MaxSkipReasons:
		return errors.New("coverage is out of range")
	}
	if a.State != InventoryOK && (len(a.Entries) > 0 || *a.NextCursor != "") {
		return errors.New("only an ok page carries entries or a cursor")
	}
	if len(a.Entries) > request.Limit {
		return errors.New("the page has more entries than were asked for")
	}
	reasons := make(map[string]bool, len(a.Coverage.Skipped))
	for _, skip := range a.Coverage.Skipped {
		if !skipReasons[skip.Reason] || reasons[skip.Reason] || skip.Count < 1 {
			return errors.New("skip reasons must be known, unique and counted")
		}
		reasons[skip.Reason] = true
	}
	natives := make(map[string]bool, len(a.Entries))
	for _, entry := range a.Entries {
		if err := entry.validate(category); err != nil {
			return err
		}
		if natives[entry.NativeID] {
			return errors.New("a native id appears twice on one page")
		}
		natives[entry.NativeID] = true
	}
	return nil
}

func (e InventoryEntry) validate(category Category) error {
	switch {
	case !ValidNativeID(e.NativeID):
		return errors.New("native_id is not a contained stable identity")
	case !category.hasKind(e.Kind):
		return errors.New("an entry has a kind its category does not declare")
	case !plainLine(e.Name, maxNameChars):
		return errors.New("an entry name is not one plain line")
	case e.Facts == nil || len(e.Facts) > MaxEntryFacts:
		return errors.New("an entry's facts are out of range")
	}
	if e.ModifiedAt != "" {
		modified, err := time.Parse(time.RFC3339, e.ModifiedAt)
		if err != nil || modified.UTC().Year() < 1970 || modified.UTC().Year() > 9998 {
			return errors.New("modified_at is not a plausible RFC 3339 time")
		}
	}
	facts := make(map[string]bool, len(e.Facts))
	for _, fact := range e.Facts {
		if !category.hasFact(fact.ID) || facts[fact.ID] || !plainLine(fact.Value, maxFactValueChars) {
			return errors.New("facts must be declared, unique and plain")
		}
		facts[fact.ID] = true
	}
	if e.Description != nil && (!plainLine(e.Description.Text, maxDescriptionTextChars) || !plainLine(e.Description.Source, maxDescriptionSourceChars)) {
		return errors.New("description must be plain text with its source")
	}
	return nil
}

// ValidNativeID accepts a plugin's identity for one resource inside one
// source: NFC text without control characters that is never an absolute path
// and never climbs out of its source.
func ValidNativeID(value string) bool {
	if value == "" || len(value) > MaxNativeIDBytes || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~") || strings.HasPrefix(value, "\\") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == 0x2028 || r == 0x2029 {
			return false
		}
	}
	for _, segment := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return false
		}
	}
	return true
}

// EntryID is the host's identity for one observed resource. It binds the
// installation and source, so the same path on another computer or in
// another source is a different entry and never inherits its annotations.
func EntryID(installationID, sourceID, nativeID string) string {
	sum := sha256.Sum256([]byte(installationID + "\x00" + sourceID + "\x00" + nativeID))
	return "rc_" + hex.EncodeToString(sum[:16])
}

// validCursor accepts printable ASCII without spaces, bounded. An opaque
// cursor needs nothing else, and nothing else can smuggle text into a prompt.
func validCursor(value string, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}
	if len(value) > MaxCursorBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// plainLine accepts one line of plain, visible text within limit characters.
// Besides control characters it refuses line and paragraph separators and
// the marks that reorder text, so a name reads as what it is. A value with
// nothing visible in it is refused too.
func plainLine(value string, limit int) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	visible := false
	for _, r := range value {
		switch {
		case unicode.IsControl(r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r), unicode.Is(unicode.Bidi_Control, r):
			return false
		case unicode.IsGraphic(r) && !unicode.IsSpace(r) && !unicode.Is(unicode.Cf, r):
			visible = true
		}
	}
	return visible
}
