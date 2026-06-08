package server

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
)

// TestIsGroupWorkspace verifies the routing branch used by serveWorkspaceDetail:
// group IDs are detected as groups, while concrete workspaces, missing IDs, and
// an unavailable store are not (so they keep the standard workspace-detail path).
func TestIsGroupWorkspace(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := session.NewHybridStoreWithDB(db, 50)
	now := time.Now()

	createWorkspace(t, ctx, store, &session.Workspace{
		ID: "grp", Name: "Clients", Kind: session.WorkspaceKindGroup,
		CreatedAt: now, UpdatedAt: now,
	})
	createWorkspace(t, ctx, store, &session.Workspace{
		ID: "ws", Name: "Project", Kind: session.WorkspaceKindWorkspace,
		CreatedAt: now, UpdatedAt: now,
	})

	s := &Server{Storage: &StorageSystemFacade{SessionStore: store}}

	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"group is detected", "grp", true},
		{"concrete workspace is not a group", "ws", false},
		{"missing id is not a group", "does-not-exist", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.isGroupWorkspace(tc.id); got != tc.want {
				t.Errorf("isGroupWorkspace(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}

	// An unavailable store must never panic and must report false.
	empty := &Server{Storage: &StorageSystemFacade{}}
	if empty.isGroupWorkspace("grp") {
		t.Errorf("isGroupWorkspace with nil SessionStore = true, want false")
	}
}

func createWorkspace(t *testing.T, ctx context.Context, store session.HybridStore, ws *session.Workspace) {
	t.Helper()
	if err := store.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("create workspace %q: %v", ws.ID, err)
	}
}
