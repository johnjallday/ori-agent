package folderagent

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func TestProjectHoldersViewCapsRowsAndCountsRemainder(t *testing.T) {
	root := build(t, "A/one/go.mod", "A/two/go.mod", "B/Song.rpp", "C/go.mod", "D/outline.md", "E/go.mod")
	h := downloadsHarness(t, root)
	v := h.start(t, "")
	view, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	holders := view.ProjectHolders
	if holders == nil || len(holders.Rows) != 3 || holders.More != 2 || holders.Rows[0].Name != "A" || holders.Rows[0].Count != 2 || holders.Rows[0].Kinds != "2 software projects" {
		t.Fatal("incorrect holder list", holders)
	}
}

func TestChooseProjectHolderAcceptsOnlyTheSavedOwnedWalk(t *testing.T) {
	for _, mode := range []string{"valid", "other owner", "stale", "absent index", "root index", "vanished", "symlink", "unrecorded folder"} {
		t.Run(mode, func(t *testing.T) {
			root := build(t, "Projects/One/go.mod", "Projects/Two/Song.rpp")
			h := downloadsHarness(t, root)
			v := h.start(t, "")
			view, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
			if view.ProjectHolders == nil {
				t.Fatal("missing holder")
			}
			key, index, owner := view.ProjectHolders.ReportKey, view.ProjectHolders.Rows[0].Index, "u1"
			switch mode {
			case "other owner":
				owner = "foreign"
			case "stale":
				key = "old"
			case "absent index":
				index = 99999
			case "root index":
				index = 0
			case "vanished", "symlink":
				if err := os.Rename(filepath.Join(root, "Projects"), filepath.Join(root, "Gone")); err != nil {
					t.Fatal(err)
				}
				if mode == "symlink" {
					if err := os.Symlink(t.TempDir(), filepath.Join(root, "Projects")); err != nil {
						t.Fatal(err)
					}
				}
			case "unrecorded folder":
				if err := os.Mkdir(filepath.Join(root, "Other"), 0750); err != nil {
					t.Fatal(err)
				}
				index = -1
			}
			before, _ := h.svc.deps.Store.Read(v.WorkspaceID)
			count := len(h.ws.anchors)
			card, err := h.svc.ChooseProjectHolder(t.Context(), owner, v.WorkspaceID, key, index)
			after, _ := h.svc.deps.Store.Read(v.WorkspaceID)
			if !reflect.DeepEqual(before, after) || len(h.ws.anchors) != count {
				t.Fatal("opening the card created or changed a workspace")
			}
			if mode == "valid" {
				if err != nil || card.FolderName != "Projects" || card.Hire.AgentName != "Projects Manager" {
					t.Fatal("not a normal hire card", card, err)
				}
				held, err := h.svc.takeChoice("u1", card.Token)
				if err != nil || held.path != filepath.Join(root, "Projects") {
					t.Fatal("incorrect host-held folder", held, err)
				}
			} else if err == nil || card != nil {
				t.Fatal("accepted invalid child", card, err)
			}
			if mode == "other owner" && !errors.Is(err, ErrNotOwner) {
				t.Fatal("owner check not applied", err)
			}
		})
	}
}

func TestProjectHolderCardRefusesMapChangedSinceDisplay(t *testing.T) {
	root := build(t, "Projects/A/go.mod")
	h := downloadsHarness(t, root)
	v := h.start(t, "")
	view, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	_, err := h.svc.deps.Store.Update(v.WorkspaceID, func(r *Record) error {
		r.Map.Folders = append(r.Map.Folders, foldermap.Folder{Path: "Later", State: foldermap.StateUnopened})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ChooseProjectHolder(t.Context(), "u1", v.WorkspaceID, view.ProjectHolders.ReportKey, view.ProjectHolders.Rows[0].Index); !errors.Is(err, ErrWrongState) {
		t.Fatal("stale walk accepted", err)
	}
}
