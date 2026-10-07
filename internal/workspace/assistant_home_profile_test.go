package workspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func homeProfileFixture() *HomeProfile {
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	later := at.Add(time.Hour)
	return &HomeProfile{
		SchemaVersion: HomeProfileSchemaVersion, Revision: 3,
		DeclaredBy: HomeProfileDeclaredBy{PluginID: "music-project-management", Version: "0.2.0", Title: "Your studio"},
		DetectedAt: &at,
		Apps: []HomeProfileApp{
			{ID: "reaper", Name: "REAPER", Detected: true, DetectedAt: &at, Version: "7.28", ConfirmedAt: &later},
			{ID: "logic-pro", Name: "Logic Pro", Detected: true, DetectedAt: &at},
		},
		MainApp: &HomeProfileMainApp{ID: "reaper", Source: HomeProfileSourceDetected, Reason: HomeProfileMainAppLibrary, ConfirmedAt: &later},
		Templates: &HomeProfileTemplates{
			Consent: &HomeProfileTemplatesConsent{GrantedAt: at, Source: HomeProfileTemplatesFolderOffer, OfferID: "offer-1"},
			ReadAt:  &later, AppID: "reaper",
			Items: []HomeProfileTemplate{{Name: "Band Session", Kind: HomeProfileTemplateProject, File: "Band Session.RPP", ModifiedAt: &at}},
		},
		Defaults: &HomeProfileDefaults{TempoBPM: 120, TimeSignature: "4 4", SampleRateHz: 48000, BitDepth: 24,
			Source: HomeProfileSourceOwner, ConfirmedAt: &later},
		Requests: []HomeProfileRequest{{ID: "request-1", Action: "fields", Revision: 3, RecordedAt: later}},
	}
}

