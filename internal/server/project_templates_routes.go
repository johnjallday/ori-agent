package server

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// handleProjectTemplates serves GET /api/project-templates: the project
// template library available for workspace creation.
func (s *Server) handleProjectTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	root := resolveTemplatesRoot(s.Core.ConfigManager)
	templates, err := projecttemplates.ListLibrary(root)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to read templates library")
		return
	}
	if templates == nil {
		templates = []projecttemplates.Template{}
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"templates":      templates,
		"templates_root": root,
	})
}
