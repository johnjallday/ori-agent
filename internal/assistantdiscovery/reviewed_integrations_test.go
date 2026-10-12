package assistantdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

type fakePluginStore struct {
	installed []plugin.InstalledPlugin
	err       error
	reads     int
}

func (f *fakePluginStore) List() ([]plugin.InstalledPlugin, error) {
	f.reads++
	return f.installed, f.err
}

// The registry in these tests is made up, so every name the reader returns can
// only have come from an entry.
func fakeRegistry() []reviewedintegration.Entry {
	return []reviewedintegration.Entry{
		{Key: "ori_loom", PluginID: "loom-plugin", DisplayName: "Loom Studio", MinimumVersion: "1.2.0", SupportedPlatforms: []string{"darwin/arm64"}, ReleaseReady: true},
		{Key: "ori_draft", PluginID: "draft-plugin", DisplayName: "Draft Desk", MinimumVersion: "0.3.0", SupportedPlatforms: []string{"darwin/arm64"}, ReleaseReady: false},
		{Key: "ori_winonly", PluginID: "winonly-plugin", DisplayName: "Winonly", MinimumVersion: "2.0.0", SupportedPlatforms: []string{"windows/amd64"}, ReleaseReady: true},
		{Key: "ori_blank", PluginID: "blank-plugin", DisplayName: "  ", SupportedPlatforms: []string{"darwin/arm64"}, ReleaseReady: true},
	}
}

type questCall struct{ userID, key string }

func reviewedService(store *fakePluginStore, quest func(context.Context, string, string) (PluginSetupQuest, bool)) *Service {
	service := NewService(nil, nil, nil, nil)
	service.ConfigureReviewedIntegrations(ReviewedIntegrationSources{
		Entries: fakeRegistry, Plugins: store, Platform: "darwin/arm64", PluginQuest: quest,
	})
	return service
}

func only(t *testing.T, result ReviewedIntegrationsResult) ReviewedIntegration {
	t.Helper()
	if result.Status != "available" || len(result.Integrations) != 1 {
		t.Fatalf("result=%+v, want exactly the one release-ready entry for this platform", result)
	}
	return result.Integrations[0]
}

