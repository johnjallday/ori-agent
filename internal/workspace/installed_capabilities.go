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
const (
	CapabilityFileJanitor   = "file-janitor"
	CapabilitySampleLibrary = "sample-library"
)

// Well-known install sources (FR-5). The set is not closed: any normalized
// non-empty string is accepted so a future blueprint or preset can name itself
// without a change here. These constants exist so the in-repo writers agree.
const (
	CapabilityOwnerPlugin = "plugin"

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

// Resource kinds a capability can own. They are stable strings because they are
// persisted; the set is closed and compiled.
const (
	ResourceDirectoryReference = "directory_reference"
	ResourceMCPBinding         = "mcp_binding"
	ResourceSkillBinding       = "skill_binding"
	ResourceWatcher            = "watcher"
	ResourceSchedule           = "schedule"
	ResourceCompanionAgent     = "companion_agent"
)

// CapabilityResource records one resource a capability created or uses.
//
// It exists so removal and relink can answer "may I release this?" from
// recorded fact rather than from a display name (PRD §9.5). Names are a
// terrible ownership signal: a user can rename an agent, two features can pick
// the same binding alias, and a directory reference called "Downloads" says
// nothing about who created it.
type CapabilityResource struct {
	// Kind is one of the Resource* constants above.
	Kind string `json:"kind"`
	// ID is the resource's stable identifier within its own collection — a
	// directory reference ID, an MCP binding ID, a trigger ID, an agent
	// instance ID. Never a path, alias, or display name.
	ID string `json:"id"`
	// Shared marks a resource the capability uses but did not create
	// exclusively for itself. Removal releases the ASSOCIATION with a shared
	// resource and leaves the resource in place for its other owners; an
	// exclusively-owned resource may be removed outright (FR-27).
	Shared bool `json:"shared,omitempty"`
}

// Valid reports whether the resource record is usable.
func (r CapabilityResource) Valid() bool {
	return normalizeResourceKind(r.Kind) != "" && strings.TrimSpace(r.ID) != ""
}

func normalizeResourceKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case ResourceDirectoryReference:
		return ResourceDirectoryReference
	case ResourceMCPBinding:
		return ResourceMCPBinding
	case ResourceSkillBinding:
		return ResourceSkillBinding
	case ResourceWatcher:
		return ResourceWatcher
	case ResourceSchedule:
		return ResourceSchedule
	case ResourceCompanionAgent:
		return ResourceCompanionAgent
	default:
		// An unknown kind is dropped rather than preserved: acting on a
		// resource class this build does not understand is exactly the kind of
		// thing removal must not attempt.
		return ""
	}
}

// CapabilityOwner is inert provenance for a non-built-in capability. Commands,
// paths, endpoints, operation bindings, and plugin generations remain in the
// trusted global plugin registries and are never persisted here.
type CapabilityOwner struct {
	Kind          string `json:"kind"`
	PluginID      string `json:"plugin_id"`
	PluginVersion string `json:"plugin_version,omitempty"`
}

func (o CapabilityOwner) Clone() CapabilityOwner { return o }

func (o CapabilityOwner) Normalize() CapabilityOwner {
	o.Kind = strings.ToLower(strings.TrimSpace(o.Kind))
	o.PluginID = NormalizeCapabilityID(o.PluginID)
	o.PluginVersion = strings.TrimSpace(o.PluginVersion)
	return o
}

func (o CapabilityOwner) Valid() bool {
	o = o.Normalize()
	return o.Kind == CapabilityOwnerPlugin && o.PluginID != "" && o.PluginVersion != ""
}

func (o CapabilityOwner) MatchesPlugin(pluginID string) bool {
	o = o.Normalize()
	return o.Kind == CapabilityOwnerPlugin && o.PluginID == NormalizeCapabilityID(pluginID)
}

// InstalledCapability is the persisted record that one Workspace Capability
// is installed on one workspace (PRD FR-4, FR-5).
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
	// Owner is nil for compiled built-ins. Plugin-backed records retain only
	// inert owner/version provenance so another plugin can never claim the same
	// local capability ID from a workspace file.
	Owner *CapabilityOwner `json:"owner,omitempty"`
	// OwnedResources records the workspace resources this capability created or
	// associated itself with, so removal and relink can release exactly the
	// right ones (FR-27). Empty until setup grants something: installing alone
	// creates no resources.
	OwnedResources []CapabilityResource `json:"owned_resources,omitempty"`
	// RemovedAt turns this record into a TOMBSTONE: the capability is not
	// installed, and was deliberately removed by the user.
	//
	// The record is kept rather than deleted because "removed" and "never
	// installed" are different facts, and only one of them should stop the
	// startup migration from installing it. Downloads Janitor's template
	// provenance survives an uninstall, so without this marker the next boot
	// would helpfully re-install the capability the user had just removed
	// (FR-30, FR-126).
	//
	// It reuses the existing installed_capabilities column, so it needs no
	// schema change. Every lookup below treats a tombstoned record as absent.
	RemovedAt *time.Time `json:"removed_at,omitempty"`
}

