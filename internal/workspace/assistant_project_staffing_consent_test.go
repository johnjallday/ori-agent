package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var consentDigest = strings.Repeat("a", 64)

func testConsent() *ProjectStaffingConsent {
	return &ProjectStaffingConsent{
		GrantedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Source: ProjectStaffingConsentFolderOffer, OfferID: "offer-1",
		PluginID: "ori-reaper", BlueprintID: "reaper-song", TeamDigest: consentDigest,
		Roles: []ProjectStaffingConsentRole{{RoleID: "reaper-assistant"}},
	}
}

func TestProjectStaffingConsentValidate(t *testing.T) {
	if err := testConsent().Validate(); err != nil {
		t.Fatalf("valid consent: %v", err)
	}
	revokedEarly := testConsent()
	before := revokedEarly.GrantedAt.Add(-time.Minute)
	revokedEarly.RevokedAt = &before
	cases := map[string]*ProjectStaffingConsent{
		"nil":            nil,
		"no time":        func() *ProjectStaffingConsent { c := testConsent(); c.GrantedAt = time.Time{}; return c }(),
		"unknown source": func() *ProjectStaffingConsent { c := testConsent(); c.Source = "agent"; return c }(),
		"no plugin":      func() *ProjectStaffingConsent { c := testConsent(); c.PluginID = ""; return c }(),
		"short digest":   func() *ProjectStaffingConsent { c := testConsent(); c.TeamDigest = "abc"; return c }(),
		"upper digest":   func() *ProjectStaffingConsent { c := testConsent(); c.TeamDigest = strings.Repeat("A", 64); return c }(),
		"no roles":       func() *ProjectStaffingConsent { c := testConsent(); c.Roles = nil; return c }(),
		"duplicate role": func() *ProjectStaffingConsent {
			c := testConsent()
			c.Roles = append(c.Roles, ProjectStaffingConsentRole{RoleID: "reaper-assistant"})
			return c
		}(),
		"upper role id":   func() *ProjectStaffingConsent { c := testConsent(); c.Roles[0].RoleID = "Reaper"; return c }(),
		"name with break": func() *ProjectStaffingConsent { c := testConsent(); c.Roles[0].AgentName = "A\nB"; return c }(),
		"long name": func() *ProjectStaffingConsent {
			c := testConsent()
			c.Roles[0].AgentName = strings.Repeat("x", 81)
			return c
		}(),
		"revoked before": revokedEarly,
	}
	for name, consent := range cases {
		if err := consent.Validate(); !errors.Is(err, ErrProjectStaffingConsentInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDecideProjectStaffing(t *testing.T) {
	agents := map[string]bool{"REAPER Assistant": true, "Mix Helper": true}
	exists := func(name string) bool { return agents[name] }
	takenBy := func(names ...string) func(string) bool {
		return func(name string) bool {
			for _, taken := range names {
				if strings.EqualFold(taken, name) {
					return true
				}
			}
			return false
		}
	}
	facts := func() ProjectStaffingFacts {
		return ProjectStaffingFacts{
			PluginID: "ori-reaper", BlueprintID: "reaper-song", TeamDigest: consentDigest,
			RoleID: "reaper-assistant", RoleLabel: "REAPER Assistant", AgentExists: exists, NameTaken: takenBy(),
		}
	}
	revoked := testConsent()
	at := revoked.GrantedAt.Add(time.Hour)
	revoked.RevokedAt = &at
	named := testConsent()
	named.Roles[0].AgentName = "REAPER Assistant"
	gone := testConsent()
	gone.Roles[0].AgentName = "REAPER Assistant 3"
	invalid := testConsent()
	invalid.Source = ""

	cases := []struct {
		name    string
		consent *ProjectStaffingConsent
		facts   func(ProjectStaffingFacts) ProjectStaffingFacts
		want    ProjectStaffingDecision
	}{
		{"no consent", nil, nil, ProjectStaffingDecision{Action: ProjectStaffingSkip, Reason: ProjectStaffingNoConsent}},
		{"unreadable consent", invalid, nil, ProjectStaffingDecision{Action: ProjectStaffingSkip, Reason: ProjectStaffingNoConsent}},
		{"switched off", revoked, nil, ProjectStaffingDecision{Action: ProjectStaffingSkip, Reason: ProjectStaffingConsentRevoked}},
		{"another blueprint", testConsent(), func(f ProjectStaffingFacts) ProjectStaffingFacts { f.BlueprintID = "ableton-set"; return f },
			ProjectStaffingDecision{Action: ProjectStaffingSkip, Reason: ProjectStaffingNotCovered}},
		{"another role", testConsent(), func(f ProjectStaffingFacts) ProjectStaffingFacts { f.RoleID = "mixer"; return f },
			ProjectStaffingDecision{Action: ProjectStaffingSkip, Reason: ProjectStaffingNotCovered}},
		{"stale digest", named, func(f ProjectStaffingFacts) ProjectStaffingFacts { f.TeamDigest = strings.Repeat("b", 64); return f },
			ProjectStaffingDecision{Action: ProjectStaffingStop, Reason: ProjectStaffingConsentStale}},
		{"first create", testConsent(), nil, ProjectStaffingDecision{Action: ProjectStaffingCreate, AgentName: "REAPER Assistant"}},
		{"bind the recorded agent", named, nil, ProjectStaffingDecision{Action: ProjectStaffingBind, AgentName: "REAPER Assistant"}},
		{"recorded agent is gone", gone, nil,
			ProjectStaffingDecision{Action: ProjectStaffingStop, Reason: ProjectStaffingAssistantMissing, AgentName: "REAPER Assistant 3"}},
		// D3: an unrelated agent already called "REAPER Assistant" is never adopted.
		{"name taken by a stranger", testConsent(), func(f ProjectStaffingFacts) ProjectStaffingFacts {
			f.NameTaken = takenBy("reaper assistant")
			return f
		}, ProjectStaffingDecision{Action: ProjectStaffingCreate, AgentName: "REAPER Assistant 2"}},
		{"the next free name", testConsent(), func(f ProjectStaffingFacts) ProjectStaffingFacts {
			f.NameTaken = takenBy("REAPER Assistant", "REAPER Assistant 2")
			return f
		}, ProjectStaffingDecision{Action: ProjectStaffingCreate, AgentName: "REAPER Assistant 3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := facts()
			if tc.facts != nil {
				input = tc.facts(input)
			}
			if got := DecideProjectStaffing(tc.consent, input); got != tc.want {
				t.Fatalf("decision = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestFirstFreeAgentNameIsBounded(t *testing.T) {
	if got := FirstFreeAgentName("", nil); got != "" {
		t.Fatalf("empty label = %q", got)
	}
	long := strings.Repeat("x", 90)
	if got := FirstFreeAgentName(long, nil); len([]rune(got)) != 80 {
		t.Fatalf("long label = %d runes", len([]rune(got)))
	}
	if got := FirstFreeAgentName(long, func(name string) bool { return len(name) == 80 && !strings.HasSuffix(name, " 2") }); !strings.HasSuffix(got, " 2") || len(got) != 80 {
		t.Fatalf("long label with suffix = %q", got)
	}
	if got := FirstFreeAgentName("Busy", func(string) bool { return true }); got != "" {
		t.Fatalf("no free name = %q", got)
	}
}

func consentHome(t *testing.T, store Store) string {
	t.Helper()
	home := &Workspace{ID: "music-home", Name: "Music Home", OwnerUserID: "local", Status: StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	home.SetAssistantProgramState(&AssistantProgramState{
		SchemaVersion: AssistantProgramStateSchemaVersion, PluginAvailable: true,
		Key: AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
	})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	return home.ID
}

func reaperGrant() ProjectStaffingConsentGrant {
	return ProjectStaffingConsentGrant{
		Source: ProjectStaffingConsentFolderOffer, OfferID: "offer-1", PluginID: "ori-reaper", BlueprintID: "reaper-song",
		TeamDigest: consentDigest, RoleIDs: []string{"reaper-assistant"},
	}
}

func TestProjectStaffingConsentsGrantRecordRevoke(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consents := NewProjectStaffingConsents(store)
	if consent, err := consents.Read(homeID); err != nil || consent != nil {
		t.Fatalf("a new Home has no consent: %+v %v", consent, err)
	}
	granted, err := consents.Grant(homeID, reaperGrant())
	if err != nil || !granted.Active() || granted.Roles[0].AgentName != "" {
		t.Fatalf("grant = %+v %v", granted, err)
	}
	// Granting again is the same grant: nothing changes, the time stays.
	version := mustVersion(t, store, homeID)
	again, err := consents.Grant(homeID, reaperGrant())
	if err != nil || !again.GrantedAt.Equal(granted.GrantedAt) || mustVersion(t, store, homeID) != version {
		t.Fatalf("second grant rewrote the Home: %+v %v", again, err)
	}
	// Only a reserved name can be recorded.
	if err := consents.RecordAgent(homeID, "ori-reaper", "reaper-song", consentDigest, "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	if read, _ := consents.Read(homeID); read.Roles[0].AgentName != "" {
		t.Fatalf("an unreserved name was recorded: %+v", read)
	}
	if err := consents.ReserveAgent(homeID, "ori-reaper", "reaper-song", consentDigest, "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	if err := consents.RecordAgent(homeID, "ori-reaper", "reaper-song", consentDigest, "reaper-assistant", "Someone Else"); err != nil {
		t.Fatal(err)
	}
	if read, _ := consents.Read(homeID); read.Roles[0].AgentName != "" || read.Roles[0].PendingName != "REAPER Assistant" {
		t.Fatalf("a name other than the reserved one was recorded: %+v", read)
	}
	if err := consents.RecordAgent(homeID, "ori-reaper", "reaper-song", consentDigest, "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	// Once recorded, nothing can be reserved over it.
	if err := consents.ReserveAgent(homeID, "ori-reaper", "reaper-song", consentDigest, "reaper-assistant", "REAPER Assistant 2"); !errors.Is(err, ErrProjectStaffingConsentInvalid) {
		t.Fatalf("reserved over a recorded agent: %v", err)
	}
	// A name already recorded is never replaced, and another blueprint or
	// digest records nothing.
	for _, call := range [][]string{
		{"ori-reaper", "reaper-song", consentDigest, "reaper-assistant", "REAPER Assistant 2"},
		{"ori-reaper", "reaper-song", strings.Repeat("b", 64), "reaper-assistant", "Other"},
		{"ori-ableton", "ableton-set", consentDigest, "reaper-assistant", "Other"},
	} {
		if err := consents.RecordAgent(homeID, call[0], call[1], call[2], call[3], call[4]); err != nil {
			t.Fatal(err)
		}
	}
	read, err := consents.Read(homeID)
	if err != nil || read.Roles[0].AgentName != "REAPER Assistant" || read.Roles[0].PendingName != "" {
		t.Fatalf("recorded agent = %+v %v", read, err)
	}
	revoked, err := consents.Revoke(homeID)
	if err != nil || revoked.RevokedAt == nil || revoked.Active() {
		t.Fatalf("revoke = %+v %v", revoked, err)
	}
	// Switching it back on is a fresh consent that keeps the agent it created.
	switched := reaperGrant()
	switched.Source, switched.OfferID = ProjectStaffingConsentHomeSwitch, ""
	fresh, err := consents.Grant(homeID, switched)
	if err != nil || !fresh.Active() || fresh.Source != ProjectStaffingConsentHomeSwitch || fresh.Roles[0].AgentName != "REAPER Assistant" {
		t.Fatalf("re-grant = %+v %v", fresh, err)
	}
	// A plugin update that changes the team is a fresh consent too, still with
	// the same agent for the role it keeps.
	updated := reaperGrant()
	updated.TeamDigest = strings.Repeat("c", 64)
	refreshed, err := consents.Grant(homeID, updated)
	if err != nil || refreshed.TeamDigest != updated.TeamDigest || refreshed.Roles[0].AgentName != "REAPER Assistant" {
		t.Fatalf("stale re-grant = %+v %v", refreshed, err)
	}
	// A consent for another blueprint never inherits this one's agent.
	other := reaperGrant()
	other.PluginID, other.BlueprintID = "ori-ableton", "ableton-set"
	replaced, err := consents.Grant(homeID, other)
	if err != nil || replaced.Roles[0].AgentName != "" {
		t.Fatalf("another blueprint adopted an agent: %+v %v", replaced, err)
	}
}

func TestProjectStaffingConsentsRefuseAProjectAndBadGrants(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	project := &Workspace{ID: "song", Name: "Song", Status: StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	project.SetAssistantProjectLink(&AssistantProjectLink{StationWorkspaceID: homeID})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	consents := NewProjectStaffingConsents(store)
	if _, err := consents.Grant(project.ID, reaperGrant()); !errors.Is(err, ErrAssistantStationNotFound) {
		t.Fatalf("a project is not a Home: %v", err)
	}
	bad := reaperGrant()
	bad.TeamDigest = "nope"
	if _, err := consents.Grant(homeID, bad); !errors.Is(err, ErrProjectStaffingConsentInvalid) {
		t.Fatalf("invalid grant: %v", err)
	}
	if consent, _ := consents.Read(homeID); consent != nil {
		t.Fatalf("an invalid grant was stored: %+v", consent)
	}
}

func TestProjectStaffingConsentCloneIsDeep(t *testing.T) {
	consent := testConsent()
	at := consent.GrantedAt.Add(time.Minute)
	consent.RevokedAt = &at
	state := &AssistantProgramState{ProjectStaffingConsent: consent}
	clone := CloneAssistantProgramState(state)
	clone.ProjectStaffingConsent.Roles[0].AgentName = "changed"
	*clone.ProjectStaffingConsent.RevokedAt = at.Add(time.Hour)
	if consent.Roles[0].AgentName != "" || !consent.RevokedAt.Equal(at) {
		t.Fatalf("clone aliased the consent: %+v", consent)
	}
}

func mustVersion(t *testing.T, store Store, id string) int64 {
	t.Helper()
	ws, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return ws.Version
}
