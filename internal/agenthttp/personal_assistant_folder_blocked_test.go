package agenthttp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// The placement answer for a destination that is missing because its
// integration is not set up names that integration from the reader and
// attaches its guided setup; it never shows a key, and it offers nothing when
// the registry cannot name what is missing.
func TestFolderPlacement_BlockedDestinationNamesTheIntegrationAndOffersItsSetup(t *testing.T) {
	f, discovery := newReviewedFixture(t, loomNotInstalled())
	blocker := &personalassistant.FolderDestinationBlocker{IntegrationKey: "ori_loom", HomeProviderKey: "loom_homes"}
	message, actions, ok := f.handler.folderDestinationBlockedChoice(context.Background(), "local", blocker)
	if !ok || !strings.Contains(message, "Loom Studio") || !strings.Contains(message, "not installed yet") || !strings.Contains(message, "Guided Setup") {
		t.Fatal("blocked answer does not name the integration and its setup", ok, message)
	}
	if strings.Contains(message, "ori_loom") || strings.Contains(message, "loom_homes") {
		t.Fatal("internal key shown", message)
	}
	if len(actions) != 1 || actions[0].Type != HomeActionOpenSetupQuest || actions[0].Label != "Open Guided Setup: Loom Studio" || actions[0].Arguments["quest_id"] != "install_ori_loom" || actions[0].Href != "" {
		t.Fatal("setup button missing or wrong", actions)
	}
	// Installed and enabled, with the plugin's own setup still open.
	discovery.integrations = []assistantdiscovery.ReviewedIntegration{{
		Key: "ori_loom", DisplayName: "Loom Studio", Installed: true, Enabled: true, InstalledVersion: "1.2.0",
		SetupQuest: &assistantdiscovery.SetupQuestRef{Source: assistantdiscovery.SetupQuestSourcePlugin, PluginID: "loom-plugin", ID: "loom_setup"},
	}}
	message, actions, ok = f.handler.folderDestinationBlockedChoice(context.Background(), "local", blocker)
	if !ok || !strings.Contains(message, "guided setup is not finished") || len(actions) != 1 || actions[0].Label != "Continue setup: Loom Studio" || actions[0].Arguments["plugin_id"] != "loom-plugin" {
		t.Fatal("continue-setup answer wrong", ok, message, actions)
	}
	// Nothing left to set up for the integration: the Home provider is what is
	// missing. A listed provider is named, without a button.
	provider := reviewedintegration.HomeProviders()[0]
	discovery.integrations = []assistantdiscovery.ReviewedIntegration{{Key: "ori_loom", DisplayName: "Loom Studio", Installed: true, Enabled: true, SetupComplete: true}}
	message, actions, ok = f.handler.folderDestinationBlockedChoice(context.Background(), "local", &personalassistant.FolderDestinationBlocker{IntegrationKey: "ori_loom", HomeProviderKey: provider.Key})
	if !ok || !strings.Contains(message, provider.DisplayName) || len(actions) != 0 {
		t.Fatal("provider answer wrong", ok, message, actions)
	}
	// Keys the registry does not list, or no blocker at all: the generic
	// sentence stands.
	for _, unknown := range []*personalassistant.FolderDestinationBlocker{nil, {}, {IntegrationKey: "ori_other", HomeProviderKey: "nowhere"}} {
		if message, actions, ok := f.handler.folderDestinationBlockedChoice(context.Background(), "local", unknown); ok || message != "" || actions != nil {
			t.Fatal("unknown blocker answered", unknown, message, actions)
		}
	}
	// A reader that cannot answer names no integration, but a listed provider
	// is still named.
	discovery.integrations = nil
	f.handler.Discovery = discovery.researchSourceFixture
	message, actions, ok = f.handler.folderDestinationBlockedChoice(context.Background(), "local", &personalassistant.FolderDestinationBlocker{IntegrationKey: "ori_loom", HomeProviderKey: provider.Key})
	if !ok || !strings.Contains(message, provider.DisplayName) || len(actions) != 0 {
		t.Fatal("provider not named without a reader", ok, message, actions)
	}
}

