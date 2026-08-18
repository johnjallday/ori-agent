package reapersetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestParseWebRemoteConfig(t *testing.T) {
	tests := []struct {
		name  string
		input string
		state ProbeState
		port  int
	}{
		{name: "real REAPER HTTP encoding", input: "[reaper]\ncsurf_0=HTTP 0 2307 '' 'index.html' 0 ''\n", state: ProbeReady, port: 2307},
		{name: "alternate HTTP mode", input: "csurf_0=HTTP 1 2308 '' 'index.html' 0 ''\n", state: ProbeReady, port: 2308},
		{name: "WEBR enabled", input: "csurf_2=WEBR 1 0 3210\n", state: ProbeReady, port: 3210},
		{name: "WEBR disabled", input: "csurf_2=WEBR 0 0 3210\n", state: ProbeMissing},
		{name: "default guess is not configuration", input: "lastproject=/music/song.rpp\n", state: ProbeMissing},
		{name: "bad port", input: "csurf_0=HTTP 1 70000\n", state: ProbeInvalid},
		{name: "bad enabled flag", input: "csurf_0=HTTP maybe 2307\n", state: ProbeInvalid},
		{name: "truncated", input: "csurf_0=HTTP 1\n", state: ProbeInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseWebRemoteConfig([]byte(test.input))
			if got.State != test.state || got.Port != test.port {
				t.Fatalf("got %+v, want state=%s port=%d", got, test.state, test.port)
			}
		})
	}

	multiple := parseWebRemoteConfig([]byte("csurf_0=HTTP 0 2307 '' 'index.html' 0 ''\ncsurf_1=HTTP 0 2308 '' 'index.html' 0 ''\ncsurf_2=HTTP 0 2308 '' 'index.html' 0 ''\n"))
	if multiple.State != ProbeReady || multiple.Port != 2307 || len(multiple.Ports) != 2 || multiple.Ports[0] != 2307 || multiple.Ports[1] != 2308 {
		t.Fatalf("multiple configured interfaces = %+v", multiple)
	}

	oversized := parseWebRemoteConfig([]byte(strings.Repeat("x", maxREAPERConfigBytes+1)))
	if oversized.State != ProbeUnknown {
		t.Fatalf("oversized state = %s", oversized.State)
	}
}

func TestRunnerCommandIDValidation(t *testing.T) {
	for _, valid := range []string{"_RSabc123", "_CUSTOM-42", "12345"} {
		if !validRunnerCommandID(valid) {
			t.Errorf("%q should be valid", valid)
		}
	}
	for _, invalid := range []string{"", "0", "_", "../runner", "id with spaces", strings.Repeat("A", 100)} {
		if validRunnerCommandID(invalid) {
			t.Errorf("%q should be invalid", invalid)
		}
	}
}

func TestAuthoritativeProjectRejectsMissingTraversalAndSymlinks(t *testing.T) {
	folder := t.TempDir()
	project := filepath.Join(folder, "song")
	if err := os.Mkdir(project, 0o750); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(project, "song.rpp")
	if err := os.WriteFile(entry, []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	ws.SharedData = map[string]any{}
	ws.ProjectPath = "song"
	if err := workspace.SetProjectEntryPath(ws.SharedData, "song.rpp"); err != nil {
		t.Fatal(err)
	}
	source := &runtimeTestSource{ws: ws, folder: folder}
	got, err := AuthoritativeProject(source, ws.ID)
	if err != nil || got != entry {
		t.Fatalf("project = %q, %v", got, err)
	}

	ws.ProjectPath = "../song"
	if _, err := AuthoritativeProject(source, ws.ID); err == nil {
		t.Fatal("traversing project path should fail")
	}
	ws.ProjectPath = "song"

	outside := filepath.Join(folder, "outside.rpp")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := AuthoritativeProject(source, ws.ID); err == nil {
		t.Fatal("symlinked project entry should fail")
	}
}

func TestSameProjectPathRejectsFilenameOnlyMatch(t *testing.T) {
	left := filepath.Join(t.TempDir(), "song.rpp")
	right := filepath.Join(t.TempDir(), "song.rpp")
	for _, path := range []string{left, right} {
		if err := os.WriteFile(path, []byte("project"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if sameProjectPath(left, right) {
		t.Fatal("different projects with the same filename must not match")
	}
}
