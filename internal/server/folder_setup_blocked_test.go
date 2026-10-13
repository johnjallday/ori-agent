package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A recognized song folder with no Home and no provider template is the fresh
// install the owner demoed: nothing is installed yet. The reader must still
// say "unavailable" to everyone who asks only that, and name, by key, the
// reviewed integration whose guided setup clears it.
func TestFolderDestination_MissingHomeTemplateNamesTheIntegrationToSetUp(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := reviewedintegration.HomeProviders()[0]
	f.builder.sessionHandler.SetGroupTemplateCatalog(func(string) ([]sessionhttp.GroupTemplateCatalogEntry, bool, error) {
		return nil, false, nil
	})
	host := &folderSetupHost{builder: f.builder}
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{Subject: personalassistant.FolderCandidateRecord{Shape: "audio", MarkerName: "*.rpp"}}}
	destination, err := host.ReadSetupDestination(t.Context(), req)
	var blocked *personalassistant.FolderSetupBlockedError
	if destination != nil || !errors.As(err, &blocked) || !errors.Is(err, errSetupUnavailable) {
		t.Fatal("missing Home template did not name what to set up", destination, err)
	}
	entry, ok := reviewedintegration.Get(blocked.Blocker.IntegrationKey)
	if !ok || entry.ExpectedProgramID != provider.ProgramID || blocked.Blocker.HomeProviderKey != provider.Key {
		t.Fatal("blocker names something other than the song's reviewed integration and its Home provider", blocked.Blocker)
	}
	for _, text := range []string{err.Error(), blocked.Blocker.IntegrationKey, blocked.Blocker.HomeProviderKey} {
		if strings.Contains(text, entry.DisplayName) || strings.Contains(text, provider.DisplayName) {
			t.Fatal("the reader spoke a display name; only the handler speaks from the registry", text)
		}
	}
	// A requested destination that does not exist stays a plain refusal: the
	// user asked for a specific group, not for the Home the project needs.
	req.Offer.ConversationReview = &personalassistant.FolderConversationReview{DestinationID: "some-group"}
	if _, err := host.ReadSetupDestination(t.Context(), req); !errors.Is(err, errSetupUnavailable) || errors.As(err, &blocked) {
		t.Fatal("explicit destination miss was blamed on the integration", err)
	}
	req.Offer.ConversationReview = nil
	// With the provider's template ready, the same read proposes the new Home.
	owner := workspace.AssistantProgramHomeOwner{PluginID: provider.PluginID, PluginVersion: "0.1.1", ProgramID: provider.ProgramID, HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 2, ComponentFingerprint: strings.Repeat("b", 64)}
	declaration := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: provider.ProgramID, StationName: "Music Home", Roles: []workspace.AssistantProgramRoleSpec{{ID: "producer", Label: "Producer", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate reviewed work."}}}
	template := projecttemplates.Template{ID: "plugin-home:" + provider.PluginID + ":" + provider.ProgramID, Name: "Music Home", AssistantProgram: declaration, ProgramHomeOwner: &owner, GroupRequirement: &projecttemplates.GroupRequirement{SchemaVersion: 1, Policy: projecttemplates.GroupPolicyRequired, AssistantProgramID: provider.ProgramID, DefaultHomeName: declaration.StationName}}
	f.builder.sessionHandler.SetGroupTemplateCatalog(func(string) ([]sessionhttp.GroupTemplateCatalogEntry, bool, error) {
		return []sessionhttp.GroupTemplateCatalogEntry{{Template: template, Readiness: blueprintreadiness.Ready(blueprintreadiness.OwnershipPlugin), Active: true}}, false, nil
	})
	destination, err = host.ReadSetupDestination(t.Context(), req)
	if err != nil || destination == nil || destination.Status != "new" || destination.Name != "Music Home" {
		t.Fatal("ready template no longer proposes the new Home", destination, err)
	}
	// A catalog that cannot be read at all is the host's problem, not a
	// missing integration: nothing is blamed on the user.
	f.builder.sessionHandler.SetGroupTemplateCatalog(func(string) ([]sessionhttp.GroupTemplateCatalogEntry, bool, error) {
		return nil, false, errors.New("catalog store down")
	})
	if _, err := host.ReadSetupDestination(t.Context(), req); !errors.Is(err, errSetupUnavailable) || errors.As(err, &blocked) {
		t.Fatal("unreadable catalog was blamed on the integration", err)
	}
}
