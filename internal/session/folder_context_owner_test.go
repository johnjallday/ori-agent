package session

import (
	"context"
	"testing"
)

func TestFolderContext_ShallowHQDeletionAndReassignmentClearBinding(t *testing.T) {
	for _, action := range []string{"delete_hq", "reassign", "rename"} {
		t.Run(action, func(t *testing.T) {
			db, _ := setupTestDB(t)
			hybrid := NewHybridStoreWithDB(db, 10)
			store := hybrid.(FolderContextStore)
			ctx := context.Background()
			hq := &Workspace{Name: "Fixture HQ"}
			if err := hybrid.CreateWorkspace(ctx, hq); err != nil {
				t.Fatal(err)
			}
			sess := &Session{AgentName: "Atlas", FolderID: hq.ID}
			if err := hybrid.CreateSession(ctx, sess); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AppendFolderTurn(ctx, sess.ID, hq.ID, "Atlas", "", folderEventFixture(), "Keep history", "Reply"); err != nil {
				t.Fatal(err)
			}
			// Prime the ordinary cache. Folder reads must still see canonical state.
			if _, err := hybrid.GetSession(ctx, sess.ID); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "delete_hq":
				if err := hybrid.DeleteWorkspace(ctx, hq.ID); err != nil {
					t.Fatal(err)
				}
			case "reassign":
				if _, err := db.ExecContext(ctx, `UPDATE sessions SET workspace_id=NULL WHERE id=?`, sess.ID); err != nil {
					t.Fatal(err)
				}
			case "rename":
				if _, err := hybrid.(*hybridStore).RenameSessionsByAgent(ctx, "Atlas", "Nova"); err != nil {
					t.Fatal(err)
				}
			}
			fresh, err := store.GetFolderSession(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(fresh.Messages) != 3 || fresh.Messages[0].FolderContext != nil || fresh.Messages[1].Content != "Keep history" {
				t.Fatalf("owner transition: %+v", fresh.Messages)
			}
		})
	}
}