// Active reports whether this record represents a live install, as opposed to
// the tombstone left behind by a removal.
func (c InstalledCapability) Active() bool { return c.RemovedAt == nil }

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

// Clone returns a deep copy of the record, including its owned-resource list.
func (c InstalledCapability) Clone() InstalledCapability {
	cp := c
	if c.Owner != nil {
		owner := c.Owner.Clone()
		cp.Owner = &owner
	}
	if c.OwnedResources != nil {
		cp.OwnedResources = append([]CapabilityResource(nil), c.OwnedResources...)
	}
	return cp
}

// Owns reports whether this capability records the given resource, and whether
// it owns it exclusively.
//
// Exclusively-owned resources may be removed on uninstall; shared ones only
// lose this capability's association (FR-27).
func (c InstalledCapability) Owns(kind, id string) (exclusive bool, recorded bool) {
	wantKind := normalizeResourceKind(kind)
	wantID := strings.TrimSpace(id)
	if wantKind == "" || wantID == "" {
		return false, false
	}
	for _, resource := range c.OwnedResources {
		if normalizeResourceKind(resource.Kind) == wantKind && strings.TrimSpace(resource.ID) == wantID {
			return !resource.Shared, true
		}
	}
	return false, false
}

// ResourcesOfKind returns every recorded resource of one kind.
func (c InstalledCapability) ResourcesOfKind(kind string) []CapabilityResource {
	wantKind := normalizeResourceKind(kind)
	if wantKind == "" {
		return nil
	}
	var out []CapabilityResource
	for _, resource := range c.OwnedResources {
		if normalizeResourceKind(resource.Kind) == wantKind {
			out = append(out, resource)
		}
	}
	return out
}

// withResource returns a copy of the record with the resource recorded,
// replacing any existing entry for the same kind and ID.
func (c InstalledCapability) withResource(resource CapabilityResource) InstalledCapability {
	cp := c.Clone()
	resource.Kind = normalizeResourceKind(resource.Kind)
	resource.ID = strings.TrimSpace(resource.ID)
	if !resource.Valid() {
		return cp
	}
	for i, existing := range cp.OwnedResources {
		if normalizeResourceKind(existing.Kind) == resource.Kind && strings.TrimSpace(existing.ID) == resource.ID {
			cp.OwnedResources[i] = resource
			return cp
		}
	}
	cp.OwnedResources = append(cp.OwnedResources, resource)
	return cp
}

// normalizeResources drops unusable entries and collapses duplicates.
func normalizeResources(resources []CapabilityResource) []CapabilityResource {
	if len(resources) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(resources))
	out := make([]CapabilityResource, 0, len(resources))
	for _, resource := range resources {
		resource.Kind = normalizeResourceKind(resource.Kind)
		resource.ID = strings.TrimSpace(resource.ID)
		if !resource.Valid() {
			continue
		}
		key := resource.Kind + "\x00" + resource.ID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, resource)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	if c.Owner != nil && !c.Owner.Valid() {
		return fmt.Errorf("installed capability %q: plugin owner is invalid", c.ID)
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
	if c.Owner != nil {
		owner := c.Owner.Normalize()
		if owner.Valid() {
			c.Owner = &owner
		} else {
			// Preserve the capability record but retain an unusable owner marker;
			// resolution will fail closed rather than treating it as a built-in.
			c.Owner = &owner
		}
	}
	c.OwnedResources = normalizeResources(c.OwnedResources)
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
		if NormalizeCapabilityID(c.ID) != key {
			continue
		}
		// A tombstone is a record of a removal, not an install. Everything that
		// asks "is this installed?" must read it as no.
		if !c.Active() {
			return InstalledCapability{}, false
		}
		return c.Clone(), true
	}
	return InstalledCapability{}, false
}

