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
	"github.com/johnjallday/ori-agent/internal/platform"
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
	}, Pick: func(ctx context.Context) (string, bool, error) {
		return platform.ChooseFolder(ctx, "Choose one folder for local filename search")
	}})
	if err == nil {
		b.server.fileSearch = service
	}
}
func (s *Server) registerFileSearch(mux *http.ServeMux) {
	if runtime.GOOS == "darwin" && s.Storage != nil {
		mux.Handle(filesearchhttp.Prefix, filesearchhttp.Handler{Service: s.fileSearch, Users: s.Storage.UserProvider})
	}
}
