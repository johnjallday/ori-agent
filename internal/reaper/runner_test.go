package reaper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

type runnerRootStub struct {
	root string
	err  error
}

func (s runnerRootStub) Resolve() (string, error) { return s.root, s.err }

type runnerProbeStub struct{ observation reapersetup.RunnerObservation }

func (s runnerProbeStub) DetectRunner(context.Context) reapersetup.RunnerObservation {
	return s.observation
}

func TestRunnerWritesInboxTriggersRegisteredRunnerAndReadsStatus(t *testing.T) {
	root := t.TempDir()
	commandID := "_RSdeadBEEF"
	actionCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/_/"+commandID {
			http.NotFound(w, req)
			return
		}
		actionCalls++
		if err := os.WriteFile(filepath.Join(root, "last_status.txt"), []byte("ok\n"), 0o600); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	probe := clientProbe{
		web:       reapersetup.WebRemoteObservation{State: reapersetup.ProbeReady, Port: port},
		transport: reapersetup.LiveTransportObservation{State: reapersetup.TransportAvailable, Port: port},
	}
	client := NewClient(reapersetup.ProbeSet{WebRemote: probe, Transport: probe})
	client.http = server.Client()
	runner := NewRunner(runnerRootStub{root: root}, reapersetup.ProbeSet{
		Runner: runnerProbeStub{observation: reapersetup.RunnerObservation{State: reapersetup.ProbeReady, Root: root, CommandID: commandID}},
	}, client)

	lua := "reaper.ShowConsoleMsg('hello')\n"
	result, err := runner.RunScript(context.Background(), lua)
	if err != nil || result.Outcome != "ok" || actionCalls != 1 {
		t.Fatalf("run = %+v, %v, calls=%d", result, err, actionCalls)
	}
	inbox, err := os.ReadFile(filepath.Join(root, "inbox.lua"))
	if err != nil || string(inbox) != lua {
		t.Fatalf("inbox = %q, %v", inbox, err)
	}
	if info, err := os.Stat(filepath.Join(root, "inbox.lua")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("inbox permissions = %+v, %v", info, err)
	}
}

func TestRunnerSurfacesREAPERErrorText(t *testing.T) {
	root := t.TempDir()
	commandID := "_RSdeadBEEF"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = os.WriteFile(filepath.Join(root, "last_status.txt"), []byte("error: attempt to call nil value\n"), 0o600)
	}))
	defer server.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	probe := clientProbe{
		web:       reapersetup.WebRemoteObservation{State: reapersetup.ProbeReady, Port: port},
		transport: reapersetup.LiveTransportObservation{State: reapersetup.TransportAvailable, Port: port},
	}
	client := NewClient(reapersetup.ProbeSet{WebRemote: probe, Transport: probe})
	client.http = server.Client()
	runner := NewRunner(runnerRootStub{root: root}, reapersetup.ProbeSet{
		Runner: runnerProbeStub{observation: reapersetup.RunnerObservation{State: reapersetup.ProbeReady, Root: root, CommandID: commandID}},
	}, client)
	result, err := runner.RunScript(context.Background(), "error('test')")
	if !errors.Is(err, ErrRunnerFailed) || result.Outcome != "error" || result.ErrorText != "attempt to call nil value" {
		t.Fatalf("runner error = %+v, %v", result, err)
	}
}

func TestRunnerDoesNotTouchInboxWhileDisconnected(t *testing.T) {
	root := t.TempDir()
	probe := clientProbe{
		web:       reapersetup.WebRemoteObservation{State: reapersetup.ProbeReady, Port: 2307},
		transport: reapersetup.LiveTransportObservation{State: reapersetup.TransportOffline},
	}
	client := NewClient(reapersetup.ProbeSet{WebRemote: probe, Transport: probe})
	runner := NewRunner(runnerRootStub{root: root}, reapersetup.ProbeSet{
		Runner: runnerProbeStub{observation: reapersetup.RunnerObservation{State: reapersetup.ProbeReady, Root: root, CommandID: "_RSdeadBEEF"}},
	}, client)
	runner.timeout = 50 * time.Millisecond
	result, err := runner.RunScript(context.Background(), "return 1")
	if !errors.Is(err, ErrActionDisconnected) || result.Outcome != "error" {
		t.Fatalf("disconnected run = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "inbox.lua")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disconnected run touched inbox: %v", err)
	}
}
