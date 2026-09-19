package agenthttp

import (
	"net/http"
	"net/url"
	"runtime"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// rootAgentFolderLocator is the composite store's view of where one of the
// user's agents lives in the Workspace Directory.
type rootAgentFolderLocator interface {
	RootAgentFolder(name string) (string, bool)
}

// SetDesktopOpener wires the native "open in Finder" boundary; tests pass a
// fake. Nil means the platform's own.
func (h *Handler) SetDesktopOpener(opener platform.DesktopOpener) {
	h.desktopOpener = opener
}

// IsAgentRevealPath reports whether path is POST /api/agents/{name}/reveal.
func IsAgentRevealPath(path string) bool {
	return strings.HasPrefix(path, "/api/agents/") && strings.HasSuffix(path, "/reveal")
}

// HandleReveal serves POST /api/agents/{name}/reveal: it opens the agent's
// folder in <Workspace Directory>/Agents in Finder, so the user can read or
// edit its files. macOS only, and only for the user's own agents. The folder
// comes from the agent store by name; no path is ever taken from the request.
func (h *Handler) HandleReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/agents/"), "/reveal")
	name, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(name) == "" || strings.Contains(name, "/") {
		orihttp.BadRequest(w, "agent name is required")
		return
	}
	locator, ok := h.State.(rootAgentFolderLocator)
	if !ok {
		orihttp.NotFound(w, "This agent has no folder in your Workspace Directory.")
		return
	}
	folder, ok := locator.RootAgentFolder(name)
	if !ok {
		orihttp.NotFound(w, "This agent has no folder in your Workspace Directory.")
		return
	}
	// Checked after the lookup, so an unknown agent is a 404 on every
	// platform and only a real folder meets the platform limit.
	if runtime.GOOS != "darwin" {
		orihttp.NotImplemented(w, "Show in Finder is available on macOS only.")
		return
	}
	opener := h.desktopOpener
	if opener == nil {
		opener = platform.NativeDesktopOpener{}
	}
	if err := opener.OpenFolder(folder); err != nil {
		logger.Warn("Failed to show an agent's folder", logger.Fields{"agent": name, "error": err.Error()})
		orihttp.InternalError(w, "The agent's folder could not be opened.")
		return
	}
	orihttp.Success(w, map[string]any{"success": true, "path": folder})
}
