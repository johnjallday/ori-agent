package settingsreset

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/vault"
)

type archiveFixture struct {
	*resetfixture.Fixture
	options RecoveryOptions
	preview Preview
	source  string
	other   string
}

func stageArchiveFixture(t *testing.T, intent Intent, archive bool, alsoSettings bool) archiveFixture {
	t.Helper()
	f, owners, planner := previewFixture(t)
	requireArchiveFS(t, owners)
	source := seedArchiveHQ(t, owners)
	other := filepath.Join(f.Paths().Workspaces, "my-hq")
	mustPreview(t, f.WriteFile("projects/my-hq/keep.txt", []byte("different folder stays\n")))
	mustPreview(t, os.Symlink(filepath.Join(other, "keep.txt"), filepath.Join(source, "linked-elsewhere")))
	var categories []CategoryID
	switch intent {
	case IntentStartFresh:
		root := f.Paths().DataDir
		mustPreview(t, owners.Config.SetTemplatesRoot(filepath.Join(root, "templates")))
		mustPreview(t, owners.Config.Save())
		t.Setenv("ORI_TEMPLATES_DIR", filepath.Join(root, "templates"))
		t.Setenv("WORKFLOW_TEMPLATES_DIR", filepath.Join(root, "workflow_templates"))
		owners.FreshTargets = fixtureFreshTargets(root)
	default:
		categories = []CategoryID{CategoryAppRecords}
		if alsoSettings {
			categories = append(categories, CategorySettings)
		}
	}
	ctx := t.Context()
	preview, err := planner.CreateWithOptions(ctx, intent, categories, PreviewOptions{ArchivePersonalHQ: archive})
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("preview blocked: %+v", preview.Blockers)
	}
	lease, err := resetstate.Acquire(f.Paths().DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { _ = lease.Close() })
	life := &fixtureLifecycle{drain: func(_ context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}}
	coordinator := NewCoordinator(lease, planner, life)
	req := ExecuteRequest{PreviewID: preview.ID, RequestID: "archive-reset-review", Confirmation: "RESET", ConfirmAgentsFolder: confirmedAgentsFolder(preview)}
	if archive {
		if _, err := coordinator.Stage(ctx, req); !errors.Is(err, ErrHQArchiveUnconfirmed) {
			t.Fatalf("no explicit archive confirmation = %v", err)
		}
		req.ConfirmHQArchive = strings.Repeat("0", 64)
		if _, err := coordinator.Stage(ctx, req); !errors.Is(err, ErrHQArchiveUnconfirmed) {
			t.Fatalf("wrong scope allowed archive: %v", err)
		}
		req.ConfirmHQArchive = preview.ScopeDigest
	}
	op, err := coordinator.Stage(ctx, req)
	if err != nil || op.State != StateAwaitingRestart {
		t.Fatalf("Stage = %+v, %v", op, err)
	}
	if _, err := os.Lstat(source); err != nil {
		t.Fatalf("admission already moved the folder: %v", err)
	}
	if archive {
		if err := RecoverBeforeStores(ctx, lease, RecoveryOptions{DataDir: lease.Path(), SecretStore: f.Secrets()}); !errors.Is(err, ErrRecoveryIncomplete) {
			t.Fatalf("archive could run on the accepting lease: %v", err)
		}
	}
	mustPreview(t, lease.Close())
	return archiveFixture{Fixture: f, options: RecoveryOptions{DataDir: lease.Path(), SecretStore: f.Secrets()}, preview: preview, source: source, other: other}
}

func (f archiveFixture) recover(t *testing.T, hooks archiveMoveHooks) (Operation, error) {
	t.Helper()
	lease, err := resetstate.Acquire(f.Paths().DataDir)
	mustPreview(t, err)
	defer func() { _ = lease.Close() }()
	options := f.options
	options.archiveHooks = hooks
	pending := RecoverBeforeStores(t.Context(), lease, options)
	op, err := NewCoordinator(lease, nil, nil).Status(t.Context(), f.preview.OperationID)
	mustPreview(t, err)
	return op, pending
}

