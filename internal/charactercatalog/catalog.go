// Package charactercatalog owns the server-side character catalog: the curated,
// versioned set of visual identities an agent may be assigned, plus the single
// reserved identity belonging to Ori, the app's setup-and-navigation guide.
//
// The catalog is the only source of character truth (PRD FR-52/FR-53). Agent
// records store a stable catalog ID, never a filename, so a reviewed
// replacement asset can ship without rewriting a single agent (FR-114).
//
// Everything here is read-only. There is no mutation API, no write path, and no
// way for a user-supplied value to add or alter an entry — a working agent can
// only ever *reference* an ID that already passed validation at build time.
package charactercatalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

//go:embed catalog.json
var catalogJSON []byte

// SupportedMajorVersion is the catalog schema major version this build
// understands. A manifest declaring anything else fails to load rather than
// being interpreted under guessed semantics (FR-124).
const SupportedMajorVersion = 1

// Kind separates the single system-owned guide identity from the identities a
// user may assign to a working agent. It is a closed set; an unrecognized kind
// fails validation.
type Kind string

const (
	// KindGuide is Ori's reserved identity. Exactly one entry may hold it and
	// no working agent may ever be assigned it (FR-19/FR-71).
	KindGuide Kind = "guide"
	// KindWorking is a curated identity assignable to a working agent.
	KindWorking Kind = "working"
)

// MinWorkingCharacters is the floor set by FR-51: V1 ships at least eight
// working-agent choices *in addition to* Ori.
const MinWorkingCharacters = 8

// Palette carries the accent tokens a character contributes to its frame and
// surrounding chrome. These are tokens, not arbitrary CSS: every value is
// validated as a hex colour before it can reach a stylesheet.
type Palette struct {
	Base   string `json:"base"`
	Accent string `json:"accent"`
	Ink    string `json:"ink"`
}

// Assets names the three required renderings of a character. Static is the
// reduced-motion equivalent of Sprite and must convey the same meaning without
// animation (FR-120).
type Assets struct {
	Portrait string `json:"portrait"`
	Sprite   string `json:"sprite"`
	Static   string `json:"static"`
}

// Character is one catalog entry. Note what is deliberately absent: no prompt
// text, no tool list, no permission, no operational status. A character is
// presentation and tone only, so assigning one can never grant a capability
// (FR-73, Non-Goal 6).
type Character struct {
	ID           CharacterID `json:"id"`
	EntryVersion int         `json:"entry_version"`
	Kind         Kind        `json:"kind"`
	// Name is a descriptive role, not a proper noun. Invented character names
	// were dropped: a single common word is exactly where trademark collisions
	// cluster, and the role reads more usefully beside an agent's own name
	// anyway (see docs/CHARACTER_ASSET_PROVENANCE.md).
	Name         string   `json:"name"`
	Family       string   `json:"family"`
	FamilyLabel  string   `json:"family_label"`
	Purpose      string   `json:"purpose"`
	Description  string   `json:"description"`
	Silhouette   string   `json:"silhouette"`
	Prop         string   `json:"signature_prop"`
	IdleBehavior string   `json:"idle_behavior"`
	ToneTraits   []string `json:"tone_traits"`
	SampleLine   string   `json:"sample_line"`
	Palette      Palette  `json:"palette"`
	Assets       Assets   `json:"assets"`
	Provenance   string   `json:"provenance"`
}

// CharacterID is a stable catalog identifier. It is a distinct type so an agent
// name, a role slug, or a filename cannot be passed where an ID is expected.
type CharacterID string

// Catalog is the validated, immutable manifest.
type Catalog struct {
	Version         string      `json:"catalog_version"`
	ReservedGuideID CharacterID `json:"reserved_guide_id"`
	Characters      []Character `json:"characters"`

	byID map[CharacterID]Character
}

var (
	loadOnce sync.Once
	loaded   *Catalog
	loadErr  error
)

// Load returns the embedded catalog, validating it exactly once per process.
// A malformed catalog is a build defect, so callers that cannot proceed without
// one should surface the error at startup rather than degrade silently.
func Load() (*Catalog, error) {
	loadOnce.Do(func() {
		loaded, loadErr = parse(catalogJSON)
	})
	return loaded, loadErr
}

