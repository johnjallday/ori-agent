package types

import "strings"

// AgentAppearance is an agent's visual configuration: one active source plus
// the saved state of the sources that are not currently active.
//
// It replaces the previous split between flat avatar fields (a colour or an
// uploaded file) and a nested character identity that also owned the display
// mode. That split made "avatar" and "character" read as two different kinds of
// agent, and it stored the active mode inside the character object even when the
// user had chosen an upload. Appearance collapses both into one concept with
// three interchangeable sources (PRD FR-1).
//
// Two properties are load-bearing:
//
//   - The active source is *explicit*. Nothing infers it from whichever nested
//     field happens to be populated, so switching sources is a reversible choice
//     rather than a destructive one (FR-5).
//   - Inactive source state is *retained*. Switching from Upload to Character
//     keeps the uploaded filename; switching back needs no re-upload. Only an
//     explicit removal deletes source data (FR-11/FR-12).
//
// Appearance is presentation only. Nothing here may influence an agent's
// prompt, model, role, tools, skills, permissions, routing, or execution
// (FR-17).
type AgentAppearance struct {
	// Mode names the requested source. It is always one of the three constants
	// below on a normalized record.
	Mode AppearanceMode `json:"mode"`
	// Generated is always present on a normalized record: it is the source that
	// can always render, so it doubles as the safe fallback (FR-13).
	Generated *GeneratedAppearance `json:"generated,omitempty"`
	// Uploaded is nil until the user has uploaded an image, and survives a switch
	// to another source until the user removes it explicitly (FR-11).
	Uploaded *UploadedAppearance `json:"uploaded,omitempty"`
	// Character is nil until the user has chosen one, and likewise survives a
	// switch to another source.
	Character *CharacterAppearance `json:"character,omitempty"`
}

// AppearanceMode names which of the three sources an agent requests.
//
// There is deliberately no "fallback" member. Generated is a positive user
// choice with its own controls; falling back is something the *renderer* does
// when a requested asset cannot be produced, and it is reported as a runtime
// outcome rather than saved (FR-15).
type AppearanceMode string

const (
	// AppearanceModeGenerated renders Ori's deterministic generated portrait,
	// optionally recoloured by the user.
	AppearanceModeGenerated AppearanceMode = "generated"
	// AppearanceModeCharacter renders a curated catalog portrait.
	AppearanceModeCharacter AppearanceMode = "character"
	// AppearanceModeUploaded renders the user's own uploaded image.
	AppearanceModeUploaded AppearanceMode = "uploaded"
)

// IsValidAppearanceMode reports whether m is one of the three canonical modes.
// Write paths use it to reject an unknown mode from a direct API call instead of
// persisting a value no renderer understands (FR-3/FR-58).
func IsValidAppearanceMode(m AppearanceMode) bool {
	switch m {
	case AppearanceModeGenerated, AppearanceModeCharacter, AppearanceModeUploaded:
		return true
	}
	return false
}

// GeneratedAppearance configures the deterministic generated portrait.
type GeneratedAppearance struct {
	// Color is an optional override for the palette the deterministic algorithm
	// would otherwise pick. Empty means "use the generated colour" — it is not a
	// missing value to be filled in, which is why reset is simply clearing it
	// (FR-6).
	Color string `json:"color,omitempty"`
}

// UploadedAppearance points at the user's stored image.
type UploadedAppearance struct {
	// Image is the server-managed relative filename inside the avatar storage
	// directory. It is never an arbitrary client path or a remote URL: clients
	// cannot write this field at all, only the upload endpoint can (FR-8/FR-55).
	Image string `json:"image,omitempty"`
}

// CharacterAppearance points at a curated catalog entry.
type CharacterAppearance struct {
	// CatalogID is the stable catalog identifier — deliberately not a filename,
	// so a reviewed replacement asset ships without rewriting agent records.
	CatalogID string `json:"catalog_id,omitempty"`
	// CatalogVersion records which entry revision was selected. The server
	// assigns it from the validated catalog entry; a client may read it but may
	// not choose it (FR-10/FR-55).
	CatalogVersion int `json:"catalog_version,omitempty"`
}