func TestHomeProfileFixtureIsValidAndUsesTheDocumentedKeys(t *testing.T) {
	profile := homeProfileFixture()
	if err := profile.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"schema_version":1`, `"revision":3`, `"declared_by":{"plugin_id":"music-project-management","version":"0.2.0"`,
		`"apps":[{"id":"reaper","name":"REAPER","detected":true,"detected_at":`, `"main_app":{"id":"reaper","source":"detected"`,
		`"templates":{"consent":{"granted_at":`, `"source":"folder_offer"`, `"app_id":"reaper"`,
		`"items":[{"name":"Band Session","kind":"project","file":"Band Session.RPP","modified_at":`,
		`"defaults":{"tempo_bpm":120,"time_signature":"4 4","sample_rate_hz":48000,"bit_depth":24,"source":"owner","confirmed_at":`} {
		if !strings.Contains(string(encoded), key) {
			t.Errorf("stored record lacks %s:\n%s", key, encoded)
		}
	}
}

func TestHomeProfileCloneIsDeep(t *testing.T) {
	profile := homeProfileFixture()
	clone := profile.Clone()
	moved := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	*clone.Apps[0].ConfirmedAt = moved
	*clone.MainApp.ConfirmedAt = moved
	*clone.Templates.ReadAt = moved
	clone.Templates.Consent.RevokedAt = &moved
	clone.Templates.Items[0].Name = "changed"
	*clone.Defaults.ConfirmedAt = moved
	clone.Requests[0].ID = "changed"
	if err := profile.Validate(); err != nil {
		t.Fatalf("mutating a clone damaged the original: %v", err)
	}
	if profile.Templates.Items[0].Name != "Band Session" || profile.Templates.Consent.RevokedAt != nil ||
		profile.Apps[0].ConfirmedAt.Equal(moved) || profile.Requests[0].ID != "request-1" {
		t.Fatalf("clone shares memory with the original: %+v", profile)
	}
	var none *HomeProfile
	if none.Clone() != nil || none.VisibleApps() != nil {
		t.Fatal("a missing profile clones to something")
	}
}

func TestHomeProfileLookups(t *testing.T) {
	profile := homeProfileFixture()
	profile.Apps[1].Hidden = true
	if visible := profile.VisibleApps(); len(visible) != 1 || visible[0].ID != "reaper" {
		t.Fatalf("visible apps = %+v", visible)
	}
	if app, ok := profile.App("logic-pro"); !ok || !app.Hidden {
		t.Fatalf("hidden app lookup = %+v %v", app, ok)
	}
	if _, ok := profile.App("ableton-live"); ok {
		t.Fatal("found an app that is not listed")
	}
	if request, ok := profile.Request("request-1"); !ok || request.Action != "fields" {
		t.Fatalf("request = %+v %v", request, ok)
	}
	if _, ok := profile.Request(""); ok {
		t.Fatal("an empty request id matched")
	}
}

func TestHomeProfileValidationRejections(t *testing.T) {
	tests := map[string]func(*HomeProfile){
		"schema version":            func(p *HomeProfile) { p.SchemaVersion = 2 },
		"zero revision":             func(p *HomeProfile) { p.Revision = 0 },
		"declared_by plugin":        func(p *HomeProfile) { p.DeclaredBy.PluginID = "Music Plugin" },
		"declared_by version":       func(p *HomeProfile) { p.DeclaredBy.Version = "" },
		"app id":                    func(p *HomeProfile) { p.Apps[1].ID = "Logic Pro" },
		"app name":                  func(p *HomeProfile) { p.Apps[1].Name = "" },
		"app listed twice":          func(p *HomeProfile) { p.Apps[1].ID = "reaper" },
		"detected without a time":   func(p *HomeProfile) { p.Apps[1].DetectedAt = nil },
		"hidden and confirmed":      func(p *HomeProfile) { p.Apps[0].Hidden = true },
		"main app not listed":       func(p *HomeProfile) { p.MainApp.ID = "ableton-live" },
		"main app hidden":           func(p *HomeProfile) { p.MainApp.ID = "logic-pro"; p.Apps[1].Hidden = true },
		"main app source":           func(p *HomeProfile) { p.MainApp.Source = "inferred" },
		"detected main without why": func(p *HomeProfile) { p.MainApp.Reason = "" },
		"owner main not confirmed":  func(p *HomeProfile) { *p.MainApp = HomeProfileMainApp{ID: "reaper", Source: HomeProfileSourceOwner} },
		"consent source":            func(p *HomeProfile) { p.Templates.Consent.Source = "chat" },
		"consent without a time":    func(p *HomeProfile) { p.Templates.Consent.GrantedAt = time.Time{} },
		"items after revoke":        func(p *HomeProfile) { p.Templates.Consent.RevokedAt = p.Templates.ReadAt },
		"items without consent":     func(p *HomeProfile) { p.Templates.Consent = nil },
		"items for an unlisted app": func(p *HomeProfile) { p.Templates.AppID = "ableton-live" },
		"template kind":             func(p *HomeProfile) { p.Templates.Items[0].Kind = "preset" },
		"template path":             func(p *HomeProfile) { p.Templates.Items[0].File = "/Users/me/ProjectTemplates/Band.RPP" },
		"template parent reference": func(p *HomeProfile) { p.Templates.Items[0].File = ".." },
		"template subfolder":        func(p *HomeProfile) { p.Templates.Items[0].File = "Sub/Band.RPP" },
		"template name":             func(p *HomeProfile) { p.Templates.Items[0].Name = "Band\nSession" },
		"unknown templates problem": func(p *HomeProfile) { p.Templates.Problem = "exploded" },
		"unknown empty reason":      func(p *HomeProfile) { p.Templates.EmptyReason = "elsewhere" },
		"empty reason with items":   func(p *HomeProfile) { p.Templates.EmptyReason = HomeProfileTemplatesNoFolders },
		"problem without a time":    func(p *HomeProfile) { p.Templates.Problem = HomeProfileTemplatesFailed },
		"empty defaults": func(p *HomeProfile) {
			*p.Defaults = HomeProfileDefaults{Source: HomeProfileSourceOwner, ConfirmedAt: p.Defaults.ConfirmedAt}
		},
		"detected defaults":           func(p *HomeProfile) { p.Defaults.Source = HomeProfileSourceDetected },
		"defaults not confirmed":      func(p *HomeProfile) { p.Defaults.ConfirmedAt = nil },
		"tempo too slow":              func(p *HomeProfile) { p.Defaults.TempoBPM = 39 },
		"tempo too fast":              func(p *HomeProfile) { p.Defaults.TempoBPM = 241 },
		"sample rate":                 func(p *HomeProfile) { p.Defaults.SampleRateHz = 22050 },
		"bit depth":                   func(p *HomeProfile) { p.Defaults.BitDepth = 20 },
		"time signature":              func(p *HomeProfile) { p.Defaults.TimeSignature = "4\n4" },
		"request from the future":     func(p *HomeProfile) { p.Requests[0].Revision = 4 },
		"request without an id":       func(p *HomeProfile) { p.Requests[0].ID = "" },
		"request with a wrong action": func(p *HomeProfile) { p.Requests[0].Action = "Fields!" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			profile := homeProfileFixture()
			mutate(profile)
			if err := profile.Validate(); !errors.Is(err, ErrHomeProfileInvalid) {
				t.Fatalf("err = %v, want an invalid profile", err)
			}
		})
	}
	var none *HomeProfile
	if err := none.Validate(); !errors.Is(err, ErrHomeProfileInvalid) {
		t.Fatalf("missing profile err = %v", err)
	}
}

func TestHomeProfileBoundsAreEnforced(t *testing.T) {
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	profile := homeProfileFixture()
	for len(profile.Templates.Items) <= HomeProfileMaxTemplates {
		profile.Templates.Items = append(profile.Templates.Items,
			HomeProfileTemplate{Name: "Extra", Kind: HomeProfileTemplateTrack, File: "Extra.RTrackTemplate"})
	}
	if err := profile.Validate(); !errors.Is(err, ErrHomeProfileInvalid) {
		t.Fatalf("65 templates err = %v", err)
	}
	profile = homeProfileFixture()
	for len(profile.Requests) <= HomeProfileMaxRequests {
		profile.Requests = append(profile.Requests, HomeProfileRequest{ID: "request-extra", Action: "detect", Revision: 1, RecordedAt: at})
	}
	if err := profile.Validate(); !errors.Is(err, ErrHomeProfileInvalid) {
		t.Fatalf("17 receipts err = %v", err)
	}
}

// An empty profile is what a Home has after its package first declares the
// card: nothing detected, nothing read.
func TestHomeProfileEmptyRecordIsValid(t *testing.T) {
	profile := &HomeProfile{SchemaVersion: HomeProfileSchemaVersion, Revision: 1,
		DeclaredBy: HomeProfileDeclaredBy{PluginID: "music-project-management", Version: "0.2.0"}}
	if err := profile.Validate(); err != nil {
		t.Fatalf("empty profile: %v", err)
	}
	// A package id may contain dots; an application id may not.
	profile.DeclaredBy.PluginID = "com.example.music-homes"
	if err := profile.Validate(); err != nil {
		t.Fatalf("a dotted package id: %v", err)
	}
	profile.DeclaredBy.PluginID = "music-project-management"
	// A consent on its own (granted, nothing read yet) and a recorded problem
	// are both storable.
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	profile.Templates = &HomeProfileTemplates{
		Consent: &HomeProfileTemplatesConsent{GrantedAt: at, Source: HomeProfileTemplatesHomeReview},
		AppID:   "reaper", Problem: HomeProfileTemplatesUnavailable, ProblemAt: &at,
	}
	if err := profile.Validate(); err != nil {
		t.Fatalf("consent with a recorded problem: %v", err)
	}
	// A read that listed nothing may say why; a reason without a read may not.
	profile.Apps = []HomeProfileApp{{ID: "reaper", Name: "REAPER", Detected: true, DetectedAt: &at}}
	for _, reason := range []string{HomeProfileTemplatesNoFolders, HomeProfileTemplatesAppNotFound} {
		profile.Templates = &HomeProfileTemplates{
			Consent: &HomeProfileTemplatesConsent{GrantedAt: at, Source: HomeProfileTemplatesHomeReview},
			AppID:   "reaper", ReadAt: &at, EmptyReason: reason,
		}
		if err := profile.Validate(); err != nil {
			t.Fatalf("an empty read with reason %s: %v", reason, err)
		}
		profile.Templates.ReadAt = nil
		if err := profile.Validate(); !errors.Is(err, ErrHomeProfileInvalid) {
			t.Fatalf("reason %s without a read: %v", reason, err)
		}
	}
}

// PRD FR5: state written before the profile existed loads unchanged, and the
// state decoder is lenient, so a build without the field loads a Home that has
// one (and silently drops the key on its next write of that Home).
func TestAssistantProgramStateCarriesTheHomeProfile(t *testing.T) {
	legacy := []byte(`{"schema_version":2,"state_revision":4,"key":{"owner_user_id":"local","plugin_id":"music-project-management","program_id":"music-producer-assistant"},"declaration":null}`)
	var state AssistantProgramState
	if err := json.Unmarshal(legacy, &state); err != nil {
		t.Fatalf("legacy state: %v", err)
	}
	if state.HomeProfile != nil || state.GetHomeProfile() != nil {
		t.Fatal("a state without a profile grew one")
	}
	if encoded, _ := json.Marshal(state); strings.Contains(string(encoded), "home_profile") {
		t.Fatalf("a state without a profile encodes the key: %s", encoded)
	}

	state.HomeProfile = homeProfileFixture()
	clone := CloneAssistantProgramState(&state)
	clone.HomeProfile.Apps[0].Name = "changed"
	if state.HomeProfile.Apps[0].Name != "REAPER" {
		t.Fatal("state clone shares the profile")
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded AssistantProgramState
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetHomeProfile(); got == nil || got.Revision != 3 || got.MainApp.ID != "reaper" {
		t.Fatalf("reloaded profile = %+v", got)
	}

	// An older build's struct has no such field. Its decoder accepts the key.
	var older struct {
		SchemaVersion int   `json:"schema_version"`
		StateRevision int64 `json:"state_revision"`
	}
	if err := json.Unmarshal(encoded, &older); err != nil || older.StateRevision != 4 {
		t.Fatalf("a lenient decoder refused the new key: %v", err)
	}

	// A stored profile that no longer validates reads as no profile, and is
	// left in place rather than erased.
	reloaded.HomeProfile.Revision = 0
	if reloaded.GetHomeProfile() != nil {
		t.Fatal("an invalid stored profile was served")
	}
	if CloneAssistantProgramState(&reloaded).HomeProfile == nil {
		t.Fatal("an invalid stored profile was dropped by a clone")
	}
}