func (f archiveFixture) assertBytes(t *testing.T, archived bool) {
	t.Helper()
	source, destination := f.source, f.preview.HQArchive.Destination
	where, absent := source, destination
	if archived {
		where, absent = destination, source
	}
	for path, want := range map[string]string{
		filepath.Join(where, "keep.txt"):   "old personal HQ content\n",
		filepath.Join(f.other, "keep.txt"): "different folder stays\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", path, got, err)
		}
	}
	if link, err := os.Readlink(filepath.Join(where, "linked-elsewhere")); err != nil || link != filepath.Join(f.other, "keep.txt") {
		t.Fatalf("retained link = %q, %v", link, err)
	}
	if _, err := os.Lstat(absent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected other path: %v", err)
	}
	f.AssertPreserved(t)
}

func TestArchiveRecoveryBothResetModesPreserveByDefaultAndMoveOnOptIn(t *testing.T) {
	for _, intent := range []Intent{IntentSelectedData, IntentStartFresh} {
		t.Run(string(intent), func(t *testing.T) {
			for _, archive := range []bool{false, true} {
				mode := "preserve"
				if archive {
					mode = "archive"
				}
				t.Run(mode, func(t *testing.T) {
					f := stageArchiveFixture(t, intent, archive, false)
					result, err := f.recover(t, archiveMoveHooks{})
					if err != nil || !result.VerifiedComplete() || (result.HQArchive != nil) != archive {
						t.Fatalf("result = %+v, %v", result, err)
					}
					if archive && (result.HQArchive.Destination != f.preview.HQArchive.Destination || result.HQArchive.Outcome != OutcomeCompleted) {
						t.Fatalf("archive = %+v", result.HQArchive)
					}
					f.assertBytes(t, archive)
					if _, err := database.InspectResetHQFile(t.Context(), filepath.Join(f.Paths().DataDir, "sessions.db")); !errors.Is(err, database.ErrResetHQIneligible) {
						t.Fatalf("reset left a designation: %v", err)
					}
					if again, err := f.recover(t, archiveMoveHooks{}); err != nil || again.Revision != result.Revision {
						t.Fatalf("relaunch replayed the archive or reset: %+v, %v", again, err)
					}
				})
			}
		})
	}
}

func TestArchiveRecoveryRestartsFromIntentBeforeOrAfterRename(t *testing.T) {
	for _, occurrence := range []string{"before move", "after move"} {
		t.Run(occurrence, func(t *testing.T) {
			f := stageArchiveFixture(t, IntentSelectedData, true, false)
			lost := func() error { return errors.New("test process stopped") }
			hooks := archiveMoveHooks{beforeMove: lost}
			if occurrence == "after move" {
				hooks = archiveMoveHooks{afterMove: lost}
			}
			if op, err := f.recover(t, hooks); !errors.Is(err, ErrAdmissionUncertain) || op.State != StateApplying {
				t.Fatalf("did not interrupt with durable intent: %+v, %v", op, err)
			}
			f.assertBytes(t, occurrence == "after move")
			if op, err := f.recover(t, archiveMoveHooks{}); err != nil || !op.VerifiedComplete() {
				t.Fatalf("did not settle exactly one move: %+v, %v", op, err)
			}
			f.assertBytes(t, true)
		})
	}
}

func TestArchiveRenamePrimitiveRefusesEvenAnEmptyDestination(t *testing.T) {
	f, owners, _ := previewFixture(t)
	requireArchiveFS(t, owners)
	source := seedArchiveHQ(t, owners)
	parents, err := openArchiveParents(filepath.Dir(source), f.Paths().DataDir)
	mustPreview(t, err)
	defer parents.close()
	destination := filepath.Join(f.Paths().DataDir, "occupied")
	mustPreview(t, os.Mkdir(destination, 0o750))
	if err := renameArchiveNoReplace(parents.source, fixtureHQSlug, parents.destination, "occupied"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("rename with an occupied empty directory = %v", err)
	}
	if files, err := os.ReadDir(destination); err != nil || len(files) != 0 {
		t.Fatalf("destination changed: %v, %v", files, err)
	}
	if _, err := os.Stat(filepath.Join(source, "keep.txt")); err != nil {
		t.Fatalf("source was moved: %v", err)
	}
}

