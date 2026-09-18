package agenthttp

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
)

type recordingOpener struct{ opened []string }

func (o *recordingOpener) OpenFolder(path string) error {
	o.opened = append(o.opened, path)
	return nil
}
func (o *recordingOpener) OpenFile(string) error            { return nil }
func (o *recordingOpener) RevealInFileManager(string) error { return nil }

func TestShowInFinderOpensTheAgentsFolderInTheRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Show in Finder is macOS only")
	}
	root := t.TempDir()
	c := compositeForRoot(t, root)
	for _, name := range []string{"Scout", systemassistant.CanonicalName} {
		if err := c.CreateAgent(name, &store.CreateAgentConfig{}); err != nil {
			t.Fatalf("CreateAgent %s: %v", name, err)
		}
	}
	opener := &recordingOpener{}
	h := New(c)
	h.SetDesktopOpener(opener)
	reveal := func(path string) int {
		rr := httptest.NewRecorder()
		h.HandleReveal(rr, httptest.NewRequest(http.MethodPost, path, nil))
		return rr.Code
	}

	if code := reveal("/api/agents/Scout/reveal"); code != http.StatusOK {
		t.Fatalf("reveal Scout: %d", code)
	}
	if len(opener.opened) != 1 || opener.opened[0] != filepath.Join(root, "Agents", "Scout") {
		t.Fatalf("opened %v, want Scout's folder in the root", opener.opened)
	}

	// Only the user's own agents have a folder to show.
	if code := reveal("/api/agents/Ask%20Ori/reveal"); code != http.StatusNotFound {
		t.Errorf("reveal the built-in assistant: %d, want 404", code)
	}
	if code := reveal("/api/agents/..%2F..%2Fetc/reveal"); code != http.StatusBadRequest && code != http.StatusNotFound {
		t.Errorf("a traversal attempt: %d, want 400 or 404", code)
	}
	if len(opener.opened) != 1 {
		t.Errorf("something else was opened: %v", opener.opened)
	}
}
