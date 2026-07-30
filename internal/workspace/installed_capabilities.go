package workspace

import (
	"fmt"
	"strings"
	"time"
)

// CapabilityFileJanitor is the stable built-in Workspace Capability identifier
// for File Janitor (PRD FR-1). It lives in this package rather than the
// capability registry so workspace-level code (persistence, migration, station
// derivation) can reference the ID without importing the registry — and so the
// registry may import this package instead of the other way around.
const CapabilityFileJanitor = "file-janitor"

// Well-known install sources (FR-5). The set is not closed: any normalized
// non-empty string is accepted so a future blueprint or preset can name itself
// without a change here. These constants exist so the in-repo writers agree.
const (
	// InstallSourceInPlace is a user installing the capability into an existing
	// workspace from the Capabilities catalog (FR-19).
	InstallSourceInPlace = "in-place"
	// InstallSourceBlueprint is a capability declared by a blueprint and
	// installed as part of Create Workspace (FR-32).
	InstallSourceBlueprint = "blueprint"
	// InstallSourceLegacyMigration is the additive backfill of an authoritative
	// legacy Downloads Janitor workspace (FR-125).
	InstallSourceLegacyMigration = "legacy-migration"
)

// InstalledCapability is the persisted record that one built-in Workspace
// Capability is installed on one workspace (PRD FR-4, FR-5).
//
// It is deliberately inert metadata: an ID that may only resolve to a
// definition compiled into the running Ori build, the definition version that
// was installed, when it happened, and where the install came from. It carries
// no executable reference, no path, no URL, and no runtime health — health is
// always derived from the capability service at read time (FR-6), never trusted
// from a persisted string.
//
// This type is unrelated to CapabilityMapping (capability_mapping.go), which
// binds semantic connector operations onto an MCP server's tools and lives on
// MCPBinding. The two never share a field, a column, or a JSON key (FR-7).
type InstalledCapability struct {
	// ID is the stable built-in capability identifier, e.g. CapabilityFileJanitor.
	// Normalized to lower case so a hand-edited workspace.json cannot produce
	// two records that differ only in case.
	ID string `json:"id"`
	// Version is the installed definition version, used to drive
	// capability-specific migration without changing workspace schema
	// semantics (FR-13). Always >= 1 for a valid record.
	Version int `json:"version"`
	// InstalledAt is when the install record was created.
	InstalledAt time.Time `json:"installed_at"`
	// Source records which flow performed the install (FR-5). See the
	// InstallSource* constants.
	Source string `json:"source,omitempty"`
}

// NormalizeCapabilityID trims and lower-cases a capability identifier. Callers
// comparing a persisted ID against a registry key must route both sides through
// this so lookups cannot miss on spelling.
func NormalizeCapabilityID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// normalizeInstallSource trims and lower-cases an install source. An empty
// source stays empty rather than acquiring a fabricated default: it is
// provenance metadata, and inventing a value would misreport how an install
// happened.
func normalizeInstallSource(source string) string {
	return strings.ToLower(strings.TrimSpace(source))
}

// Clone returns a copy of the record. InstalledCapability holds no maps,
// slices, or pointers today, so a value copy is a deep copy — the method
// exists so later fields (e.g. companion-agent association metadata) can be
// deep-copied without auditing every caller.
func (c InstalledCapability) Clone() InstalledCapability {
	return c
}

// Validate reports whether the record is complete enough to be written as a new
// install (FR-5). It is the strict check used on the install path; decoding a
// persisted workspace uses the more forgiving normalizeInstalledCapability
// below, which must never erase a real install over missing provenance.
func (c InstalledCapability) Validate() error {
	if NormalizeCapabilityID(c.ID) == "" {
		return fmt.Errorf("installed capability: id is required")
	}
	if c.Version < 1 {
		return fmt.Errorf("installed capability %q: version must be positive, got %d", c.ID, c.Version)
	}
	if c.InstalledAt.IsZero() {
		return fmt.Errorf("installed capability %q: installed_at is required", c.ID)
	}
	if normalizeInstallSource(c.Source) == "" {
		return fmt.Errorf("installed capability %q: source is required", c.ID)
	}
	return nil
}

