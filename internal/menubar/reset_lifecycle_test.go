package menubar

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/server"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

// Exercise the real StartServer/runServer/StopServer paths twice in the same
// process and directory. Only runtime construction is replaced: real SQLite,
// session cache, Server.Start/Shutdown and HTTP lifecycle are used; the broad
// builder/native discovery and native menubar UI are deliberately not invoked.
func TestResetLifecycleMenubarStopStartLeavesPreviousSQLiteOpen(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	// Match the shell's outer lifetime, but release in fixture cleanup only
	// AFTER explicitly closing all test stores. Production pins until exit.
	if resetstate.Supported() {
		lease, err := resetstate.Acquire(f.Paths().DataDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := lease.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	path := filepath.Join(f.Paths().DataDir, "sessions.db")
	controller := NewController(0)
	type running struct {
		db  *database.DB
		url string
	}
	ready := make(chan running, 1)
	var mu sync.Mutex
	var stores []session.HybridStore
	controller.runtimeFactory = func(string) (*server.Server, *http.Server, error) {
		db, err := database.Open(context.Background(), &database.Config{Path: path, WALMode: true})
		if err != nil {
			return nil, nil, err
		}
		cached := session.NewHybridStoreWithDB(db, 10)
		mu.Lock()
		stores = append(stores, cached)
		mu.Unlock()
		srv := &server.Server{Storage: &server.StorageSystemFacade{SessionStore: cached}}
		return srv, &http.Server{
			Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}),
			BaseContext: func(listener net.Listener) context.Context {
				srv.Start()
				ready <- running{db: db, url: "http://" + listener.Addr().String()}
				return context.Background()
			},
		}, nil
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if controller.GetStatus() != StatusStopped {
			if err := controller.StopServer(ctx); err != nil {
				t.Error(err)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		for _, cached := range stores {
			if err := cached.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var previous *database.DB
	for range 2 {
		if err := controller.StartServer(ctx); err != nil {
			t.Fatal(err)
		}
		var instance running
		select {
		case instance = <-ready:
		case <-ctx.Done():
			t.Fatal("test-owned listener never started")
		}
		response, err := client.Get(instance.url)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNoContent {
			t.Fatal("test-owned handler did not respond")
		}
		if err := controller.StopServer(ctx); err != nil {
			t.Fatal(err)
		}
		if resetstate.Supported() {
			other, err := resetstate.Acquire(f.Paths().DataDir)
			if other != nil || !errors.Is(err, resetstate.ErrInUse) {
				t.Fatal("menubar stop released installation ownership:", err)
			}
		}
		if controller.GetStatus() != StatusStopped || controller.server != nil || controller.httpServer != nil {
			t.Fatal("StopServer did not clear host state")
		}
		if err := instance.db.PingContext(ctx); err != nil {
			t.Fatal("characterization changed: StopServer now closes SQLite:", err)
		}
		if previous != nil {
			if err := previous.PingContext(ctx); err != nil {
				t.Fatal("expected previous same-process instance to retain an open handle:", err)
			}
		}
		previous = instance.db
	}
}
