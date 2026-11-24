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
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	}, Pick: pickSearchFolder, ValidLink: func(owner, path string, m filesearch.WorkspaceMatch) bool {
		if b.workspaceFileStore == nil {
			return false
		}
		ws, err := b.workspaceFileStore.Get(m.ID)
		if err != nil || ws.ID != m.ID || ws.OwnerUserID != owner || ws.GetStatus() == workspace.StatusTrashed || ws.GetStatus() == workspace.StatusMissing {
			return false
		}
		ref, err := ws.GetDirectoryReference(m.Link)
		return err == nil && ref.WorkspaceID == ws.ID && ref.Purpose == "" && ref.Path == path
	}})
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
			// Once a workspace exists, searchable folders are managed in Files
			// (merge-tree-into-files PR 2, requirement 41): /search opens it. A
			// scoped arrival travels in the tab's own Files place, never in the
			// address, so nothing of the request is carried over. With no
			// workspace this is still the first-run page.
			if s.fileSearch != nil && localSearchPage(r) && s.hasWorkspace() {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, "/?view=tree", http.StatusFound)
				return
			}
			s.searchPage(w, r, onboarding.SearchLanding(config.DefaultDataDir()), false)
		})
		mux.HandleFunc("POST /api/file-search/preference", s.searchPreference)
		mux.Handle(filesearchhttp.Prefix, filesearchhttp.Handler{Service: s.fileSearch, Users: s.Storage.UserProvider, WorkspaceReady: s.folderSearchReady, Accepted: func() error { return onboarding.AcceptSearchFolder(config.DefaultDataDir()) }})
	}
}