// normalizeInstalledCapability canonicalizes one record and reports whether it
// is usable at all.
//
// The bar here is deliberately lower than Validate: this runs on every decode
// of every workspace, so dropping a record means erasing a user's install. Only
// the two fields that make a record structurally unusable are grounds for a
// drop — an ID that resolves to nothing, and a version that cannot select a
// definition. A missing installed_at or source is incomplete provenance, not a
// phantom install, and is preserved as-is.
func normalizeInstalledCapability(c InstalledCapability) (InstalledCapability, bool) {
	c.ID = NormalizeCapabilityID(c.ID)
	c.Source = normalizeInstallSource(c.Source)
	if c.ID == "" || c.Version < 1 {
		return InstalledCapability{}, false
	}
	return c, true
}

// CloneInstalledCapabilities returns a deep copy of the collection. A nil input
// yields nil so a caller cannot turn "no data" into "known empty" — the
// distinction matters at every merge site (see the len()==0 guards in
// SyncStore.Save and mergePortableWorkspaceState).
func CloneInstalledCapabilities(caps []InstalledCapability) []InstalledCapability {
	if caps == nil {
		return nil
	}
	out := make([]InstalledCapability, len(caps))
	for i, c := range caps {
		out[i] = c.Clone()
	}
	return out
}

// NormalizeInstalledCapabilities canonicalizes IDs and sources, drops
// structurally unusable records, and enforces one record per capability ID
// (FR-8). First-seen wins, matching NormalizeCapabilityMappings; relative order
// of the surviving records is preserved so the collection stays stable across
// save cycles and diffs cleanly.
//
// A nil input yields nil, and an input whose records are all dropped yields nil
// rather than an empty slice, so the result never asserts "this workspace is
// known to have no capabilities" on the strength of garbage.
func NormalizeInstalledCapabilities(caps []InstalledCapability) []InstalledCapability {
	if len(caps) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(caps))
	out := make([]InstalledCapability, 0, len(caps))
	for _, raw := range caps {
		c, ok := normalizeInstalledCapability(raw)
		if !ok {
			continue
		}
		if _, duplicate := seen[c.ID]; duplicate {
			continue
		}
		seen[c.ID] = struct{}{}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FindInstalledCapability returns the record for id and whether it is present.
// Both sides of the comparison are normalized.
func FindInstalledCapability(caps []InstalledCapability, id string) (InstalledCapability, bool) {
	key := NormalizeCapabilityID(id)
	if key == "" {
		return InstalledCapability{}, false
	}
	for _, c := range caps {
		if NormalizeCapabilityID(c.ID) == key {
			return c.Clone(), true
		}
	}
	return InstalledCapability{}, false
}

// NormalizeInstalledCapabilities canonicalizes the workspace's collection in
// place. Called from FromJSON so a hand-edited or partially-written
// workspace.json cannot introduce duplicate or unusable install records into
// the rest of the system.
func (w *Workspace) NormalizeInstalledCapabilities() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.InstalledCapabilities = NormalizeInstalledCapabilities(w.InstalledCapabilities)
}

// GetInstalledCapabilities returns a deep copy of the workspace's install
// records. Callers may mutate the result freely.
func (w *Workspace) GetInstalledCapabilities() []InstalledCapability {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return CloneInstalledCapabilities(w.InstalledCapabilities)
}

