package server

import (
	"encoding/json"
	"html/template"
	"io"
	"net"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/learningmissionshttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/web"
)

var deskTemplate = template.Must(template.ParseFS(web.Static, "static/search-desk.html"))

func localSearchPage(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(peer).IsLoopback() && connectionshttp.NewOriginGuard().Check(r) == nil
}
func (s *Server) maybeSearchLanding(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != "GET" || r.URL.ForceQuery || s.fileSearch == nil || !localSearchPage(r) {
		return false
	}
	state := onboarding.SearchLanding(config.DefaultDataDir())
	if r.URL.RawQuery == "home=1" && state != onboarding.SearchLegacy && !s.Storage.OnboardingMgr.IsOnboardingComplete() {
		s.searchPage(w, r, state, true)
		return true
	}
	if r.URL.RawQuery != "" {
		return false
	}
	switch state {
	case onboarding.SearchFirst:
		// A user may have followed a setup deep link before the first ordinary
		// visit. Preserve their progress rather than competing with Welcome.
		setup := s.Storage.OnboardingMgr.GetState()
		if setup.Completed || !setup.SkippedAt.IsZero() || setup.CurrentStep != 0 || len(setup.StepsCompleted) != 0 || s.Core.ConfigManager.IsWorkspaceRootConfirmed() {
			return false
		}
		s.searchPage(w, r, state, false)
		return true
	case onboarding.SearchDesk:
		// Files is Home's tree with one search box, so a user who made Files
		// their starting page lands there once a workspace exists. With none, the
		// first-run page is still what there is to show, as before.
		if s.hasWorkspace() {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/?view=tree", http.StatusFound)
			return true
		}
		s.searchPage(w, r, state, false)
		return true
	case onboarding.SearchLater:
		if !s.Storage.OnboardingMgr.IsOnboardingComplete() {
			s.searchPage(w, r, state, true)
			return true
		}
	}
	return false
}

// hasWorkspace reports whether any workspace exists, the same count Home's own
// first-run check uses.
func (s *Server) hasWorkspace() bool {
	if s.Storage == nil || s.Storage.WorkspaceStore == nil {
		return false
	}
	ids, err := s.Storage.WorkspaceStore.List()
	return err == nil && len(ids) > 0
}
func (s *Server) searchPage(w http.ResponseWriter, r *http.Request, state string, home bool) {
	if !localSearchPage(r) || s.fileSearch == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self'; style-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ready := s.folderSearchReady()
	_ = deskTemplate.Execute(w, struct {
		State                    string
		Home, First, MapEligible bool
	}{state, home, state == onboarding.SearchFirst && !ready, ready})
}
func (s *Server) searchPreference(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.fileSearch == nil || !localSearchPage(r) || r.URL.RawQuery != "" {
		http.Error(w, "Search unavailable.", http.StatusForbidden)
		return
	}
	_, err := learningmissionshttp.LocalOwner(r, s.Storage.UserProvider, true)
	if err != nil {
		http.Error(w, "Local browser required.", http.StatusForbidden)
		return
	}
	var req struct {
		Expected string `json:"expected"`
		Next     string `json:"next"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid decision.", http.StatusBadRequest)
		return
	}
	if onboarding.DecideSearchLanding(config.DefaultDataDir(), req.Expected, req.Next) != nil {
		http.Error(w, "Decision changed or unavailable. Reload to check.", http.StatusConflict)
		return
	}
	w.WriteHeader(204)
}
