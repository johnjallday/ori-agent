package workspace

import (
	"errors"
	"testing"
)

type projectLinkMirrorFixture struct {
	Store
	folder   *Workspace
	mirrored bool
	err      error
}

func (s projectLinkMirrorFixture) GetMirrorWorkspace(_ string) (*Workspace, bool, error) {
	return s.folder, s.mirrored, s.err
}

func TestAssistantProjectLinkMirrorsAgreeRefusesPartialChildWrites(t *testing.T) {
	primary := NewInMemoryStore()
	child := NewWorkspace(CreateWorkspaceParams{Name: "Selected Song"})
	child.OwnerUserID = "local"
	child.ParentID = "home"
	child.SetAssistantProjectLink(&AssistantProjectLink{
		ID: "exact-link", StationWorkspaceID: "home", StateRevision: 1,
		ProjectRoles:    []AssistantProgramRoleSpec{{ID: "reaper-assistant", Scope: AssistantRoleScopeProject, Required: true}},
		ProjectProvider: &AssistantProjectProviderOwner{PluginID: "reaper-plugin", ComponentFingerprint: "original"},
	})
	if err := primary.Save(child); err != nil {
		t.Fatal(err)
	}
	if !AssistantProjectLinkMirrorsAgree(primary, child) {
		t.Fatal("one-store child with a link should not require an invented mirror")
	}
	copyChild := func() *Workspace {
		t.Helper()
		folder, err := cloneWorkspaceForRebind(child)
		if err != nil {
			t.Fatal(err)
		}
		return folder
	}
	folder := copyChild()
	mirror := projectLinkMirrorFixture{Store: primary, folder: folder, mirrored: true}
	if !AssistantProjectLinkMirrorsAgree(mirror, child) {
		t.Fatal("identical primary and folder links were refused")
	}
	for name, change := range map[string]func(*Workspace){
		"missing saved roles": func(ws *Workspace) {
			link := ws.GetAssistantProjectLink()
			link.ProjectRoles = nil
			ws.SetAssistantProjectLink(link)
		},
		"changed provider pin": func(ws *Workspace) {
			link := ws.GetAssistantProjectLink()
			link.ProjectProvider.ComponentFingerprint = "replacement"
			ws.SetAssistantProjectLink(link)
		},
		"wrong parent":   func(ws *Workspace) { ws.ParentID = "other-home" },
		"wrong owner":    func(ws *Workspace) { ws.OwnerUserID = "other-owner" },
		"wrong identity": func(ws *Workspace) { ws.ID = "other-child" },
		"wrong status":   func(ws *Workspace) { ws.Status = StatusTrashed },
	} {
		t.Run(name, func(t *testing.T) {
			split := copyChild()
			change(split)
			mirror.folder = split
			if AssistantProjectLinkMirrorsAgree(mirror, child) {
				t.Fatal("split child was trusted as a connected link")
			}
		})
	}
	mirror.folder = nil
	if AssistantProjectLinkMirrorsAgree(mirror, child) {
		t.Fatal("missing folder mirror was trusted")
	}
	mirror.mirrored, mirror.err = false, errors.New("folder read failed")
	if AssistantProjectLinkMirrorsAgree(mirror, child) {
		t.Fatal("failed mirror lookup was treated as no mirror")
	}
	unlinked := copyChild()
	unlinked.SetAssistantProjectLink(nil)
	if AssistantProjectLinkMirrorsAgree(primary, unlinked) {
		t.Fatal("unlinked child was treated as a verified link")
	}
}