// SetInstalledCapabilities replaces the whole collection, normalizing it first.
//
// This is the only helper that can shrink the collection, and it exists for the
// store/adapter layer (which reconstructs the field wholesale from persisted
// JSON). Feature code should prefer AddInstalledCapability /
// RemoveInstalledCapability, which are idempotent and report what changed.
func (w *Workspace) SetInstalledCapabilities(caps []InstalledCapability) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.InstalledCapabilities = NormalizeInstalledCapabilities(CloneInstalledCapabilities(caps))
	w.capabilitiesExplicit = true
	w.UpdatedAt = time.Now()
}

// InstalledCapabilitiesExplicit reports whether this in-memory workspace has had
// its capability collection deliberately edited. The store layer consults it to
// tell an intentional uninstall from a record that simply never loaded the
// field; see the capabilitiesExplicit doc comment on Workspace.
func (w *Workspace) InstalledCapabilitiesExplicit() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.capabilitiesExplicit
}

// GetInstalledCapability returns the workspace's record for id, if installed.
func (w *Workspace) GetInstalledCapability(id string) (InstalledCapability, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return FindInstalledCapability(w.InstalledCapabilities, id)
}

// HasInstalledCapability reports whether id is installed on this workspace.
func (w *Workspace) HasInstalledCapability(id string) bool {
	_, ok := w.GetInstalledCapability(id)
	return ok
}

// AddInstalledCapability records an install, idempotently (FR-9).
//
// It is designed to be called inside a Store.Update closure, which is the only
// safe way to write this field during workspace creation — the create flow
// hands one workspace struct through several whole-struct workspace.json writes
// before template application, so an install set on that struct would be
// overwritten. See tasks/trace-installed-capabilities-persistence.md (H5).
//
// Returns true when a new record was appended, false when the capability was
// already installed (in which case the existing record, including its original
// installed_at and source, is left untouched — a repeat install must not
// rewrite provenance). A malformed record is rejected with an error and changes
// nothing.
func (w *Workspace) AddInstalledCapability(c InstalledCapability) (bool, error) {
	c.ID = NormalizeCapabilityID(c.ID)
	c.Source = normalizeInstallSource(c.Source)
	if c.InstalledAt.IsZero() {
		c.InstalledAt = time.Now()
	}
	if err := c.Validate(); err != nil {
		return false, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Normalize first so a pre-existing duplicate or unusable record cannot
	// hide the ID we are about to check for.
	w.InstalledCapabilities = NormalizeInstalledCapabilities(w.InstalledCapabilities)
	if _, exists := FindInstalledCapability(w.InstalledCapabilities, c.ID); exists {
		return false, nil
	}

	w.InstalledCapabilities = append(w.InstalledCapabilities, c)
	w.capabilitiesExplicit = true
	w.UpdatedAt = time.Now()
	return true, nil
}

// RemoveInstalledCapability drops the record for id and reports whether
// anything was removed. Idempotent: removing an absent capability is a no-op
// returning false.
//
// Removing a record marks the collection as deliberately edited, which is what
// lets the write survive SyncStore.Save's stale-write guard. Without that mark
// the guard could not tell an uninstall from a record that never loaded its
// capabilities, and would restore the removed install from workspace.json — see
// the capabilitiesExplicit doc comment on Workspace, and hazard H6 in
// tasks/trace-installed-capabilities-persistence.md.
//
// Removing the record is still only the model-level half of uninstall:
// FR-24-FR-27 also require stopping automation and releasing access first.
func (w *Workspace) RemoveInstalledCapability(id string) bool {
	key := NormalizeCapabilityID(id)
	if key == "" {
		return false
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	remaining := make([]InstalledCapability, 0, len(w.InstalledCapabilities))
	removed := false
	for _, c := range w.InstalledCapabilities {
		if NormalizeCapabilityID(c.ID) == key {
			removed = true
			continue
		}
		remaining = append(remaining, c)
	}
	if !removed {
		return false
	}
	if len(remaining) == 0 {
		remaining = nil
	}
	w.InstalledCapabilities = remaining
	w.capabilitiesExplicit = true
	w.UpdatedAt = time.Now()
	return true
}
