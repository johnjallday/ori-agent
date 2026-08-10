package types

import (
	"encoding/json"
	"strings"
)

// This file is the only place in the codebase that still knows the retired
// avatar/character schema, and it knows it in exactly one direction: reading old
// records off disk. Nothing here is ever written back (FR-77), and none of it
// makes an HTTP client using the old request shape succeed — disk migration and
// API compatibility are separate concerns (FR-78).

// Legacy display-mode values as they appear in records written before the
// Appearance model. `fallback` is the one that disappears entirely: it becomes
// Generated, which is a positive choice rather than an error state (FR-15).
const (
	legacyDisplayModeFallback  = "fallback"
	legacyDisplayModeUploaded  = "uploaded"
	legacyDisplayModeCharacter = "character"
)

// Migration reason codes. They are deliberately coarse, stable strings: they are
// logged and surfaced in health output, so they must never carry a filesystem
// path or any other user content (FR-73).
const (
	// AppearanceReasonUploadMissing means the record pointed at an uploaded file
	// that is not on disk, so Upload could not stay the active source.
	AppearanceReasonUploadMissing = "upload-missing"
	// AppearanceReasonCharacterUnavailable means the referenced catalog entry is
	// unknown, withdrawn, or reserved, so Character could not stay active.
	AppearanceReasonCharacterUnavailable = "character-unavailable"
	// AppearanceReasonInvalidMode means the stored display mode was not one this
	// build understands.
	AppearanceReasonInvalidMode = "invalid-mode"
	// AppearanceReasonInvalidColor means the stored avatar colour was not a
	// colour and was dropped rather than persisted as garbage.
	AppearanceReasonInvalidColor = "invalid-color"
	// AppearanceReasonVoiceDiscarded records that a voice/tone flag was found and
	// dropped. It is not mapped to anything: the concept is gone (FR-19/FR-70).
	AppearanceReasonVoiceDiscarded = "voice-discarded"
)

// LegacyAppearance is the retired avatar/character state captured verbatim from
// a record on disk, before any interpretation.
type LegacyAppearance struct {
	AvatarColor    string
	AvatarImage    string
	DisplayMode    string
	CatalogID      string
	CatalogVersion int
	VoiceEnabled   bool
	// HadCharacter distinguishes "no character object at all" from "a character
	// object with nothing useful in it". Only the former is silent; the latter is
	// worth normalizing away on the next write.
	HadCharacter bool
}

// IsEmpty reports whether the record carried no legacy appearance state at all,
// which is the common case for an agent created after this feature ships.
func (l *LegacyAppearance) IsEmpty() bool {
	if l == nil {
		return true
	}
	return strings.TrimSpace(l.AvatarColor) == "" &&
		strings.TrimSpace(l.AvatarImage) == "" &&
		!l.HadCharacter
}

