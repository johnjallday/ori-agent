package assistantsetup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeWorkspaceSource struct {
	items []*workspace.Workspace
	err   error
}

func (f fakeWorkspaceSource) ListActive() ([]*workspace.Workspace, error) { return f.items, f.err }

func janitorWorkspace(id, owner, name string) *workspace.Workspace {
	ws := &workspace.Workspace{ID: id, OwnerUserID: owner, Name: name, FolderSlug: id, Status: workspace.StatusActive}
	ws.SetInstalledCapabilities([]workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now()}})
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: BlueprintID, Builtin: true, Version: 2,
		SetupWizard: &workspace.SetupWizard{Version: 1, Steps: []workspace.SetupWizardStep{{ID: "folder", Kind: "directory", Adapter: "file_janitor"}}},
	})
	return ws
}

func TestCanonicalCandidateResolverUsesOwnedActiveCapabilityIdentity(t *testing.T) {
	owned := janitorWorkspace("owned", "local", "Owned")
	legacyOwner := janitorWorkspace("legacy", "", "Legacy local")
	foreign := janitorWorkspace("foreign", "other", "Foreign")
	group := janitorWorkspace("group", "local", "Group")
	group.Kind = "group"
	hq := janitorWorkspace("hq", "local", "HQ")
	hq.Designation = "personal_hq"
	trashed := janitorWorkspace("trashed", "local", "Trashed")
	trashed.Status = workspace.StatusTrashed
	noCapability := janitorWorkspace("plain", "local", "Plain")
	noCapability.SetInstalledCapabilities(nil)
	custom := janitorWorkspace("custom", "local", "Custom")
	custom.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "custom-template", Version: 1})

	resolver := NewCanonicalCandidateResolver(fakeWorkspaceSource{items: []*workspace.Workspace{
		foreign, group, hq, trashed, noCapability, custom, legacyOwner, owned,
	}})
	targets, err := resolver.Resolve(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 || targets[0].WorkspaceID != "custom" || targets[1].WorkspaceID != "legacy" || targets[2].WorkspaceID != "owned" {
		t.Fatalf("targets = %+v", targets)
	}
	if targets[0].Supported || targets[0].Reason != "custom_workspace_requires_review" {
		t.Fatalf("custom target = %+v", targets[0])
	}
	if !targets[1].Supported || !targets[2].Supported {
		t.Fatalf("supported targets = %+v", targets)
	}
}

func TestCanonicalCandidateResolverDistinguishesUnavailableFromNone(t *testing.T) {
	resolver := NewCanonicalCandidateResolver(fakeWorkspaceSource{err: errors.New("disk unavailable")})
	if _, err := resolver.Resolve(context.Background(), "local"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
