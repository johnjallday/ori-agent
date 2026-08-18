//go:build darwin

package reapersetup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var verificationNoncePattern = regexp.MustCompile(`verify-([a-f0-9]{32})\.txt`)

func TestPlatformLiveProbeHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	probe := &platformProbe{client: server.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	got := probe.CheckTransport(ctx, WebRemoteObservation{State: ProbeReady, Port: port})
	if got.State != TransportOffline {
		t.Fatalf("cancelled live probe = %+v", got)
	}
}

func TestDetectWebRemoteConfigsCombinesStandardAndPortableCandidates(t *testing.T) {
	standard := filepath.Join(t.TempDir(), "standard.ini")
	portable := filepath.Join(t.TempDir(), "portable.ini")
	if err := os.WriteFile(standard, []byte("csurf_0=HTTP 0 2307 '' 'index.html' 0 ''\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(portable, []byte("csurf_0=HTTP 0 2308 '' 'index.html' 0 ''\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := detectWebRemoteConfigs([]string{standard, portable})
	if got.State != ProbeReady || got.Port != 2307 || len(got.Ports) != 2 || got.Ports[0] != 2307 || got.Ports[1] != 2308 {
		t.Fatalf("combined configs = %+v", got)
	}
}

func TestPlatformLiveProbeUsesResponsiveConfiguredInterface(t *testing.T) {
	stale := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer stale.Close()
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("TRANSPORT\t0\t0.0\n"))
	}))
	defer live.Close()
	stalePort, _ := strconv.Atoi(strings.TrimPrefix(stale.URL, "http://127.0.0.1:"))
	livePort, _ := strconv.Atoi(strings.TrimPrefix(live.URL, "http://127.0.0.1:"))

	probe := &platformProbe{client: http.DefaultClient}
	got := probe.CheckTransport(context.Background(), WebRemoteObservation{
		State: ProbeReady,
		Port:  stalePort,
		Ports: []int{stalePort, livePort},
	})
	if got.State != TransportAvailable || got.Port != livePort {
		t.Fatalf("multi-interface live probe = %+v, want available port %d", got, livePort)
	}
}

func TestPlatformRunnerProbeDistinguishesMissingInvalidAndReady(t *testing.T) {
	root := t.TempDir()
	probe := &platformProbe{roots: runtimeTestRoot(root), homeDir: os.UserHomeDir, client: http.DefaultClient}
	if got := probe.DetectRunner(context.Background()); got.State != ProbeMissing {
		t.Fatalf("missing runner = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "runner.id"), []byte("../unsafe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := probe.DetectRunner(context.Background()); got.State != ProbeInvalid {
		t.Fatalf("invalid runner = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "runner.id"), []byte("_RS123"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := probe.DetectRunner(context.Background())
	if got.State != ProbeReady || got.CommandID != "_RS123" {
		t.Fatalf("ready runner = %+v", got)
	}
}

func TestPlatformVerifierIsHarmlessRepeatableAndRestoresExchange(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(t.TempDir(), "song.rpp")
	if err := os.WriteFile(project, []byte("ORIGINAL PROJECT"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalInbox := []byte("-- user's pending inbox\n")
	originalStatus := []byte("previous-status\n")
	if err := os.WriteFile(filepath.Join(root, "inbox.lua"), originalInbox, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "last_status.txt"), originalStatus, 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		inbox, err := os.ReadFile(filepath.Join(root, "inbox.lua"))
		if err != nil {
			t.Errorf("read inbox: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		match := verificationNoncePattern.FindStringSubmatch(string(inbox))
		if len(match) != 2 {
			t.Errorf("verification nonce missing from trusted script")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		nonce := match[1]
		payload := verificationProtocolVersion + "\n" + nonce + "\n" + project
		if err := os.WriteFile(filepath.Join(root, "verify-"+nonce+".txt"), []byte(payload), 0o600); err != nil {
			t.Errorf("write response: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "last_status.txt"), []byte("ok"), 0o600); err != nil {
			t.Errorf("write status: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}

	probe := &platformProbe{roots: runtimeTestRoot(root), homeDir: os.UserHomeDir, client: server.Client()}
	target := VerificationTarget{
		ExpectedProject: project,
		WebRemote:       WebRemoteObservation{State: ProbeReady, Port: port},
		Runner:          RunnerObservation{State: ProbeReady, Root: root, CommandID: "_RS123"},
		Timeout:         2 * time.Second,
	}
	for attempt := 0; attempt < 2; attempt++ {
		got := probe.VerifyProject(context.Background(), target)
		if got.State != VerificationSucceeded {
			t.Fatalf("attempt %d = %+v", attempt+1, got)
		}
	}

	projectData, _ := os.ReadFile(project)
	inboxData, _ := os.ReadFile(filepath.Join(root, "inbox.lua"))
	statusData, _ := os.ReadFile(filepath.Join(root, "last_status.txt"))
	if string(projectData) != "ORIGINAL PROJECT" {
		t.Fatal("verification changed project content")
	}
	if string(inboxData) != string(originalInbox) || string(statusData) != string(originalStatus) {
		t.Fatalf("exchange was not restored: inbox=%q status=%q", inboxData, statusData)
	}
	matches, err := filepath.Glob(filepath.Join(root, "verify-*.txt"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("verification output was not cleaned: %v, %v", matches, err)
	}
}

func TestPlatformVerifierRejectsWrongProjectWithoutFilenameOnlyMatch(t *testing.T) {
	root := t.TempDir()
	expected := filepath.Join(t.TempDir(), "song.rpp")
	current := filepath.Join(t.TempDir(), "song.rpp")
	for _, path := range []string{expected, current} {
		if err := os.WriteFile(path, []byte("project"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		inbox, _ := os.ReadFile(filepath.Join(root, "inbox.lua"))
		match := verificationNoncePattern.FindStringSubmatch(string(inbox))
		if len(match) == 2 {
			nonce := match[1]
			_ = os.WriteFile(filepath.Join(root, "verify-"+nonce+".txt"), []byte(verificationProtocolVersion+"\n"+nonce+"\n"+current), 0o600)
			_ = os.WriteFile(filepath.Join(root, "last_status.txt"), []byte("ok"), 0o600)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	probe := &platformProbe{roots: runtimeTestRoot(root), client: server.Client(), homeDir: os.UserHomeDir}
	got := probe.VerifyProject(context.Background(), VerificationTarget{
		ExpectedProject: expected,
		WebRemote:       WebRemoteObservation{State: ProbeReady, Port: port},
		Runner:          RunnerObservation{State: ProbeReady, Root: root, CommandID: "_RS123"},
		Timeout:         time.Second,
	})
	if got.State != VerificationWrongProject {
		t.Fatalf("got %+v", got)
	}
}
