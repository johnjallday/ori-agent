package downloadsjanitor

import (
	"testing"
)

// TestEnsureWatcher_ReusesAnExistingLegacyRegistration is the FR-138 guarantee
// for an upgraded install.
//
// Every workspace configured before this feature already has a trigger record
// stored under the legacy name and domain. Reconciliation must recognize that
// record and UPDATE it. If the lookup key were renamed alongside the product,
// the existing record would no longer match, a second watcher would be
// installed beside it, and every folder event would be scanned twice.
//
// This is why WatchTriggerName and DomainKey keep their legacy values; see
// tasks/compat-boundary-file-janitor.md §1.2.
func TestEnsureWatcher_ReusesAnExistingLegacyRegistration(t *testing.T) {
	automation, _, root, triggers, _ := automationFixture(t)

	// A watcher as an earlier Ori version left it: right identity, stale path,
	// and a debounce the current defaults would not choose.
	legacy := TriggerRecord{
		ID:              "legacy-watcher-1",
		WorkspaceID:     "ws-1",
		Name:            WatchTriggerName,
		Domain:          DomainKey,
		Enabled:         true,
		Path:            "/somewhere/else",
		Events:          []string{"create"},
		DebounceSeconds: 42,
	}
	if _, err := triggers.Upsert(legacy); err != nil {
		t.Fatalf("seed legacy watcher: %v", err)
	}

	if err := automation.EnsureWatcher("ws-1"); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}

	records, err := triggers.List("ws-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("reconciliation created a duplicate watcher: %d records", len(records))
	}
	if records[0].ID != "legacy-watcher-1" {
		t.Fatalf("the existing registration was replaced rather than updated: id = %q", records[0].ID)
	}
	if records[0].Path != root {
		t.Fatalf("the reused registration was not repointed at the approved folder: %q", records[0].Path)
	}
}

// TestEnsureWatcher_MatchesOnDomainAloneWhenTheNameDiffers covers the other arm
// of the identity match. A record whose display name was edited (or written by
// a build with different wording) is still this capability's watcher, because
// the domain is what routes the fire.
func TestEnsureWatcher_MatchesOnDomainAloneWhenTheNameDiffers(t *testing.T) {
	automation, _, _, triggers, _ := automationFixture(t)

	if _, err := triggers.Upsert(TriggerRecord{
		ID:          "renamed-watcher",
		WorkspaceID: "ws-1",
		Name:        "Some other name entirely",
		Domain:      DomainKey,
		Enabled:     true,
		Path:        "/stale",
	}); err != nil {
		t.Fatalf("seed watcher: %v", err)
	}

	if err := automation.EnsureWatcher("ws-1"); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}

	records, _ := triggers.List("ws-1")
	if len(records) != 1 {
		t.Fatalf("expected the domain match to reuse the record, got %d", len(records))
	}
	if records[0].ID != "renamed-watcher" {
		t.Fatalf("reconciliation did not reuse the record: %q", records[0].ID)
	}
}

// TestEnsureWatcher_IsIdempotentAcrossRepeatedStartups proves repeated
// reconciliation — the shape of every server restart — converges on one
// registration rather than accumulating them.
func TestEnsureWatcher_IsIdempotentAcrossRepeatedStartups(t *testing.T) {
	automation, _, _, triggers, _ := automationFixture(t)

	for i := range 5 {
		if err := automation.EnsureWatcher("ws-1"); err != nil {
			t.Fatalf("EnsureWatcher #%d: %v", i, err)
		}
	}

	records, _ := triggers.List("ws-1")
	if len(records) != 1 {
		t.Fatalf("five reconciliations produced %d watchers, want 1", len(records))
	}
}

// TestEnsureWatcher_LeavesAnotherDomainsTriggerAlone proves the identity match
// is narrow: an unrelated workspace trigger must not be captured and repointed
// at this capability's folder.
func TestEnsureWatcher_LeavesAnotherDomainsTriggerAlone(t *testing.T) {
	automation, _, root, triggers, _ := automationFixture(t)

	if _, err := triggers.Upsert(TriggerRecord{
		ID:          "someone-elses",
		WorkspaceID: "ws-1",
		Name:        "Calendar Ops sync",
		Domain:      "calendar_ops",
		Enabled:     true,
		Path:        "/calendar/path",
	}); err != nil {
		t.Fatalf("seed foreign trigger: %v", err)
	}

	if err := automation.EnsureWatcher("ws-1"); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}

	records, _ := triggers.List("ws-1")
	if len(records) != 2 {
		t.Fatalf("expected the foreign trigger plus a new Janitor watcher, got %d", len(records))
	}
	for _, record := range records {
		if record.ID == "someone-elses" {
			if record.Path != "/calendar/path" || record.Domain != "calendar_ops" {
				t.Fatalf("another domain's trigger was modified: %+v", record)
			}
		}
		if record.Domain == DomainKey && record.Path != root {
			t.Fatalf("the Janitor watcher points at the wrong folder: %+v", record)
		}
	}
}
