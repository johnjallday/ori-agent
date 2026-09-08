package menubar

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/server"
)

func waitResetHost(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !ready() {
		t.Fatal("owned host did not reach its checkpoint")
	}
}

func TestResetHostMenubarProcessHelper(t *testing.T) {
	mode := os.Getenv("ORI_RESET_MENUBAR_HOST_CHILD")
	if mode == "" {
		return
	}
	root := os.Getenv("ORI_DATA_DIR")
	lease, err := resetstate.BeforeStores(root)
	if err != nil {
		t.Fatal(err)
	}
	g := lease.WorkGate()
	shell := NewSettingsManagerWithAdmission(onboarding.NewManager(filepath.Join(root, "app_state.json")), g)
	c := NewControllerWithResetLease(0, lease)
	if mode == "start_timeout" {
		started, unblock := make(chan struct{}), make(chan struct{})
		release := sync.OnceFunc(func() { close(unblock) })
		defer release()
		c.runtimeFactory = func(string) (*server.Server, *http.Server, error) {
			close(started)
			<-unblock
			return nil, nil, errors.New("owned construction stopped before stores")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		if err := c.StartServer(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("start wait:", err)
		}
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("constructor not dispatched")
		}
		if c.CanStart() || c.CanStop() {
			t.Fatal("timeout falsely retired in-flight construction")
		}
		if err := c.StartServer(t.Context()); err == nil {
			t.Fatal("overlapping construction admitted")
		}
		if err := c.StopServer(t.Context()); err == nil {
			t.Fatal("Stop pretended to join construction")
		}
		if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
			t.Fatal("constructor lost its permit:", err)
		}
		release()
		waitResetHost(t, func() bool { return g.Snapshot().Active == 0 })
		if !g.Snapshot().Fenced || !lease.Uncertain() || c.CanStart() {
			t.Fatal("failed construction became a clean retry")
		}
		return
	}

	var builds atomic.Int32
	var dbs []*database.DB
	ready := make(chan string, 1)
	requestStarted := make(chan struct{}, 1)
	requestRelease := make(chan struct{})
	finishRequest := sync.OnceFunc(func() { close(requestRelease) })
	c.runtimeFactory = func(string) (*server.Server, *http.Server, error) {
		builds.Add(1)
		if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
			t.Error("factory not tracked:", err)
		}
		db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, "sessions.db"), WALMode: true})
		if err != nil {
			return nil, nil, err
		}
		dbs = append(dbs, db) // Read only after each completed StartServer call.
		if _, err := db.ExecContext(t.Context(), "CREATE TABLE IF NOT EXISTS fixture_host_events (value TEXT)"); err != nil {
			return nil, nil, err
		}
		srv := &server.Server{}
		return srv, &http.Server{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				release, err := g.Enter()
				if err != nil {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				defer release()
				requestStarted <- struct{}{}
				<-requestRelease
				w.WriteHeader(http.StatusNoContent)
			}),
			BaseContext: func(l net.Listener) context.Context {
				ready <- "http://" + l.Addr().String()
				return context.Background()
			},
		}, nil
	}
	t.Cleanup(func() {
		finishRequest()
		if c.CanStop() {
			if err := c.StopServer(context.Background()); err != nil {
				t.Error(err)
			}
		}
		for _, db := range dbs {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	if err := c.StartServer(t.Context()); err != nil {
		t.Fatal(err)
	}
	var url string
	select {
	case url = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("owned listener did not start")
	}
	if mode == "stop_timeout" {
		transport := &http.Transport{Proxy: nil}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		done := make(chan error, 1)
		go func() {
			response, err := client.Get(url)
			if err == nil {
				err = response.Body.Close()
			}
			done <- err
		}()
		select {
		case <-requestStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("owned request did not start")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		if err := c.StopServer(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("shutdown wait:", err)
		}
		if c.GetStatus() != StatusError || !c.CanStop() || c.CanStart() {
			t.Fatal("incomplete shutdown discarded its runtime")
		}
		if err := c.StartServer(t.Context()); err == nil || builds.Load() != 1 {
			t.Fatal("restarted over active HTTP work")
		}
		if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
			t.Fatal("active request disappeared:", err)
		}
		finishRequest()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := c.StopServer(t.Context()); err != nil {
			t.Fatal("shutdown retry:", err)
		}
	} else {
		// A writer from the old runtime retains admission after Stop and after
		// a second runtime starts. Its final write uses the old real SQLite handle.
		release, err := g.Enter()
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if err := c.StopServer(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
			t.Fatal("Stop lost old writer:", err)
		}
		if err := c.StartServer(t.Context()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("second owned listener did not start")
		}
		if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
			t.Fatal("Start replaced old writer's gate:", err)
		}
		if _, err := dbs[0].ExecContext(t.Context(), "INSERT INTO fixture_host_events VALUES ('old writer finished')"); err != nil {
			t.Fatal(err)
		}
		release()
		if err := c.StopServer(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	oldBuilds := builds.Load()
	if err := c.StartServer(t.Context()); !errors.Is(err, resetstate.ErrWorkFenced) || builds.Load() != oldBuilds {
		t.Fatal("Stop/Start bypassed reset fence:", err)
	}
	other := NewControllerWithResetLease(0, lease)
	other.runtimeFactory = func(string) (*server.Server, *http.Server, error) {
		t.Error("second controller bypassed fence")
		return nil, nil, errors.New("fixture forbids production construction")
	}
	if err := other.StartServer(t.Context()); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal("new controller bypassed host fence:", err)
	}
	// A separate shell manager shares the very same fence after runtime stop.
	if err := shell.SetPort(9000); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal("shell bypassed reset:", err)
	}
	if err := lease.Close(); !errors.Is(err, resetstate.ErrPinned) {
		t.Fatal("Stop released process ownership:", err)
	}
}

func TestResetHostMenubarSharesAdmissionThroughOwnedLifecycle(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("native lease unsupported")
	}
	for _, mode := range []string{"stop_start", "start_timeout", "stop_timeout"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			// #nosec G204 -- only this test executable, owned installation and private safe factory.
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestResetHostMenubarProcessHelper$")
			cmd.Dir = root
			cmd.Env = []string{"HOME=" + root, "ORI_DATA_DIR=" + root, "ORI_RESET_MENUBAR_HOST_CHILD=" + mode}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("owned menubar %s: %v\n%s", mode, err, output)
			}
			lease, err := resetstate.Acquire(root)
			if err != nil {
				t.Fatal("process exit did not release lease:", err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
