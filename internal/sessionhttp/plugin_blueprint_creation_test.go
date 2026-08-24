package sessionhttp

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

type blueprintComponentReconciler struct{ attachErr error }

func (r blueprintComponentReconciler) AttachCapability(string, workspacecapability.Definition) error {
	return r.attachErr
}
func (blueprintComponentReconciler) DetachCapability(string, workspacecapability.Definition) error {
	return nil
}

func pluginBlueprintCreationFixture(t *testing.T, attachErr error) (*Handler, *workspace.FileStore, projecttemplates.Template, string) {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Plugin Workspace"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	registry, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	owner := workspace.CapabilityOwner{
		Kind: workspace.CapabilityOwnerPlugin, PluginID: "demo-plugin", PluginVersion: "1.0.0",
	}
	if err := registry.RegisterPluginDefinitions(owner, []workspacecapability.Definition{{
		ID: "demo-tools", Version: 1, Owner: &owner,
		Display: workspacecapability.Display{Name: "Demo Tools"},
	}}); err != nil {
		t.Fatal(err)
	}
	service := workspacecapability.NewService(registry, store)
	service.SetPluginComponentReconciler(blueprintComponentReconciler{attachErr: attachErr})
	handler := &Handler{workspaceTaskStore: store, templateCapabilityService: service}
	blueprintOwner := &workspace.PluginTemplateOwner{
		PluginID: "demo-plugin", PluginVersion: "1.0.0", BlueprintID: "starter", BlueprintVersion: 2,
	}
	template := projecttemplates.Template{
		ID: "plugin:demo-plugin:starter", Name: "Plugin Starter", PluginOwner: blueprintOwner,
		Capabilities: []projecttemplates.CapabilityInstall{{ID: "demo-tools", Source: "plugin-blueprint"}},
	}
	return handler, store, template, ws.ID
}

func TestPluginBlueprintCreationSnapshotsProvenanceAndCapabilityAtomically(t *testing.T) {
	handler, store, template, workspaceID := pluginBlueprintCreationFixture(t, nil)
	if warning := handler.persistCreateWorkspaceTemplateProvenance(workspaceID, template, true); warning != "" {
		t.Fatalf("warning = %q", warning)
	}
	stored, err := store.Get(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	provenance := stored.GetTemplateProvenance()
	record, installed := stored.GetInstalledCapability("demo-tools")
	if provenance == nil || provenance.PluginOwner == nil || provenance.Version != 2 || !installed || record.Owner == nil {
		t.Fatalf("provenance=%+v capability=%+v installed=%v", provenance, record, installed)
	}
	if provenance.PluginOwner.PluginID != record.Owner.PluginID {
		t.Fatalf("owner drift: provenance=%+v record=%+v", provenance.PluginOwner, record.Owner)
	}
}

func TestPluginBlueprintCreationReportsRecoverableAttachmentFailure(t *testing.T) {
	handler, store, template, workspaceID := pluginBlueprintCreationFixture(t, errors.New("binding store unavailable"))
	warning := handler.persistCreateWorkspaceTemplateProvenance(workspaceID, template, true)
	if warning == "" {
		t.Fatal("component failure was reported as normal ready creation")
	}
	stored, err := store.Get(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GetTemplateProvenance() == nil {
		t.Fatal("recoverable failure lost inert blueprint provenance")
	}
	if _, ok := stored.GetInstalledCapability("demo-tools"); !ok {
		t.Fatal("recoverable failure lost the capability record needed for retry")
	}
}
