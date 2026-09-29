package server

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// Background routines that list workspaces themselves (File Janitor catch-up,
// blueprint reintake) must skip an imported workspace until the user enables
// its routines on this installation.
func TestAutomaticWorkspacesSkipsInactiveImports(t *testing.T) {
	ctx := t.Context()
	db, err := database.Open(ctx, &database.Config{Path: filepath.Join(t.TempDir(), "sessions.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,created_at,updated_at) VALUES ('local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"native", "imported"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES (?, 'Synthetic',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id); err != nil {
			t.Fatal(err)
		}
	}
	local := workspacecontinuity.NewLocalStore(db)
	for _, id := range []string{"native", "imported"} {
		if err := local.RegisterNative(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE continuity_attachments SET state='imported_inactive' WHERE workspace_id='imported'`); err != nil {
		t.Fatal(err)
	}

	ids := []string{"native", "imported", "untracked"}
	b := &ServerBuilder{}
	if got := b.automaticWorkspaces(ids); !slices.Equal(got, ids) {
		t.Fatalf("without continuity every workspace keeps running, got %v", got)
	}

	b.continuityLocal = local
	if got := b.automaticWorkspaces(ids); !slices.Equal(got, []string{"native", "untracked"}) {
		t.Fatalf("inactive import was admitted to background routines: %v", got)
	}

	attachment, err := local.Attachment(ctx, "imported")
	if err != nil {
		t.Fatal(err)
	}
	if err := local.SetImportedActive(ctx, "imported", attachment.Version, true); err != nil {
		t.Fatal(err)
	}
	if got := b.automaticWorkspaces(ids); !slices.Equal(got, ids) {
		t.Fatalf("enabled import stayed out of background routines: %v", got)
	}
}
