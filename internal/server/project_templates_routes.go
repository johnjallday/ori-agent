package server

import (
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
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

// handleProjectTemplateImport serves POST /api/project-templates/import:
// copy an arbitrary folder into the library as a new template.
func (s *Server) handleProjectTemplateImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		_ = orihttp.RespondBadRequest(w, "path is required")
		return
	}

	tpl, err := projecttemplates.ImportFolder(resolveTemplatesRoot(s.Core.ConfigManager), req.Path, req.Name)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondCreated(w, map[string]any{"success": true, "template": tpl})
}

// handleProjectTemplateUpdate serves PUT /api/project-templates/{templateID}:
// edit a template's display metadata (template.json). The optional `tags` field
// is tri-state: omitted preserves existing tags, an explicit empty array clears
// them — so the legacy manage modal (which never sends tags) can't wipe them.
func (s *Server) handleProjectTemplateUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string    `json:"name"`
		Description string    `json:"description"`
		Tags        *[]string `json:"tags"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	tpl, err := projecttemplates.UpdateManifest(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Name, req.Description, req.Tags)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "template": tpl})
}

// handleProjectTemplateCreate serves POST /api/project-templates: create a new,
// empty template in the library from a display name.
func (s *Server) handleProjectTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	tpl, err := projecttemplates.CreateBlank(resolveTemplatesRoot(s.Core.ConfigManager), req.Name)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondCreated(w, map[string]any{"success": true, "template": tpl})
}

// handleProjectTemplateDuplicate serves POST
// /api/project-templates/{templateID}/duplicate: copy an existing template into a
// new one. The (optional) `name` seeds the copy's display name and id; send `{}`
// for a default "<source> copy".
func (s *Server) handleProjectTemplateDuplicate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	tpl, err := projecttemplates.Duplicate(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Name)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondCreated(w, map[string]any{"success": true, "template": tpl})
}

// handleProjectTemplateDelete serves DELETE /api/project-templates/{templateID}.
// The template goes to the system trash when supported (recoverable); the
// response reports which path was taken. Deleted starter templates reappear
// on the next server start (materialize-if-absent).
func (s *Server) handleProjectTemplateDelete(w http.ResponseWriter, r *http.Request) {
	trashed, err := projecttemplates.Delete(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"))
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "trashed": trashed})
}

// handleProjectTemplateReveal serves POST /api/project-templates/reveal:
// open the library root ({} or empty id) or reveal one template ({"id": ...})
// in the OS file manager. Local-first only, like workspace file open.
func (s *Server) handleProjectTemplateReveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	root := resolveTemplatesRoot(s.Core.ConfigManager)
	if strings.TrimSpace(req.ID) == "" {
		if err := projecttemplates.EnsureLibrary(root); err != nil {
			logger.Warn("Failed to prepare templates library for reveal", logger.Fields{"error": err})
		}
		if err := platform.OpenFolder(root); err != nil {
			_ = orihttp.RespondInternalError(w, "Failed to open templates folder")
			return
		}
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "path": root})
		return
	}

	tpl, err := projecttemplates.FindLibraryTemplate(root, req.ID)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	if err := platform.RevealInFileManager(tpl.Path); err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to reveal template")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "path": tpl.Path})
}

func (s *Server) respondProjectTemplateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projecttemplates.ErrTemplateNotFound):
		_ = orihttp.RespondNotFound(w, err.Error())
	case errors.Is(err, projecttemplates.ErrInvalidTemplateName):
		_ = orihttp.RespondBadRequest(w, err.Error())
	case errors.Is(err, projecttemplates.ErrTemplateExists):
		_ = orihttp.RespondConflict(w, err.Error())
	default:
		// Non-sentinel errors here are filesystem/server faults (copy
		// failures, unreadable library dir), not bad client input.
		logger.Error("Project template operation failed", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Project template operation failed")
	}
}
