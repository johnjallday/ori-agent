package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/filesearch"
	"github.com/johnjallday/ori-agent/internal/filesearchhttp"
	"github.com/johnjallday/ori-agent/internal/learningmissionshttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
)

func (b *ServerBuilder) initializeFileSearch() {
	if runtime.GOOS != "darwin" || b.sessionStore == nil || b.sessionStore.DB() == nil || b.configManager == nil {
		return
	}
	service, err := filesearch.New(context.Background(), filesearch.Options{DB: b.sessionStore.DB().DB, Gate: b.resetWork, Policy: func() filesearch.Policy {
		home, _ := os.UserHomeDir()
		protected := []string{config.DefaultDataDir(), config.DefaultWorkspaceRoot(), config.ResolveWorkspaceRoot(b.configManager.GetWorkspaceRoot()), config.ResolveTemplatesRoot(b.configManager.GetTemplatesRoot()), config.ResolveVaultRoot(b.configManager.GetVaultRoot())}
		if p := os.Getenv("AGENT_STORE_PATH"); p != "" {
			abs, e := filepath.Abs(p)
			if e != nil {
				return filesearch.Policy{}
			}
			protected = append(protected, filepath.Dir(abs))
		}
		return filesearch.Policy{Home: home, Protected: protected}
	}, Pick: pickSearchFolder})
	if err == nil {
		b.server.fileSearch = service
		if b.sessionHandler != nil {
			b.sessionHandler.SetFolderSearch(service, func(r *http.Request, owner string) bool {
				if !localSearchPage(r) || !b.server.folderSearchReady() {
					return false
				}
				current, e := learningmissionshttp.LocalOwner(r, b.userProvider, r.Method != http.MethodGet)
				site := r.Header.Get("Sec-Fetch-Site")
				return e == nil && current == owner && (site == "" || site == "same-origin")
			})
		}
	}
}
func (s *Server) folderSearchReady() bool {
	return s.Storage != nil && s.Storage.OnboardingMgr != nil && s.Storage.OnboardingMgr.IsOnboardingComplete()
}

func (s *Server) registerFileSearch(mux *http.ServeMux) {
	if runtime.GOOS == "darwin" && s.Storage != nil {
		mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
			s.searchPage(w, r, onboarding.SearchLanding(config.DefaultDataDir()), false)
		})
		mux.HandleFunc("POST /api/file-search/preference", s.searchPreference)
		mux.Handle(filesearchhttp.Prefix, filesearchhttp.Handler{Service: s.fileSearch, Users: s.Storage.UserProvider, WorkspaceReady: s.folderSearchReady, Accepted: func() error { return onboarding.AcceptSearchFolder(config.DefaultDataDir()) }})
	}
}
