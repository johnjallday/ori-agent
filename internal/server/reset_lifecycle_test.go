package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
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
