package grouprequirements

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Group Templates project one canonical Home per source-scoped program key.
// These characterize the existing boundaries the projection must reuse rather
// than replace: a ready variant shares its plugin source's Home, while another
// plugin, a user setup attachment, or an unowned local copy never does.
func TestProgramHomeKeysAreSourceScoped(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	plugin := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)

	pluginHome := claimHomeOnly(t, service, plugin, "plugin-home")
	if pluginHome.Kind != "group" || pluginHome.GetTemplateProvenance() != nil || len(pluginHome.GetAgentInstances()) != 0 {
		t.Fatalf("Home-only claim created more than an unstaffed group: kind=%q provenance=%#v agents=%d",
			pluginHome.Kind, pluginHome.GetTemplateProvenance(), len(pluginHome.GetAgentInstances()))
	}

	variant := plugin
	variant.ID = "neutral-variant"
	variant.PluginOwner = nil
	variant.TemplateVariant = &projecttemplates.TemplateVariant{
		SchemaVersion: 1, VariantID: "variant-one", OwnerUserID: "owner-1",
		Source: projecttemplates.TemplateVariantSource{
			PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1,
			DefinitionDigest: strings.Repeat("c", 64),
		},
	}
	if got := service.Evaluate(testInput(t, variant, CompositionGrouped, false)); got.State != StateReadyGrouped || got.HomeWorkspaceID != pluginHome.ID {
		t.Fatalf("variant evaluation = %#v, want its plugin source Home %q", got, pluginHome.ID)
	}

	otherPlugin := plugin
	otherPlugin.ID = "plugin:other-neutral:project"
	owner := *plugin.PluginOwner
	owner.PluginID = "other-neutral"
	otherPlugin.PluginOwner = &owner
	if got := service.Evaluate(testInput(t, otherPlugin, CompositionGrouped, false)); got.State != StateHomeCreationReviewRequired || got.HomeWorkspaceID != "" {
		t.Fatalf("same program ID from another plugin reused a Home: %#v", got)
	}

	attachment := plugin
	attachment.ID = "local-project"
	attachment.PluginOwner = nil
	attachment.UserSetupQuest = &projecttemplates.UserSetupQuest{AttachmentID: "attachment-1"}
	evaluation := service.Evaluate(testInput(t, attachment, CompositionGrouped, false))
	if evaluation.State != StateHomeCreationReviewRequired || evaluation.HomeWorkspaceID != "" || evaluation.ProgramKey == nil ||
		evaluation.ProgramKey.PluginID != "" || evaluation.ProgramKey.TemplateID != "local-project" || evaluation.ProgramKey.AttachmentID != "attachment-1" {
		t.Fatalf("user attachment did not use its own namespace: %#v", evaluation)
	}
	attachmentHome := claimHomeOnly(t, service, attachment, "attachment-home")
	if attachmentHome.ID == pluginHome.ID {
		t.Fatalf("user attachment claimed the plugin Home %q", pluginHome.ID)
	}
	if got := service.Evaluate(testInput(t, plugin, CompositionGrouped, false)); got.HomeWorkspaceID != pluginHome.ID {
		t.Fatalf("plugin Home changed after attachment Home creation: %#v", got)
	}

	localCopy := plugin
	localCopy.ID = "local-copy"
	localCopy.PluginOwner = nil
	if got := service.Evaluate(testInput(t, localCopy, CompositionGrouped, false)); got.State != StateSourceUnavailable || got.HomeWorkspaceID != "" {
		t.Fatalf("unowned local copy resolved a Home: %#v", got)
	}

	mixed := workspace.AssistantProgramKey{
		OwnerUserID: "owner-1", PluginID: "neutral", TemplateID: "local-project", AttachmentID: "attachment-1", ProgramID: "neutral-program",
	}
	if mixed.Valid() {
		t.Fatal("a key claiming both plugin and user-template ownership must be invalid")
	}

	ids, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("workspaces = %v, want exactly the plugin and attachment Homes", ids)
	}
}

func claimHomeOnly(t *testing.T, service *Service, template projecttemplates.Template, idempotencyKey string) *workspace.Workspace {
	t.Helper()
	input := testHomeInput(t, template)
	review, err := service.Review(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(context.Background(), input, review.Token, idempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	if !claim.HomeCreated || claim.Operation.ChildWorkspaceID != "" || claim.Operation.ProjectLinkID != "" {
		t.Fatalf("Home-only claim = %#v, want a newly created Home and no project identity", claim)
	}
	home, err := service.workspaces.Get(claim.Operation.HomeWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return home
}