func TestArchiveRecoveryNeverOverwritesAndCanRetryAfterMovingAnOccupantAside(t *testing.T) {
	f := stageArchiveFixture(t, IntentSelectedData, true, false)
	destination := f.preview.HQArchive.Destination
	mustPreview(t, os.Mkdir(destination, 0o750))
	mustPreview(t, os.WriteFile(filepath.Join(destination, "not-hq"), []byte("unrelated"), 0o600))
	for range 2 {
		op, err := f.recover(t, archiveMoveHooks{})
		if !errors.Is(err, ErrRecoveryIncomplete) || op.State != StateBlocked || op.HQArchive.Outcome != OutcomeFailed || !allResultsPending(op.Results) {
			t.Fatalf("occupied destination = %+v, %v", op, err)
		}
		got, err := os.ReadFile(filepath.Join(destination, "not-hq"))
		if err != nil || string(got) != "unrelated" {
			t.Fatalf("destination overwritten: %s, %v", got, err)
		}
	}
	mustPreview(t, os.Rename(destination, filepath.Join(f.Paths().DataDir, "kept-occupant")))
	if op, err := f.recover(t, archiveMoveHooks{}); err != nil || !op.VerifiedComplete() {
		t.Fatalf("retry after safe resolution = %+v, %v", op, err)
	}
	f.assertBytes(t, true)
}

func TestArchiveRecoveryRefusesAReplacedSourceWithoutResetting(t *testing.T) {
	f := stageArchiveFixture(t, IntentSelectedData, true, false)
	original := f.source + "-old"
	mustPreview(t, os.Rename(f.source, original))
	mustPreview(t, os.CopyFS(f.source, os.DirFS(original)))
	op, err := f.recover(t, archiveMoveHooks{})
	if !errors.Is(err, ErrRecoveryIncomplete) || op.State != StateBlocked || op.HQArchive.Outcome != OutcomeFailed || !allResultsPending(op.Results) {
		t.Fatalf("replacement source = %+v, %v", op, err)
	}
	f.assertBytes(t, false)
	if _, err := database.InspectResetHQFile(t.Context(), filepath.Join(f.Paths().DataDir, "sessions.db")); err != nil {
		t.Fatalf("preflight failure still cleared the designated HQ: %v", err)
	}
}

func TestArchiveRecoveryKeepsVerifiedArchiveAfterLaterCategoryFailure(t *testing.T) {
	f := stageArchiveFixture(t, IntentSelectedData, true, true)
	failing := &flakyResetSecrets{SecretStore: f.Secrets()}
	f.options.SecretStore = failing
	op, err := f.recover(t, archiveMoveHooks{})
	if !errors.Is(err, ErrRecoveryIncomplete) || op.State != StatePartialFailure || op.HQArchive.Outcome != OutcomeCompleted {
		t.Fatalf("category failed without preserving archive outcome: %+v, %v", op, err)
	}
	f.assertBytes(t, true)
	f.options.SecretStore = f.Secrets()
	if op, err := f.recover(t, archiveMoveHooks{}); err != nil || !op.VerifiedComplete() {
		t.Fatalf("after DB cleared, retry must not need the old designation: %+v, %v", op, err)
	}
	f.assertBytes(t, true)
}

type flakyResetSecrets struct{ vault.SecretStore }

func (s *flakyResetSecrets) Delete(vault.SecretKey) error {
	return errors.New("synthetic category deletion failure")
}

func TestArchiveJournalRejectsChangedPathsAndMissingConsent(t *testing.T) {
	f := stageArchiveFixture(t, IntentSelectedData, true, false)
	lease, err := resetstate.Acquire(f.Paths().DataDir)
	mustPreview(t, err)
	defer func() { _ = lease.Close() }()
	original, err := lease.Read(resetstate.OperationRecord)
	mustPreview(t, err)
	for _, tc := range []struct {
		name   string
		mutate func(*journal)
	}{
		{"source", func(j *journal) { j.Plan.Evidence.HQArchive.Source += "-other" }},
		{"destination", func(j *journal) { j.Plan.Evidence.HQArchive.Destination += "-other" }},
		{"contents", func(j *journal) { j.Plan.Evidence.HQArchive.Contents = strings.Repeat("a", 64) }},
		{"consent", func(j *journal) { j.Archive.Consent = "" }},
		{"baseline", func(j *journal) { j.Archive.Remainder = strings.Repeat("b", 64) }},
		{"opt-in", func(j *journal) { j.Plan.Preview.HQArchive.Requested = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j, err := decodeJournal(original)
			mustPreview(t, err)
			tc.mutate(j)
			changed, err := json.Marshal(j)
			mustPreview(t, err)
			if _, err := decodeJournal(changed); !errors.Is(err, ErrJournalInvalid) {
				t.Fatalf("altered journal allowed: %v", err)
			}
		})
	}
	f.assertBytes(t, false)
}