// NewAgentAppearance returns the default appearance for a new agent: Generated,
// with no colour override (FR-4).
func NewAgentAppearance() *AgentAppearance {
	return &AgentAppearance{
		Mode:      AppearanceModeGenerated,
		Generated: &GeneratedAppearance{},
	}
}

// Clone returns a deep copy so a handler can validate a candidate appearance
// without touching the stored one. A rejected request must leave the stored
// record byte-identical (FR-58).
func (a *AgentAppearance) Clone() *AgentAppearance {
	if a == nil {
		return nil
	}
	out := &AgentAppearance{Mode: a.Mode}
	if a.Generated != nil {
		g := *a.Generated
		out.Generated = &g
	}
	if a.Uploaded != nil {
		u := *a.Uploaded
		out.Uploaded = &u
	}
	if a.Character != nil {
		c := *a.Character
		out.Character = &c
	}
	return out
}

// Normalize puts the record into canonical shape without changing which source
// the user asked for.
//
// It is structural only: it fixes an unknown mode, guarantees Generated exists,
// lower-cases and expands a valid colour, drops a colour that is not a colour,
// and collapses empty source objects to nil so an agent that never uploaded
// anything does not carry `"uploaded": {}` forever.
//
// It deliberately does NOT decide whether the requested asset is actually
// available — that needs the catalog and the filesystem, and it is the
// migration's job (see MigrateAppearance). Keeping the two apart is what lets a
// render-time failure stay a recoverable runtime state instead of silently
// rewriting the user's saved choice (FR-84).
func (a *AgentAppearance) Normalize() {
	if a == nil {
		return
	}
	if a.Generated == nil {
		a.Generated = &GeneratedAppearance{}
	}
	if color, ok := NormalizeAppearanceColor(a.Generated.Color); ok {
		a.Generated.Color = color
	} else {
		a.Generated.Color = ""
	}
	if a.Uploaded != nil {
		a.Uploaded.Image = strings.TrimSpace(a.Uploaded.Image)
		if a.Uploaded.Image == "" {
			a.Uploaded = nil
		}
	}
	if a.Character != nil {
		a.Character.CatalogID = strings.TrimSpace(a.Character.CatalogID)
		if a.Character.CatalogID == "" {
			a.Character = nil
		} else if a.Character.CatalogVersion < 0 {
			a.Character.CatalogVersion = 0
		}
	}
	if !IsValidAppearanceMode(a.Mode) {
		a.Mode = AppearanceModeGenerated
	}
}