// UnmarshalJSON decodes agent metadata while capturing the retired appearance
// fields into an unexported side-channel.
//
// The retired fields are gone from the struct itself, so they can never be
// serialized again — that is what makes "no permanent dual-write path" a
// property of the type rather than a rule someone has to remember (FR-77). But
// they still have to be *read* once, because local user records are not API
// clients and must not lose their avatar on upgrade (FR-68).
func (m *AgentMetadata) UnmarshalJSON(data []byte) error {
	// The alias sheds this method, so decoding it does not recurse.
	type metadataAlias AgentMetadata
	var raw struct {
		metadataAlias
		AvatarColor string `json:"avatar_color"`
		AvatarImage string `json:"avatar_image"`
		Character   *struct {
			DisplayMode    string `json:"display_mode"`
			CatalogID      string `json:"catalog_id"`
			CatalogVersion int    `json:"catalog_version"`
			VoiceEnabled   bool   `json:"voice_enabled"`
		} `json:"character"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*m = AgentMetadata(raw.metadataAlias)

	legacy := LegacyAppearance{
		AvatarColor: strings.TrimSpace(raw.AvatarColor),
		AvatarImage: strings.TrimSpace(raw.AvatarImage),
	}
	if raw.Character != nil {
		legacy.HadCharacter = true
		legacy.DisplayMode = strings.TrimSpace(raw.Character.DisplayMode)
		legacy.CatalogID = strings.TrimSpace(raw.Character.CatalogID)
		legacy.CatalogVersion = raw.Character.CatalogVersion
		legacy.VoiceEnabled = raw.Character.VoiceEnabled
	}
	if !legacy.IsEmpty() {
		m.legacyAppearance = &legacy
	}
	return nil
}

// TakeLegacyAppearance returns the captured legacy state and clears it, so a
// second migration pass over the same in-memory record is a no-op. Draining
// rather than peeking is what makes repeated migration idempotent by
// construction (FR-76).
func (m *AgentMetadata) TakeLegacyAppearance() *LegacyAppearance {
	if m == nil {
		return nil
	}
	legacy := m.legacyAppearance
	m.legacyAppearance = nil
	return legacy
}

// AppearanceEnvironment supplies the two facts migration cannot derive from the
// record alone: whether a referenced catalog entry is still assignable, and
// whether a referenced upload is still on disk.
//
// They are function fields rather than a package dependency because the catalog
// package imports this one; passing them in also lets tests drive the whole
// FR-70 mapping table without a filesystem or a real catalog. A nil callback
// means "cannot tell", and migration then trusts the record — the renderer's
// runtime fallback still covers the case, and guessing "missing" would silently
// downgrade a user's saved mode over a transient read failure.
type AppearanceEnvironment struct {
	// CharacterVersion reports the current entry version of an assignable
	// working character. The second result is false for unknown, withdrawn, and
	// reserved-guide IDs alike (FR-25/FR-72).
	CharacterVersion func(catalogID string) (int, bool)
	// UploadExists reports whether the named file is present in the avatar
	// storage directory.
	UploadExists func(filename string) bool
}

// AppearanceMigration is the outcome of one migration attempt.
type AppearanceMigration struct {
	// Appearance is the canonical result. Never nil.
	Appearance *AgentAppearance
	// Changed reports whether anything actually differs from what was on disk,
	// so a already-canonical record is not rewritten on every startup (FR-76).
	Changed bool
	// Reasons lists stable codes for every downgrade or discard performed. Safe
	// to log verbatim: no paths, no user text (FR-73).
	Reasons []string
}

// MigrateAppearance produces the canonical Appearance for a record.
//
// Precedence is fixed by FR-71: when a record somehow carries both canonical
// Appearance and legacy fields — a downgrade, a hand-edit, a snapshot written by
// an older build — canonical wins and the legacy fields are dropped on the next
// write. Anything else would let a stale snapshot quietly undo the migration.
//
// Availability downgrades (FR-72) apply only on the legacy path. An
// already-canonical record keeps the mode the user saved even when the asset is
// missing right now, because a missing asset at startup is a *render-time*
// condition the renderer reports and recovers from; rewriting the saved choice
// on the strength of one failed stat would turn a temporary problem into
// permanent data loss (FR-84).
func MigrateAppearance(current *AgentAppearance, legacy *LegacyAppearance, env AppearanceEnvironment) AppearanceMigration {
	out := AppearanceMigration{}

	if current != nil {
		before := *current
		next := current.Clone()
		next.Normalize()
		out.Appearance = next
		out.Changed = !appearanceEqual(&before, next) || !legacy.IsEmpty()
		if legacy != nil && legacy.VoiceEnabled {
			out.Reasons = append(out.Reasons, AppearanceReasonVoiceDiscarded)
		}
		return out
	}

	next := NewAgentAppearance()
	if legacy.IsEmpty() {
		out.Appearance = next
		// A record with neither canonical nor legacy appearance still gains an
		// explicit Generated object, which is a real change worth persisting: it
		// is what makes "every agent resolves to a non-nil Appearance" true on
		// disk and not only in memory (FR-4).
		out.Changed = true
		return out
	}

	// Colour, upload, and character are copied first and unconditionally, before
	// any mode decision. Inactive source data is still the user's data: a record
	// that was displaying an upload keeps its character selection, and vice
	// versa, so the very first switch after upgrading is not a rebuild (FR-70's
	// "even when inactive" rows).
	if legacy.AvatarColor != "" {
		if color, ok := NormalizeAppearanceColor(legacy.AvatarColor); ok && color != "" {
			next.Generated.Color = color
		} else {
			out.Reasons = append(out.Reasons, AppearanceReasonInvalidColor)
		}
	}
	if legacy.AvatarImage != "" {
		next.Uploaded = &UploadedAppearance{Image: legacy.AvatarImage}
	}

	characterUsable := false
	if legacy.CatalogID != "" {
		version := legacy.CatalogVersion
		if env.CharacterVersion != nil {
			current, assignable := env.CharacterVersion(legacy.CatalogID)
			characterUsable = assignable
			if assignable {
				version = current
			}
		} else {
			characterUsable = true
		}
		// The reference is kept even when the entry is not assignable today. A
		// withdrawn character may come back, and keeping the ID lets the editor
		// say which selection went missing instead of showing an agent that
		// mysteriously reverted to Generated (FR-73).
		next.Character = &CharacterAppearance{CatalogID: legacy.CatalogID, CatalogVersion: version}
	}

	uploadUsable := legacy.AvatarImage != ""
	if uploadUsable && env.UploadExists != nil {
		uploadUsable = env.UploadExists(legacy.AvatarImage)
	}

	switch legacy.DisplayMode {
	case legacyDisplayModeCharacter:
		if characterUsable {
			next.Mode = AppearanceModeCharacter
		} else {
			out.Reasons = append(out.Reasons, AppearanceReasonCharacterUnavailable)
		}
	case legacyDisplayModeUploaded:
		if uploadUsable {
			next.Mode = AppearanceModeUploaded
		} else {
			out.Reasons = append(out.Reasons, AppearanceReasonUploadMissing)
		}
	case legacyDisplayModeFallback:
		// Already Generated.
	case "":
		// No explicit mode: reproduce the historical field-presence rule so an
		// agent that predates display modes looks exactly as it did (FR-70).
		if uploadUsable {
			next.Mode = AppearanceModeUploaded
		}
	default:
		out.Reasons = append(out.Reasons, AppearanceReasonInvalidMode)
	}

	if legacy.VoiceEnabled {
		out.Reasons = append(out.Reasons, AppearanceReasonVoiceDiscarded)
	}

	next.Normalize()
	out.Appearance = next
	out.Changed = true
	return out
}

// appearanceEqual compares two normalized records field by field. It exists so
// Changed can stay honest without a reflect.DeepEqual that would also compare
// pointer identity.
func appearanceEqual(a, b *AgentAppearance) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Mode != b.Mode {
		return false
	}
	if a.GeneratedColor() != b.GeneratedColor() {
		return false
	}
	// A nil Generated and an empty Generated are the same appearance to a
	// renderer, but only one of them is canonical, so the presence of the object
	// itself counts as a difference worth rewriting.
	if (a.Generated == nil) != (b.Generated == nil) {
		return false
	}
	if (a.Uploaded == nil) != (b.Uploaded == nil) || a.UploadedImage() != b.UploadedImage() {
		return false
	}
	if (a.Character == nil) != (b.Character == nil) {
		return false
	}
	return a.CharacterCatalogID() == b.CharacterCatalogID() &&
		a.CharacterCatalogVersion() == b.CharacterCatalogVersion()
}