// FindCapabilityRecord returns the raw record for id, tombstone included. It
// exists for the removal/reinstall paths, which need to see a removal that
// FindInstalledCapability deliberately hides.
func FindCapabilityRecord(caps []InstalledCapability, id string) (InstalledCapability, bool) {
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

// GetInstalledCapabilities returns a deep copy of the workspace's ACTIVE
// install records. Callers may mutate the result freely.
//
// Tombstones are excluded. Everything downstream of this — the catalog, the
// registry resolution, the per-workspace install limit — is asking "what is
// installed here?", and a removal is not an install. The tombstones stay on the
// workspace for persistence and for CapabilityWasRemoved.
func (w *Workspace) GetInstalledCapabilities() []InstalledCapability {
	w.mu.RLock()
	defer w.mu.RUnlock()
	active := make([]InstalledCapability, 0, len(w.InstalledCapabilities))
	for _, c := range w.InstalledCapabilities {
		if c.Active() {
			active = append(active, c)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return CloneInstalledCapabilities(active)
}

// AllCapabilityRecords returns every record including tombstones, for the
// persistence layer and for code that needs to see removals.
func (w *Workspace) AllCapabilityRecords() []InstalledCapability {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return CloneInstalledCapabilities(w.InstalledCapabilities)
}

// DiscardInstalledCapability deletes a record outright, leaving no tombstone.
//
// This is for rolling back an install that failed partway: that install never
// happened, so it must leave nothing behind — least of all a marker saying the
// user removed something they never had, which would then suppress the very
// migration that should install it.
func (w *Workspace) DiscardInstalledCapability(id string) bool {
	key := NormalizeCapabilityID(id)
	if key == "" {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	remaining := make([]InstalledCapability, 0, len(w.InstalledCapabilities))
	discarded := false
	for _, c := range w.InstalledCapabilities {
		if NormalizeCapabilityID(c.ID) == key {
			discarded = true
			continue
		}
		remaining = append(remaining, c)
	}
	if !discarded {
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

	// A tombstone is replaced wholesale, not revived. Reinstalling is a fresh
	// install: it must not inherit the old install's timestamp, source, or —
	// above all — its owned resources, which were released when it was removed.
	// Requiring a new folder choice is the point (FR-30).
	for i, existing := range w.InstalledCapabilities {
		if NormalizeCapabilityID(existing.ID) == c.ID {
			w.InstalledCapabilities[i] = c
			w.capabilitiesExplicit = true
			w.UpdatedAt = time.Now()
			return true, nil
		}
	}

	w.InstalledCapabilities = append(w.InstalledCapabilities, c)
	w.capabilitiesExplicit = true
	w.UpdatedAt = time.Now()
	return true, nil
}

// RecordCapabilityResource associates a workspace resource with an installed
// capability, so a later relink or uninstall can release exactly the right
// things (FR-27).
//
// Recording is idempotent: repeating it for the same kind and ID updates the
// entry rather than adding a second. It is a no-op — reported as false — when
// the capability is not installed, because a resource owned by nothing is a
// record nobody would ever act on.
func (w *Workspace) RecordCapabilityResource(capabilityID string, resource CapabilityResource) bool {
	key := NormalizeCapabilityID(capabilityID)
	resource.Kind = normalizeResourceKind(resource.Kind)
	resource.ID = strings.TrimSpace(resource.ID)
	if key == "" || !resource.Valid() {
		return false
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for i, record := range w.InstalledCapabilities {
		if NormalizeCapabilityID(record.ID) != key {
			continue
		}
		updated := record.withResource(resource)
		if len(updated.OwnedResources) == len(record.OwnedResources) {
			// Same count: either replaced in place or rejected. Compare the
			// entry to decide whether anything actually changed.
			if exclusive, ok := record.Owns(resource.Kind, resource.ID); ok && exclusive == !resource.Shared {
				return false
			}
		}
		w.InstalledCapabilities[i] = updated
		w.capabilitiesExplicit = true
		w.UpdatedAt = time.Now()
		return true
	}
	return false
}

// ForgetCapabilityResource drops a resource association. Used when a relink
// replaces a directory reference, or when removal releases a shared resource.
func (w *Workspace) ForgetCapabilityResource(capabilityID, kind, id string) bool {
	key := NormalizeCapabilityID(capabilityID)
	wantKind := normalizeResourceKind(kind)
	wantID := strings.TrimSpace(id)
	if key == "" || wantKind == "" || wantID == "" {
		return false
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for i, record := range w.InstalledCapabilities {
		if NormalizeCapabilityID(record.ID) != key {
			continue
		}
		remaining := make([]CapabilityResource, 0, len(record.OwnedResources))
		removed := false
		for _, resource := range record.OwnedResources {
			if normalizeResourceKind(resource.Kind) == wantKind && strings.TrimSpace(resource.ID) == wantID {
				removed = true
				continue
			}
			remaining = append(remaining, resource)
		}
		if !removed {
			return false
		}
		updated := record.Clone()
		if len(remaining) == 0 {
			remaining = nil
		}
		updated.OwnedResources = remaining
		w.InstalledCapabilities[i] = updated
		w.capabilitiesExplicit = true
		w.UpdatedAt = time.Now()
		return true
	}
	return false
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

	removed := false
	now := time.Now()
	for i, c := range w.InstalledCapabilities {
		if NormalizeCapabilityID(c.ID) != key || !c.Active() {
			continue
		}
		removed = true
		// Tombstone rather than delete. The owned resources go, because they
		// have been released and a stale list would make a later removal try to
		// release them twice; the fact of the removal stays.
		w.InstalledCapabilities[i].RemovedAt = &now
		w.InstalledCapabilities[i].OwnedResources = nil
	}
	if !removed {
		return false
	}
	w.capabilitiesExplicit = true
	w.UpdatedAt = now
	return true
}

// CapabilityWasRemoved reports whether the user deliberately removed this
// capability from this workspace.
//
// The startup migration consults it: a workspace can still carry the legacy
// signals that made it a migration candidate — above all the built-in template
// it was created from, which no uninstall can change — and re-installing on
// that basis would undo the user's decision every time Ori restarts (FR-30).
func (w *Workspace) CapabilityWasRemoved(id string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	record, ok := FindCapabilityRecord(w.InstalledCapabilities, id)
	return ok && !record.Active()
}
