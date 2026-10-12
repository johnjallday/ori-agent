package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderpath"
)

// The picker's typed-path decision is the shared rule set plus Ori's own
// origin: a folder under the home passes, a refusal carries only its sentence,
// and a request from another site is turned away before any path is looked at.
func TestTypedFolderPaths_CheckAndAllow(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, outside := filepath.Join(base, "home"), filepath.Join(base, "sessions")
	projects := filepath.Join(home, "Music", "REAPER Projects")
	for _, dir := range []string{projects, outside, filepath.Join(home, "data")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	notes := filepath.Join(home, "Music", "notes.txt")
	if err := os.WriteFile(notes, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := newTypedFolderPaths(filejanitor.RootGuards{HomeDir: home, DataDir: filepath.Join(home, "data")})

	typed, err := paths.Check("~/Music/REAPER Projects")
	if err != nil || typed.Path != projects || typed.Warning != "" || typed.WarningNote != "" {
		t.Fatalf("Check = %+v, %v", typed, err)
	}
	away, err := paths.Check(outside)
	if err != nil || away.Path != outside || away.Warning != folderpath.WarningOutsideHome || !strings.Contains(away.WarningNote, "outside your home folder") {
		t.Fatalf("Check outside home = %+v, %v", away, err)
	}
	for raw, want := range map[string]string{
		"Music/REAPER Projects":              "Use the full path",
		filepath.Join(home, "Music", "Gone"): "no longer exists",
		notes:                                "a file, not a folder",
		filepath.Join(home, "data"):          "Ori's own data",
	} {
		typed, err := paths.Check(raw)
		if err == nil || !strings.Contains(err.Error(), want) || typed.Path != "" {
			t.Errorf("Check(%q) = %+v, %v; want a refusal containing %q", raw, typed, err, want)
		}
		if err != nil && strings.Contains(err.Error(), base) {
			t.Errorf("Check(%q) refusal repeats a server path: %q", raw, err)
		}
	}

	request := func(host, origin string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/folder-picker/select-path", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	for _, r := range []*http.Request{
		request("localhost:8765", "http://localhost:8765"),
		request("127.0.0.1:8931", "http://127.0.0.1:8931"),
		request("127.0.0.1:8931", ""),
	} {
		if err := paths.Allow(r); err != nil {
			t.Errorf("Allow(host=%q origin=%q) = %v, want Ori's own page allowed", r.Host, r.Header.Get("Origin"), err)
		}
	}
	for _, r := range []*http.Request{
		request("127.0.0.1:8931", "https://evil.example"),
		request("evil.example", "http://evil.example"),
	} {
		if err := paths.Allow(r); err == nil {
			t.Errorf("Allow(host=%q origin=%q) passed a request from another site", r.Host, r.Header.Get("Origin"))
		}
	}
}
