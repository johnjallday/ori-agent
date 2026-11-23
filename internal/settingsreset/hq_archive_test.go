package settingsreset

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

const fixtureHQID = "native-personal-hq"
const fixtureHQSlug = "another-name"

func seedArchiveHQ(t *testing.T, o *Owners) string {
	t.Helper()
	now := time.Now().UTC()
	mustPreview(t, session.NewSQLiteStore(o.Database).CreateWorkspace(t.Context(), &session.Workspace{
		ID: fixtureHQID, Name: "My HQ", FolderSlug: fixtureHQSlug, OwnerUserID: "local",
		CreatedAt: now, UpdatedAt: now, Status: session.WorkspaceStatusActive,
	}))
	mustPreview(t, workspacecontinuity.NewLocalStore(o.Database).RegisterNative(t.Context(), fixtureHQID))
	mustPreview(t, o.Workspaces.Save(&workspace.Workspace{
		ID: fixtureHQID, Name: "My HQ", FolderSlug: fixtureHQSlug, OwnerUserID: "local", Kind: "workspace",
		CreatedAt: now, UpdatedAt: now, Status: workspace.StatusActive,
	}))
	mustPreview(t, userprofile.NewSQLiteStore(o.Database).SetPersonalWorkspaceID(t.Context(), "local", fixtureHQID))
	folder := filepath.Join(o.Workspaces.BasePath(), fixtureHQSlug)
	mustPreview(t, os.WriteFile(filepath.Join(folder, "keep.txt"), []byte("old personal HQ content\n"), 0o600))
	return folder
}

func requireArchiveFS(t *testing.T, o *Owners) {
	t.Helper()
	root, err := resolvePath(o.Workspaces.BasePath())
	mustPreview(t, err)
	data, err := resolvePath(o.DataDir)
	mustPreview(t, err)
	parents, err := openArchiveParents(root, data)
	if errors.Is(err, errArchiveVolume) {
		t.Skip("archive requires same-volume local APFS, ext4, XFS or Btrfs with stable file identity")
	}
	mustPreview(t, err)
	parents.close()
}

func TestArchivePreviewBindsTheExactDesignatedFolderOnlyWhenRequested(t *testing.T) {
	f, owners, planner := previewFixture(t)
	requireArchiveFS(t, owners)
	source := seedArchiveHQ(t, owners)
	_, inspectErr := database.InspectResetHQ(t.Context(), owners.Database.DB)
	mustPreview(t, inspectErr)
	// Same display name/slug guess is not authority, even when the folder is
	// present in the root. Neither folder is modified by preview.
	mustPreview(t, f.WriteFile("projects/my-hq/old.txt", []byte("detached")))
	ctx := t.Context()
	categories := []CategoryID{CategoryAppRecords}
	off, err := planner.Create(ctx, IntentSelectedData, categories)
	mustPreview(t, err)
	unchecked, err := planner.CreateWithOptions(ctx, IntentSelectedData, categories, PreviewOptions{ArchivePersonalHQ: false})
	mustPreview(t, err)
	on, err := planner.CreateWithOptions(ctx, IntentSelectedData, categories, PreviewOptions{ArchivePersonalHQ: true})
	mustPreview(t, err)
	canonical, err := resolvePath(source)
	mustPreview(t, err)
	for _, view := range []Preview{off, unchecked, on} {
		if view.HQArchive == nil || view.HQArchive.Source != canonical || view.HQArchive.Destination != hqArchiveDestination(owners.DataDir, view.OperationID, fixtureHQSlug) ||
			view.HQArchive.Notice == "" || view.HQArchive.UnavailableReason != "" {
			t.Fatalf("bad archive review: %+v blockers %+v", view.HQArchive, view.Blockers)
		}
		if _, err := os.Lstat(view.HQArchive.Destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("preview wrote an archive: %v", err)
		}
	}
	if off.HQArchive.Requested || unchecked.HQArchive.Requested || !on.HQArchive.Requested ||
		off.ScopeDigest != unchecked.ScopeDigest || on.ScopeDigest == off.ScopeDigest {
		t.Fatal("omitted/false changed default; or the archive opt-in did not change scope")
	}
	approved, err := planner.validatedPlan(ctx, on.ID)
	mustPreview(t, err)
	if approved.Evidence.HQArchive == nil || !confirmedHQArchive(approved, on.ScopeDigest) ||
		confirmedHQArchive(approved, off.ScopeDigest) || confirmedHQArchive(approved, "") {
		t.Fatal("the archive is not bound to the exact opt-in review")
	}
	plan, err := planner.validatedPlan(ctx, off.ID)
	mustPreview(t, err)
	if plan.Preview.HQArchive != nil || plan.Evidence.HQArchive != nil {
		t.Fatal("a default preview gained durable move authority")
	}
	old := source + "-moved"
	mustPreview(t, os.Rename(source, old))
	mustPreview(t, os.CopyFS(source, os.DirFS(old)))
	if _, err := planner.Validate(ctx, on.ID); !errors.Is(err, ErrScopeChanged) {
		t.Fatalf("a replacement source satisfied the reviewed scope: %v", err)
	}
	f.AssertPreserved(t)
}

func TestArchivePreviewRefusesDetachedOrUnsafeTargets(t *testing.T) {
	ctx := t.Context()
	for _, name := range []string{"detached", "linked", "competing designation", "nested workspace", "vault root", "source link", "external folder"} {
		t.Run(name, func(t *testing.T) {
			f, o, planner := previewFixture(t)
			mustPreview(t, f.WriteFile("projects/my-hq/old.txt", []byte("detached")))
			if name != "detached" {
				requireArchiveFS(t, o)
				source := seedArchiveHQ(t, o)
				switch name {
				case "linked":
					_, err := o.Database.ExecContext(ctx, `UPDATE workspaces SET shared_data='{"folder_import":{"enabled":true}}' WHERE id=?`, fixtureHQID)
					mustPreview(t, err)
				case "competing designation":
					mustPreview(t, userprofile.NewSQLiteStore(o.Database).SetPersonalWorkspaceID(ctx, "another", fixtureHQID))
				case "nested workspace":
					mustPreview(t, os.WriteFile(filepath.Join(source, "files", "workspace.json"), []byte(`{"id":"nested"}`), 0o600))
				case "vault root":
					mustPreview(t, o.Config.SetVaultRoot(filepath.Join(source, "vault")))
				case "source link":
					other := filepath.Join(t.TempDir(), "external")
					mustPreview(t, os.Rename(source, other))
					mustPreview(t, os.Symlink(other, source))
				case "external folder":
					mustPreview(t, os.Rename(source, filepath.Join(t.TempDir(), "external")))
				}
			}
			categories := []CategoryID{CategoryAppRecords}
			off, err := planner.Create(ctx, IntentSelectedData, categories)
			mustPreview(t, err)
			on, err := planner.CreateWithOptions(ctx, IntentSelectedData, categories, PreviewOptions{ArchivePersonalHQ: true})
			mustPreview(t, err)
			if len(off.Blockers) != 0 || off.HQArchive.Source != "" || on.HQArchive.Source != "" ||
				on.HQArchive.UnavailableReason == "" || !hasBlocker(on, "hq_archive_unavailable") {
				t.Fatalf("unsafe choice altered default or offered an archive: off %+v / %+v, on %+v / %+v", off.HQArchive, off.Blockers, on.HQArchive, on.Blockers)
			}
		})
	}
}
