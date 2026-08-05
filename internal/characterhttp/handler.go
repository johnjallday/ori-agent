// Package characterhttp serves the curated character catalog to the browser.
//
// The handler is read-only by construction: it registers one GET route, holds
// no store, and has no method that mutates anything. Character assignment
// happens through the existing agent endpoints, which validate the ID against
// the same catalog — so there is no path here that could grant an identity.
package characterhttp

import (
	"fmt"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// Handler serves the character catalog.
type Handler struct {
	catalog *charactercatalog.Catalog
	etag    string
}

// New builds a handler over the embedded catalog. The catalog is validated at
// load, so a failure here is a build defect and is returned rather than hidden.
func New() (*Handler, error) {
	cat, err := charactercatalog.Load()
	if err != nil {
		return nil, err
	}
	// The catalog is immutable for the life of the process, so a version-derived
	// ETag lets the browser skip re-fetching it on every page that needs a
	// portrait, while still invalidating the moment a build ships a new catalog.
	return &Handler{
		catalog: cat,
		etag:    fmt.Sprintf(`"charcat-%s-%d"`, cat.Version, len(cat.Characters)),
	}, nil
}

// characterDTO is the browser-facing projection. It is deliberately not the
// storage struct: `provenance` is a repository path that would leak the local
// documentation layout to every client, and nothing in the UI needs it.
type characterDTO struct {
	ID           string   `json:"id"`
	EntryVersion int      `json:"entry_version"`
	Kind         string   `json:"kind"`
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
	// Ordering hint for the picker only. Omitted when empty so a client cannot
	// read an absent list as "compatible with nothing" — every character stays
	// selectable for every agent (FR-65).
	Roles   []string `json:"roles,omitempty"`
	Palette struct {
		Base   string `json:"base"`
		Accent string `json:"accent"`
		Ink    string `json:"ink"`
	} `json:"palette"`
	Assets struct {
		Portrait string `json:"portrait"`
		Sprite   string `json:"sprite"`
		Static   string `json:"static"`
	} `json:"assets"`
}

func toDTO(ch charactercatalog.Character) characterDTO {
	var d characterDTO
	d.ID = string(ch.ID)
	d.EntryVersion = ch.EntryVersion
	d.Kind = string(ch.Kind)
	d.Name = ch.Name
	d.Family = ch.Family
	d.FamilyLabel = ch.FamilyLabel
	d.Purpose = ch.Purpose
	d.Description = ch.Description
	d.Silhouette = ch.Silhouette
	d.Prop = ch.Prop
	d.IdleBehavior = ch.IdleBehavior
	d.ToneTraits = append([]string{}, ch.ToneTraits...)
	d.SampleLine = ch.SampleLine
	for _, r := range ch.Roles {
		d.Roles = append(d.Roles, string(r))
	}
	d.Palette.Base = ch.Palette.Base
	d.Palette.Accent = ch.Palette.Accent
	d.Palette.Ink = ch.Palette.Ink
	// Asset paths are returned as browser URLs, matching the /characters/ route.
	d.Assets.Portrait = "/" + ch.Assets.Portrait
	d.Assets.Sprite = "/" + ch.Assets.Sprite
	d.Assets.Static = "/" + ch.Assets.Static
	return d
}

// ServeCatalog handles GET /api/characters.
//
// It returns the guide identity and the assignable working characters as two
// separate lists rather than one mixed array, so a client cannot accidentally
// render Ori as a selectable option for an agent (FR-19/FR-28/FR-71).
func (h *Handler) ServeCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if match := r.Header.Get("If-None-Match"); match == h.etag || match == "*" {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", h.etag)
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")

	working := h.catalog.Working()
	items := make([]characterDTO, 0, len(working))
	for _, ch := range working {
		items = append(items, toDTO(ch))
	}

	orihttp.WriteJSON(w, map[string]any{
		"catalog_version":   h.catalog.Version,
		"reserved_guide_id": string(h.catalog.ReservedGuideID),
		"guide":             toDTO(h.catalog.Guide()),
		"characters":        items,
	})
}
