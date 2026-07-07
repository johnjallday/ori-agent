package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/templateonboarding"
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
		Name            string                          `json:"name"`
		Description     string                          `json:"description"`
		Tags            *[]string                       `json:"tags"`
		Icon            *string                         `json:"icon"`
		BehaviorProfile *string                         `json:"behavior_profile"`
		StarterTasks    *[]projecttemplates.StarterTask `json:"starter_tasks"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}

	edit := &projecttemplates.ManifestEdit{
		Icon:            req.Icon,
		BehaviorProfile: req.BehaviorProfile,
		StarterTasks:    req.StarterTasks,
	}
	tpl, err := projecttemplates.UpdateManifest(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Name, req.Description, req.Tags, edit)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "template": tpl})
}

// guardTemplateMutable rejects mutating operations on built-in templates,
// responding 403 and returning false. Built-ins are read-only; "Duplicate to
// customize" is the supported way to edit one.
func (s *Server) guardTemplateMutable(w http.ResponseWriter, templateID string) bool {
	if err := projecttemplates.EnsureMutable(resolveTemplatesRoot(s.Core.ConfigManager), templateID); err != nil {
		s.respondProjectTemplateError(w, err)
		return false
	}
	return true
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
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	trashed, err := projecttemplates.Delete(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"))
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "trashed": trashed})
}

// handleProjectTemplateFilesList serves GET
// /api/project-templates/{templateID}/files: the template's file/folder tree.
func (s *Server) handleProjectTemplateFilesList(w http.ResponseWriter, r *http.Request) {
	nodes, err := projecttemplates.ListTree(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"))
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"files": nodes})
}

// handleProjectTemplateFileRead serves GET
// /api/project-templates/{templateID}/files/content?path=<rel>: one file's
// contents for the editor (read-only for binary/manifest, 413 if oversized).
func (s *Server) handleProjectTemplateFileRead(w http.ResponseWriter, r *http.Request) {
	content, err := projecttemplates.ReadFileContent(
		resolveTemplatesRoot(s.Core.ConfigManager),
		r.PathValue("templateID"),
		r.URL.Query().Get("path"),
	)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, content)
}

// handleProjectTemplateFileWrite serves PUT
// /api/project-templates/{templateID}/files/content: overwrite an existing
// file's bytes verbatim. The file must already exist (use the create endpoint
// for new files).
func (s *Server) handleProjectTemplateFileWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	if err := projecttemplates.WriteFileContent(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Path, req.Content); err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "path": req.Path})
}

// handleProjectTemplateFileCreate serves POST
// /api/project-templates/{templateID}/files: create a new file or folder
// ({"path": ..., "type": "file"|"dir"}).
func (s *Server) handleProjectTemplateFileCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	node, err := projecttemplates.CreateEntry(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Path, req.Type)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondCreated(w, map[string]any{"success": true, "node": node})
}

// handleProjectTemplateFileRename serves POST
// /api/project-templates/{templateID}/files/rename: move a file or folder
// ({"from": ..., "to": ...}); the destination must not already exist.
func (s *Server) handleProjectTemplateFileRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	node, err := projecttemplates.RenameEntry(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.From, req.To)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "node": node})
}

// handleProjectTemplateFileDelete serves DELETE
// /api/project-templates/{templateID}/files?path=<rel>: remove a file or folder
// (recursive for folders).
func (s *Server) handleProjectTemplateFileDelete(w http.ResponseWriter, r *http.Request) {
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	path := r.URL.Query().Get("path")
	if err := projecttemplates.DeleteEntry(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), path); err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "path": path})
}

// handleProjectTemplateOnboardingGet serves GET
// /api/project-templates/{templateID}/onboarding: the parsed onboarding spec
// (plus the raw block for the JSON editor), or an absent/invalid state.
func (s *Server) handleProjectTemplateOnboardingGet(w http.ResponseWriter, r *http.Request) {
	tpl, err := projecttemplates.FindLibraryTemplate(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"))
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}

	resp := map[string]any{
		"present":    false,
		"onboarding": nil,
		"raw":        strings.TrimSpace(string(tpl.Onboarding)),
	}
	spec, perr := templateonboarding.ParseSpec(tpl.Onboarding)
	switch {
	case perr != nil:
		// Malformed block on disk: report it so the raw-JSON editor can repair it.
		resp["present"] = true
		resp["error"] = perr.Error()
	case spec != nil:
		resp["present"] = true
		resp["onboarding"] = spec
	}
	_ = orihttp.RespondSuccess(w, resp)
}

// handleProjectTemplateOnboardingSet serves PUT
// /api/project-templates/{templateID}/onboarding: validate the submitted spec
// (ParseSpec + Validate) and write it into template.json. A `null` body clears
// onboarding. Validation problems are returned as a 400 with a problems list.
func (s *Server) handleProjectTemplateOnboardingSet(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if !orihttp.ParseJSONBody(w, r, &body) {
		return
	}

	spec, perr := templateonboarding.ParseSpec(body)
	if perr != nil {
		_ = orihttp.RespondBadRequest(w, perr.Error())
		return
	}
	if spec != nil {
		if res := templateonboarding.Validate(spec); !res.OK() {
			_ = orihttp.RespondJSON(w, http.StatusBadRequest, map[string]any{
				"error":    "onboarding spec is invalid",
				"problems": res.Problems,
			})
			return
		}
	}

	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	tpl, err := projecttemplates.SetOnboarding(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), body)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "present": tpl.HasOnboarding()})
}

// handleProjectTemplateOnboardingDelete serves DELETE
// /api/project-templates/{templateID}/onboarding: remove the onboarding block.
func (s *Server) handleProjectTemplateOnboardingDelete(w http.ResponseWriter, r *http.Request) {
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	if _, err := projecttemplates.SetOnboarding(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), nil); err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "present": false})
}

// handleProjectTemplateToolsSet serves PUT
// /api/project-templates/{templateID}/tools: set the template's default tool
// bindings (skills / MCP servers / plugins), referenced by name. The names are
// applied (if present on the machine) when a workspace is created from the
// template; reading them is covered by the list endpoint's Template.Tools.
func (s *Server) handleProjectTemplateToolsSet(w http.ResponseWriter, r *http.Request) {
	var req projecttemplates.ToolDefaults
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	tpl, err := projecttemplates.SetTools(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "template": tpl})
}

// handleProjectTemplateAgentsSet serves PUT
// /api/project-templates/{templateID}/agents: set the template's agent roster
// (first = entry agent, rest = specialists). Seeding happens when a workspace is
// created from the template; reading is covered by the list endpoint's
// Template.Agents.
func (s *Server) handleProjectTemplateAgentsSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agents []projecttemplates.AgentSpec `json:"agents"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	tpl, err := projecttemplates.SetAgents(resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Agents)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "template": tpl})
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
	case errors.Is(err, projecttemplates.ErrTemplateNotFound), errors.Is(err, projecttemplates.ErrFileNotFound):
		_ = orihttp.RespondNotFound(w, err.Error())
	case errors.Is(err, projecttemplates.ErrInvalidTemplateName), errors.Is(err, projecttemplates.ErrInvalidPath), errors.Is(err, projecttemplates.ErrInvalidPromptVariable):
		_ = orihttp.RespondBadRequest(w, err.Error())
	case errors.Is(err, projecttemplates.ErrTemplateExists), errors.Is(err, projecttemplates.ErrFileExists):
		_ = orihttp.RespondConflict(w, err.Error())
	case errors.Is(err, projecttemplates.ErrTemplateReadOnly):
		_ = orihttp.RespondForbidden(w, err.Error())
	case errors.Is(err, projecttemplates.ErrFileTooLarge):
		_ = orihttp.RespondError(w, http.StatusRequestEntityTooLarge, err.Error())
	default:
		// Non-sentinel errors here are filesystem/server faults (copy
		// failures, unreadable library dir), not bad client input.
		logger.Error("Project template operation failed", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Project template operation failed")
	}
}