func TestReviewedIntegrations_NotInstalledOffersTheInstallQuest(t *testing.T) {
	asked := 0
	service := reviewedService(&fakePluginStore{}, func(context.Context, string, string) (PluginSetupQuest, bool) {
		asked++
		return PluginSetupQuest{}, false
	})
	got := only(t, service.ReviewedIntegrations(context.Background(), "local"))
	want := ReviewedIntegration{
		Key: "ori_loom", DisplayName: "Loom Studio", MinimumReviewedVersion: "1.2.0",
		SetupQuest: &SetupQuestRef{Source: SetupQuestSourceHost, ID: "install_ori_loom"},
	}
	if got.Key != want.Key || got.DisplayName != want.DisplayName || got.MinimumReviewedVersion != want.MinimumReviewedVersion ||
		got.Installed || got.Enabled || got.SetupComplete || got.InstalledVersion != "" ||
		got.SetupQuest == nil || *got.SetupQuest != *want.SetupQuest {
		t.Fatalf("integration=%+v quest=%+v", got, got.SetupQuest)
	}
	if asked != 0 {
		t.Fatal("a plugin that is not installed was asked for its own quest")
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"key":"ori_loom"`, `"display_name":"Loom Studio"`, `"installed":false`, `"enabled":false`, `"minimum_reviewed_version":"1.2.0"`, `"setup_complete":false`, `"setup_quest":{"source":"host","id":"install_ori_loom"}`} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("JSON %s lacks %s", encoded, field)
		}
	}
}

func TestReviewedIntegrations_InstalledButDisabledStaysOnTheInstallQuest(t *testing.T) {
	store := &fakePluginStore{installed: []plugin.InstalledPlugin{{Name: " Loom-Plugin ", Version: "1.4.0", Enabled: false}}}
	got := only(t, reviewedService(store, nil).ReviewedIntegrations(context.Background(), "local"))
	if !got.Installed || got.InstalledVersion != "1.4.0" || got.Enabled || got.SetupComplete ||
		got.SetupQuest == nil || *got.SetupQuest != (SetupQuestRef{Source: SetupQuestSourceHost, ID: "install_ori_loom"}) {
		t.Fatalf("integration=%+v quest=%+v", got, got.SetupQuest)
	}
}

func TestReviewedIntegrations_InstalledAndEnabledContinuesInThePluginsOwnQuest(t *testing.T) {
	store := &fakePluginStore{installed: []plugin.InstalledPlugin{
		{Name: "loom-plugin", Version: "1.4.0", Enabled: true},
		{Name: "unrelated-plugin", Version: "9.9.9", Enabled: true},
	}}
	var calls []questCall
	own := PluginSetupQuest{Ref: SetupQuestRef{Source: SetupQuestSourcePlugin, PluginID: "loom-plugin", ID: "first-song"}}
	service := reviewedService(store, func(_ context.Context, userID, key string) (PluginSetupQuest, bool) {
		calls = append(calls, questCall{userID, key})
		return own, true
	})

	got := only(t, service.ReviewedIntegrations(context.Background(), "local"))
	if !got.Installed || !got.Enabled || got.InstalledVersion != "1.4.0" || got.SetupComplete ||
		got.SetupQuest == nil || *got.SetupQuest != own.Ref {
		t.Fatalf("integration=%+v quest=%+v", got, got.SetupQuest)
	}
	if len(calls) != 1 || calls[0] != (questCall{"local", "ori_loom"}) {
		t.Fatalf("plugin quest asked %v", calls)
	}

	// Finished: said so, and nothing left to open.
	own.Complete = true
	got = only(t, service.ReviewedIntegrations(context.Background(), "local"))
	if !got.SetupComplete || got.SetupQuest != nil {
		t.Fatalf("a finished setup still offers a quest: %+v quest=%+v", got, got.SetupQuest)
	}
}

func TestReviewedIntegrations_EnabledPluginWithNoQuestOffersNothing(t *testing.T) {
	store := &fakePluginStore{installed: []plugin.InstalledPlugin{{Name: "loom-plugin", Version: "1.4.0", Enabled: true}}}
	for name, quest := range map[string]func(context.Context, string, string) (PluginSetupQuest, bool){
		"no resolver": nil,
		"none listed": func(context.Context, string, string) (PluginSetupQuest, bool) { return PluginSetupQuest{}, false },
	} {
		got := only(t, reviewedService(store, quest).ReviewedIntegrations(context.Background(), "local"))
		if !got.Enabled || got.SetupComplete || got.SetupQuest != nil {
			t.Fatalf("%s: integration=%+v quest=%+v", name, got, got.SetupQuest)
		}
	}
}

// Nothing is claimed about any integration when its owners cannot be read.
func TestReviewedIntegrations_UnavailableClaimsNothing(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	unwired := NewService(nil, nil, nil, nil)
	var missing *Service
	cases := map[string]ReviewedIntegrationsResult{
		"not configured":      unwired.ReviewedIntegrations(context.Background(), "local"),
		"nil service":         missing.ReviewedIntegrations(context.Background(), "local"),
		"plugin store failed": reviewedService(&fakePluginStore{err: errors.New("store locked")}, nil).ReviewedIntegrations(context.Background(), "local"),
		"cancelled":           reviewedService(&fakePluginStore{}, nil).ReviewedIntegrations(cancelled, "local"),
	}
	for name, result := range cases {
		if result.Status != "unavailable" || result.Integrations == nil || len(result.Integrations) != 0 {
			t.Errorf("%s: result=%+v", name, result)
		}
	}
}

func TestMentionsName_WholeWordIgnoringCase(t *testing.T) {
	for text, want := range map[string]bool{
		"I make music in Loom, set up a home that knows my songs": true,
		"can you set up the LOOM integration?":                    true,
		"loom":                                                    true,
		"Is Loom's plugin installed?":                             true,
		"(loom)":                                                  true,
		"I use Loom.":                                             true,
		"heirloom tomatoes":                                       false,
		"Looming deadline":                                        false,
		"loom2 is out":                                            false,
		"my 4loom rig":                                            false,
		"":                                                        false,
		"nothing relevant":                                        false,
	} {
		if got := MentionsName(text, "Loom"); got != want {
			t.Errorf("MentionsName(%q, Loom) = %v, want %v", text, got, want)
		}
	}
	// A phrase matches across any run of whitespace, and still only whole.
	for text, want := range map[string]bool{
		"I record in loom   studio every day": true,
		"Loom\nStudio":                        true,
		"loom studios":                        false,
		"loom and studio":                     false,
	} {
		if got := MentionsName(text, " Loom  Studio "); got != want {
			t.Errorf("MentionsName(%q, Loom Studio) = %v, want %v", text, got, want)
		}
	}
	if MentionsName("anything", "  ") {
		t.Error("a blank name matched")
	}
}

// Naming is a registry read: it never touches the plugin store, and it names
// only what this host would offer.
func TestReviewedIntegrationsNamedIn_ReadsTheRegistryOnly(t *testing.T) {
	store := &fakePluginStore{}
	service := reviewedService(store, nil)
	if got := service.ReviewedIntegrationsNamedIn("I make music in loom studio"); len(got) != 1 || got[0] != "ori_loom" {
		t.Fatalf("named = %v", got)
	}
	// Not release-ready, not this platform, and unnamed entries are never named.
	for _, text := range []string{"open Draft Desk", "is Winonly here?", "I like looms", ""} {
		if got := service.ReviewedIntegrationsNamedIn(text); len(got) != 0 {
			t.Errorf("ReviewedIntegrationsNamedIn(%q) = %v", text, got)
		}
	}
	if store.reads != 0 {
		t.Fatalf("naming read the plugin store %d times", store.reads)
	}
	var missing *Service
	if got := missing.ReviewedIntegrationsNamedIn("loom studio"); got != nil {
		t.Fatalf("nil service named %v", got)
	}
}

func TestReviewedIntegrations_ListIsBounded(t *testing.T) {
	entries := make([]reviewedintegration.Entry, 0, MaxCandidates+4)
	for i := 0; i < MaxCandidates+4; i++ {
		key := "ori_entry_" + string(rune('a'+i))
		entries = append(entries, reviewedintegration.Entry{Key: key, PluginID: key + "-plugin", DisplayName: "Entry " + string(rune('A'+i)), SupportedPlatforms: []string{"linux/amd64"}, ReleaseReady: true})
	}
	service := NewService(nil, nil, nil, nil)
	service.ConfigureReviewedIntegrations(ReviewedIntegrationSources{
		Entries: func() []reviewedintegration.Entry { return entries }, Plugins: &fakePluginStore{}, Platform: "linux/amd64",
	})
	if got := service.ReviewedIntegrations(context.Background(), "local"); len(got.Integrations) != MaxCandidates {
		t.Fatalf("listed %d integrations, want the cap of %d", len(got.Integrations), MaxCandidates)
	}
}