// On Home the handler requests the page's own workspace by default, and that
// bound read is a plain miss. The unbound read, which looked for the Home the
// project needs, still names what to set up; a supporting link never does.
func TestFolderPlacement_BlockerFallsBackToTheUnboundReadForADefaultCreate(t *testing.T) {
	blocker := &personalassistant.FolderDestinationBlocker{IntegrationKey: "ori_loom", HomeProviderKey: "loom_homes"}
	bound := personalassistant.FolderOfferView{DestinationStatus: "unavailable", DestinationBlocker: blocker}
	plainMiss := personalassistant.FolderOfferView{DestinationStatus: "unavailable"}
	unbound := personalassistant.FolderOfferView{DestinationStatus: "unavailable", DestinationBlocker: blocker}
	available := personalassistant.FolderOfferView{Destination: &personalassistant.FolderSetupDestination{Status: "standalone"}}
	if got := placementBlocker(bound, available, personalassistant.FolderOperationCreate); got != blocker {
		t.Fatal("bound blocker not preferred", got)
	}
	if got := placementBlocker(plainMiss, unbound, personalassistant.FolderOperationCreate); got != blocker {
		t.Fatal("default create did not fall back to the unbound read", got)
	}
	if got := placementBlocker(plainMiss, unbound, personalassistant.FolderOperationSupport); got != nil {
		t.Fatal("supporting link blamed on the Home", got)
	}
	if got := placementBlocker(plainMiss, available, personalassistant.FolderOperationCreate); got != nil {
		t.Fatal("available unbound read invented a blocker", got)
	}
}

func TestFolderPlacement_ChoiceCarriesActionsOnlyWhenAttached(t *testing.T) {
	decode := func(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var body struct {
			Choice map[string]any `json:"folder_placement_choice"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Choice == nil {
			t.Fatal("placement choice not written", rec.Body.String(), err)
		}
		return body.Choice
	}
	rec := httptest.NewRecorder()
	writeFolderPlacementChoice(rec, "candidate-0", "Harbor Lights", "Choose.", []folderPlacementOption{{Operation: personalassistant.FolderOperationCreate, Label: "Review a separate project"}}, nil)
	plain := decode(t, rec)
	if plain["readiness"] != nil {
		t.Fatal("legacy choice invented readiness", plain)
	}
	if _, present := plain["actions"]; present || plain["candidate_id"] != "candidate-0" || plain["subject"] != "Harbor Lights" || plain["message"] != "Choose." || len(plain["options"].([]any)) != 1 {
		t.Fatal("plain choice shape changed", plain)
	}
	rec = httptest.NewRecorder()
	writeFolderPlacementChoice(rec, "candidate-0", "Harbor Lights", "Set up first.", nil, []HomeAction{{ID: "open-setup-quest-ori_loom", Type: HomeActionOpenSetupQuest, Label: "Open Guided Setup: Loom Studio", Arguments: map[string]any{"quest_id": "install_ori_loom", "source": "host"}}})
	blocked := decode(t, rec)
	actions, _ := blocked["actions"].([]any)
	if len(actions) != 1 || actions[0].(map[string]any)["type"] != HomeActionOpenSetupQuest || blocked["options"] != nil {
		t.Fatal("blocked choice shape wrong", blocked)
	}
	rec = httptest.NewRecorder()
	writeFolderPlacementChoice(rec, "candidate-0", "Harbor Lights", "Check requirements.", nil, nil, &personalassistant.FolderSetupVisual{
		RequirementsChecked: true,
		Plugins:             []personalassistant.FolderSetupPlugin{{Kind: "provider", Name: "Music", Operation: "unavailable", Source: "/private/source"}},
	})
	partial := decode(t, rec)
	readiness, _ := partial["readiness"].(map[string]any)
	if readiness == nil || readiness["requirements_checked"] == true || partial["actions"] != nil || partial["plan"] != nil || strings.Contains(rec.Body.String(), "/private/source") {
		t.Fatal("partial display gained authority or leaked a path", partial)
	}
}