// MustLoad returns the validated catalog or panics. Intended for package-level
// initialization in tests and for startup paths that have already established
// the catalog must be present.
func MustLoad() *Catalog {
	c, err := Load()
	if err != nil {
		panic("charactercatalog: " + err.Error())
	}
	return c
}

// parse is the validation core, kept separate from the embedded bytes so tests
// can drive it with deliberately broken manifests.
func parse(raw []byte) (*Catalog, error) {
	var c Catalog
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	// An unknown field is a schema drift signal, not something to ignore: it
	// usually means a hand-edited manifest expected a capability this build
	// does not implement.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}

	if err := checkVersion(c.Version); err != nil {
		return nil, err
	}
	if c.ReservedGuideID == "" {
		return nil, fmt.Errorf("catalog: reserved_guide_id is required")
	}

	c.byID = make(map[CharacterID]Character, len(c.Characters))
	guides := 0
	working := 0

	for i, ch := range c.Characters {
		where := fmt.Sprintf("character %d (%q)", i, ch.ID)

		if err := validateID(ch.ID); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if _, dup := c.byID[ch.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate id", where)
		}
		if err := validateEntry(ch); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}

		switch ch.Kind {
		case KindGuide:
			guides++
			if ch.ID != c.ReservedGuideID {
				return nil, fmt.Errorf("%s: guide entry must use the reserved id %q", where, c.ReservedGuideID)
			}
		case KindWorking:
			working++
			// The reserved ID belongs to Ori alone. Catching it here means a
			// working agent can never be handed it, because it never enters the
			// assignable set in the first place (FR-19/FR-71).
			if ch.ID == c.ReservedGuideID {
				return nil, fmt.Errorf("%s: working character may not use the reserved guide id", where)
			}
		default:
			return nil, fmt.Errorf("%s: unknown kind %q", where, ch.Kind)
		}

		c.byID[ch.ID] = ch
	}

	if guides != 1 {
		return nil, fmt.Errorf("catalog: expected exactly 1 guide character, found %d", guides)
	}
	if working < MinWorkingCharacters {
		return nil, fmt.Errorf("catalog: expected at least %d working characters, found %d", MinWorkingCharacters, working)
	}
	if _, ok := c.byID[c.ReservedGuideID]; !ok {
		return nil, fmt.Errorf("catalog: reserved_guide_id %q matches no character", c.ReservedGuideID)
	}

	return &c, nil
}

func checkVersion(v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("catalog: catalog_version is required")
	}
	major, _, ok := strings.Cut(v, ".")
	if !ok {
		return fmt.Errorf("catalog: malformed catalog_version %q", v)
	}
	if major != fmt.Sprint(SupportedMajorVersion) {
		return fmt.Errorf("catalog: unsupported catalog_version %q (this build supports major version %d)", v, SupportedMajorVersion)
	}
	return nil
}

// idRe-equivalent check without a regexp dependency: lowercase letters, digits,
// and single interior hyphens. A restrictive ID keeps catalog IDs safe to use in
// a URL path, a CSS selector, and a filesystem path without escaping.
func validateID(id CharacterID) error {
	s := string(id)
	if s == "" {
		return fmt.Errorf("id is required")
	}
	if len(s) > 48 {
		return fmt.Errorf("id is longer than 48 characters")
	}
	if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") || strings.Contains(s, "--") {
		return fmt.Errorf("id %q has misplaced hyphens", s)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("id %q contains an illegal character %q", s, r)
		}
	}
	return nil
}

func validateEntry(ch Character) error {
	if ch.EntryVersion < 1 {
		return fmt.Errorf("entry_version must be >= 1")
	}
	required := map[string]string{
		"name":           ch.Name,
		"family":         ch.Family,
		"family_label":   ch.FamilyLabel,
		"purpose":        ch.Purpose,
		"description":    ch.Description,
		"silhouette":     ch.Silhouette,
		"signature_prop": ch.Prop,
		"idle_behavior":  ch.IdleBehavior,
		"sample_line":    ch.SampleLine,
		"provenance":     ch.Provenance,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	if len(ch.ToneTraits) == 0 {
		return fmt.Errorf("tone_traits must list at least one trait")
	}
	for _, t := range ch.ToneTraits {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("tone_traits contains an empty trait")
		}
	}

	for field, hex := range map[string]string{
		"palette.base":   ch.Palette.Base,
		"palette.accent": ch.Palette.Accent,
		"palette.ink":    ch.Palette.Ink,
	} {
		if !isHexColor(hex) {
			return fmt.Errorf("%s %q is not a hex colour", field, hex)
		}
	}

	for field, path := range map[string]string{
		"assets.portrait": ch.Assets.Portrait,
		"assets.sprite":   ch.Assets.Sprite,
		"assets.static":   ch.Assets.Static,
	} {
		if err := validateAssetPath(path); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}

	// A provenance link is mandatory, so an unreviewed asset cannot reach
	// production by simply omitting its record (FR-106/FR-128).
	if !strings.Contains(ch.Provenance, "#") {
		return fmt.Errorf("provenance %q must link to a specific record anchor", ch.Provenance)
	}
	return nil
}