// NormalizeAppearanceColor validates a user-supplied colour and returns it as a
// lower-case six-digit `#rrggbb` value.
//
// Three-digit shorthand is expanded and a missing leading `#` is tolerated on
// input, but exactly one form is ever stored — otherwise `#FFF`, `#ffffff`, and
// `ffffff` would be three different saved values for one colour and every
// comparison downstream (version token, dirty check, test fixture) would have to
// re-normalize (FR-7).
//
// An empty string is reported as valid-and-empty: absent is a legitimate state
// meaning "use the deterministic colour", not an error.
func NormalizeAppearanceColor(raw string) (string, bool) {
	hex := strings.TrimSpace(raw)
	if hex == "" {
		return "", true
	}
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 3 && len(hex) != 6 {
		return "", false
	}
	var b strings.Builder
	b.Grow(7)
	b.WriteByte('#')
	for i := 0; i < len(hex); i++ {
		c := hex[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
			c += 'a' - 'A'
		default:
			return "", false
		}
		b.WriteByte(c)
		if len(hex) == 3 {
			// Expand shorthand in place: #abc -> #aabbcc.
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

// GeneratedColor returns the saved colour override, or "" when the deterministic
// palette should choose.
func (a *AgentAppearance) GeneratedColor() string {
	if a == nil || a.Generated == nil {
		return ""
	}
	return a.Generated.Color
}

// UploadedImage returns the saved upload filename regardless of the active mode.
// An inactive upload is still the user's data; callers that care whether it is
// rendering must check Mode themselves (FR-12).
func (a *AgentAppearance) UploadedImage() string {
	if a == nil || a.Uploaded == nil {
		return ""
	}
	return strings.TrimSpace(a.Uploaded.Image)
}

// CharacterCatalogID returns the saved character selection regardless of the
// active mode, for the same reason as UploadedImage.
func (a *AgentAppearance) CharacterCatalogID() string {
	if a == nil || a.Character == nil {
		return ""
	}
	return strings.TrimSpace(a.Character.CatalogID)
}

// CharacterCatalogVersion returns the server-assigned catalog revision for the
// saved character selection, or 0 when there is none.
func (a *AgentAppearance) CharacterCatalogVersion() int {
	if a == nil || a.Character == nil {
		return 0
	}
	return a.Character.CatalogVersion
}

// SetGeneratedColor stores a validated colour override. It reports false and
// changes nothing when the value is not a colour, so a bad input cannot
// half-apply (FR-58).
func (a *AgentAppearance) SetGeneratedColor(raw string) bool {
	if a == nil {
		return false
	}
	color, ok := NormalizeAppearanceColor(raw)
	if !ok {
		return false
	}
	if a.Generated == nil {
		a.Generated = &GeneratedAppearance{}
	}
	a.Generated.Color = color
	return true
}

// ClearGeneratedColor resets the override so the deterministic algorithm chooses
// again. It never touches the active mode or the other sources (FR-31).
func (a *AgentAppearance) ClearGeneratedColor() {
	if a == nil {
		return
	}
	if a.Generated == nil {
		a.Generated = &GeneratedAppearance{}
		return
	}
	a.Generated.Color = ""
}

// SetCharacter saves a character selection and activates it. The version is
// supplied by the caller from the validated catalog entry, never by a client
// (FR-10/FR-32).
func (a *AgentAppearance) SetCharacter(catalogID string, catalogVersion int) {
	if a == nil {
		return
	}
	a.Character = &CharacterAppearance{
		CatalogID:      strings.TrimSpace(catalogID),
		CatalogVersion: catalogVersion,
	}
	a.Mode = AppearanceModeCharacter
}

// ClearCharacter removes the saved character selection.
//
// It returns to Generated only when Character was the source actually being
// rendered. Removing a selection the agent was not displaying must leave the
// active mode alone, and must never touch the upload or the colour override
// (FR-33/FR-34/FR-35).
func (a *AgentAppearance) ClearCharacter() {
	if a == nil {
		return
	}
	a.Character = nil
	if a.Mode == AppearanceModeCharacter {
		a.Mode = AppearanceModeGenerated
	}
}

// SetUpload saves an uploaded filename and activates it in the same operation.
//
// Activation is part of the save on purpose: storing a file without showing it
// reproduces exactly the confusion this feature exists to remove — a user
// uploads an image and nothing appears to happen (FR-36).
func (a *AgentAppearance) SetUpload(filename string) {
	if a == nil {
		return
	}
	a.Uploaded = &UploadedAppearance{Image: strings.TrimSpace(filename)}
	a.Mode = AppearanceModeUploaded
}

// ClearUpload removes the saved upload reference, mirroring ClearCharacter:
// back to Generated only if Upload was active, and never touching the saved
// character or colour (FR-38/FR-39/FR-40).
func (a *AgentAppearance) ClearUpload() {
	if a == nil {
		return
	}
	a.Uploaded = nil
	if a.Mode == AppearanceModeUploaded {
		a.Mode = AppearanceModeGenerated
	}
}
