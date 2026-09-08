package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Characterization, not desired reset behavior: Shutdown currently leaves the
// shared SQLite handle and session cache usable. A caller cannot treat it as
// permission to unlink the database, especially in a same-process host.
func TestResetLifecycleShutdownLeavesSessionStoreWritable(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	db, err := database.Open(t.Context(), &database.Config{
		Path: filepath.Join(f.Paths().DataDir, "sessions.db"), WALMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cached := session.NewHybridStoreWithDB(db, 10)
	t.Cleanup(func() {
		if err := cached.Close(); err != nil {
			t.Error(err)
		}
	})
	srv := &Server{Storage: &StorageSystemFacade{SessionStore: cached}}
	saved, err := cached.GetSession(t.Context(), resetfixture.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Title = "Cached before shutdown"
	writer := resetfixture.NewWriter(t, func() error { return cached.FlushToStorage(context.Background()) })
	pending, err := writer.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	srv.Shutdown()
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal("characterization changed: Shutdown now closes the session DB:", err)
	}
	pending.Release()
	if err := pending.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	durable, err := session.NewSQLiteStore(db).GetSession(t.Context(), resetfixture.SessionID)
	if err != nil || durable.Title != saved.Title {
		t.Fatal("expected cached writer to remain live after Shutdown:", err)
	}
}

// Closing the actual session owner flushes cached metadata before closing the
// DB. This proves why shutdown must precede destructive apply, not follow it.
func TestResetLifecycleRefusesFiniteWorkWithoutCancellation(t *testing.T) {
	gate := &resetstate.WorkGate{}
	workCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release, err := gate.Enter()
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := newServerResetLifecycle(&Server{resetWork: gate})
	if err := lifecycle.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("active fence = %v", err)
	}
	select {
	case <-workCtx.Done():
		t.Fatalf("busy reset cancelled work: %v", workCtx.Err())
	default:
	}
	if gate.Snapshot().Fenced {
		t.Fatal("busy refusal partly fenced runtime")
	}
	release()
}

func TestResetLifecycleDrainFlushesFencedSessionBeforeClose(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	path := filepath.Join(f.Paths().DataDir, "sessions.db")
	db, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	cached := session.NewHybridStoreWithDB(db, 10)
	gate := &resetstate.WorkGate{}
	session.SetAdmissionGate(cached, gate)
	saved, err := cached.GetSession(t.Context(), resetfixture.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Title = "Final cache before fenced drain"
	srv := &Server{resetWork: gate, Storage: &StorageSystemFacade{SessionStore: cached}}
	lifecycle := newServerResetLifecycle(srv)
	if err := lifecycle.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(t.Context()); err == nil {
		t.Fatal("reset drain left shared database open")
	}
	reopened, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	durable, err := session.NewSQLiteStore(reopened).GetSession(t.Context(), resetfixture.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Title != saved.Title {
		t.Fatalf("fenced close lost final cache: title=%q", durable.Title)
	}
}

func TestResetLifecycleDrainRejectsUnreleasedLifetimeOwner(t *testing.T) {
	gate := &resetstate.WorkGate{}
	release, err := gate.EnterLifetime()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	lifecycle := newServerResetLifecycle(&Server{resetWork: gate})
	if err := lifecycle.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := lifecycle.Drain(ctx); !errors.Is(err, errResetOwnersRemain) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ambiguous owner drain = %v", err)
	}
}

func TestProductionResetLifecycleStagesThenHostRecoversSameInstallation(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("native reset admission lease unavailable")
	}
	f := resetfixture.NewSeeded(t)
	paths := f.Paths()
	t.Chdir(paths.DataDir)
	t.Setenv("ORI_DATA_DIR", paths.DataDir)
	lease, err := resetstate.Acquire(paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	gate := lease.WorkGate()
	cfg := config.NewManagerWithSecretStore(filepath.Join(paths.DataDir, "settings.json"), f.Secrets())
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(paths.DataDir, "sessions.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.NewHybridStoreWithDB(db, 10)
	session.SetAdmissionGate(sessions, gate)
	agents, err := store.NewFileStore(filepath.Join(paths.DataDir, "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	folders, err := workspace.NewFileStore(paths.Workspaces)
	if err != nil {
		t.Fatal(err)
	}
	uploads, err := sessionfiles.NewStore(filepath.Join(paths.DataDir, "session_files"))
	if err != nil {
		t.Fatal(err)
	}
	owners := func() settingsreset.Owners {
		return settingsreset.Owners{
			DataDir: paths.DataDir, Config: cfg, Agents: agents, Setup: onboarding.NewManager(filepath.Join(paths.DataDir, "app_state.json")),
			Database: db, Uploads: uploads, Workspaces: folders,
			Allowlist: workspace.NewAllowlist(filepath.Join(paths.DataDir, workspace.DefaultAllowlistFilename)),
			Vaults:    vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: paths.DataDir, ManagedVaultRoot: paths.Vaults}),
			CheckLifecycle: func(context.Context) []settingsreset.Blocker {
				if gate.Snapshot().Known {
					return nil
				}
				return []settingsreset.Blocker{{Code: "lifecycle_unavailable", Message: "fixture lifecycle unavailable"}}
			},
		}
	}
	planner := settingsreset.NewPlanner(owners)
	preview, err := planner.Create(t.Context(), settingsreset.IntentSelectedData, []settingsreset.CategoryID{settingsreset.CategoryAppRecords})
	if err != nil || len(preview.Blockers) != 0 {
		t.Fatalf("preview = %+v, %v", preview.Blockers, err)
	}
	srv := &Server{
		resetWork:          gate,
		resetLease:         lease,
		Storage:            &StorageSystemFacade{SessionStore: sessions},
		workspaceFileStore: folders,
	}
	operation, err := settingsreset.NewCoordinator(lease, planner, newServerResetLifecycle(srv)).Stage(t.Context(), settingsreset.ExecuteRequest{
		PreviewID: preview.ID, RequestID: "production-lifecycle-fixture", Confirmation: "RESET",
	})
	if err != nil || operation.State != settingsreset.StateAwaitingRestart {
		t.Fatalf("stage = %+v, %v", operation, err)
	}
	if err := db.PingContext(t.Context()); err == nil {
		t.Fatal("staged production lifecycle left the selected database owner open")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := settingsreset.BeforeStores(t.Context(), paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil || recovered.WorkGate().Snapshot().Fenced {
		t.Fatal("pre-store recovery did not authorize normal runtime construction")
	}
	if err := recovered.Close(); !errors.Is(err, resetstate.ErrPinned) {
		t.Fatalf("verified host did not retain process ownership: %v", err)
	}
	reopened, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(paths.DataDir, "sessions.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	report, err := database.InspectReset(t.Context(), reopened.DB)
	if err != nil {
		t.Fatal(err)
	}
	for name, count := range report.Counts {
		if count == nil || *count != 0 {
			t.Fatalf("database domain %s not reset: %v", name, count)
		}
	}
	f.AssertPreserved(t)
}

func TestResetLifecycleSessionCloseFlushesBeforeSamePathReopen(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	path := filepath.Join(f.Paths().DataDir, "sessions.db")
	db, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	cached := session.NewHybridStoreWithDB(db, 10)
	closed := false
	t.Cleanup(func() {
		if !closed {
			if err := cached.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	saved, err := cached.GetSession(t.Context(), resetfixture.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Title = "Final cached title"
	closeErr := cached.Close()
	closed = true
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := db.PingContext(t.Context()); err == nil {
		t.Fatal("Close left the database usable")
	}
	reopened, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	durable, err := session.NewSQLiteStore(reopened).GetSession(t.Context(), resetfixture.SessionID)
	if err != nil || durable.Title != saved.Title {
		t.Fatal("final flush was not visible in the same installation:", err)
	}
}