// validateAssetPath keeps every asset inside the embedded static tree. It
// rejects absolute paths, traversal, Windows separators, and anything carrying a
// URL scheme — no catalog entry may point at a remote or third-party file, which
// is what makes "no third-party assets" checkable rather than merely asserted
// (FR-111).
func validateAssetPath(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("path is required")
	}
	if strings.Contains(p, "://") || strings.HasPrefix(p, "//") {
		return fmt.Errorf("path %q must be repository-relative, not a URL", p)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("path %q must not be absolute", p)
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("path %q must use forward slashes", p)
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("path %q has an unsafe segment", p)
		}
	}
	if !strings.HasPrefix(p, "characters/") {
		return fmt.Errorf("path %q must live under characters/", p)
	}
	if !strings.HasSuffix(p, ".svg") {
		return fmt.Errorf("path %q must be an .svg asset", p)
	}
	return nil
}

func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, r := range s[1:] {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

/* ---- read API ------------------------------------------------------------ */

// Get returns the character with the given ID. The boolean reports whether it
// exists, so a withdrawn or misspelled ID resolves to a visible miss rather than
// a zero-value character (FR-74).
func (c *Catalog) Get(id CharacterID) (Character, bool) {
	if c == nil {
		return Character{}, false
	}
	ch, ok := c.byID[id]
	return ch, ok
}

// Guide returns Ori's reserved identity.
func (c *Catalog) Guide() Character {
	ch, _ := c.Get(c.ReservedGuideID)
	return ch
}

// Working returns every character assignable to a working agent, ordered by ID
// so the picker and every response render in a stable order (FR-52).
func (c *Catalog) Working() []Character {
	if c == nil {
		return nil
	}
	out := make([]Character, 0, len(c.Characters))
	for _, ch := range c.Characters {
		if ch.Kind == KindWorking {
			out = append(out, ch)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// IsAssignable reports whether id may be stored on a working agent. This is the
// single authority every write path consults; rejecting the reserved guide ID
// here is what stops a direct API call from claiming Ori's identity (FR-71).
func (c *Catalog) IsAssignable(id CharacterID) bool {
	ch, ok := c.Get(id)
	return ok && ch.Kind == KindWorking
}

// AssetPaths returns every asset path the catalog declares, in catalog order.
func (c *Catalog) AssetPaths() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Characters)*3)
	for _, ch := range c.Characters {
		out = append(out, ch.Assets.Portrait, ch.Assets.Sprite, ch.Assets.Static)
	}
	return out
}

// ValidateAssetsExist checks that every declared asset resolves to a real file
// in fsys, which must be rooted where `characters/` lives (for the embedded web
// assets that is fs.Sub(web.Static, "static")).
//
// Path *shape* is validated at load; this checks the file is actually there.
// The two are separate because a catalog can be structurally valid and still
// reference an asset someone forgot to commit — which would ship as a broken
// portrait rather than a build failure (FR-124).
func (c *Catalog) ValidateAssetsExist(fsys fs.FS) error {
	if c == nil {
		return fmt.Errorf("catalog: nil")
	}
	var missing []string
	for _, ch := range c.Characters {
		for _, p := range []string{ch.Assets.Portrait, ch.Assets.Sprite, ch.Assets.Static} {
			if _, err := fs.Stat(fsys, p); err != nil {
				missing = append(missing, fmt.Sprintf("%s -> %s", ch.ID, p))
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("catalog: %d declared asset(s) missing: %s", len(missing), strings.Join(missing, ", "))
	}
	return nil
}
