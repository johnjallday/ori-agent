package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

func (b *ServerBuilder) wireProjectTemplateResolver() {
	if b == nil || b.sessionHandler == nil {
		return
	}
	b.sessionHandler.SetProjectTemplateResolver(func(templateID, templatePath string) (projecttemplates.Template, error) {
		catalog := templateRuntimeCatalog{
			capabilities: b.workspaceCapabilityRegistry,
			runtimes:     b.runtimeCapabilityRegistry,
		}
		if id := strings.TrimSpace(templateID); id != "" {
			if b.pluginHandler != nil {
				installed, err := b.pluginHandler.Manager().List()
				if err != nil {
					return projecttemplates.Template{}, err
				}
				for _, template := range activePluginBlueprintTemplates(installed) {
					if template.ID == id {
						return template, nil
					}
				}
			}
			if strings.HasPrefix(id, "plugin:") {
				return projecttemplates.Template{}, fmt.Errorf("%w: %q", projecttemplates.ErrTemplateNotFound, id)
			}
			return projecttemplates.FindLibraryTemplateWithCatalog(resolveTemplatesRoot(b.configManager), id, catalog)
		}
		if path := strings.TrimSpace(templatePath); path != "" {
			return projecttemplates.LoadFolderWithCatalog(path, catalog)
		}
		return projecttemplates.Template{}, errors.New("no template specified")
	})
}

type templateRuntimeCatalog struct {
	capabilities *workspacecapability.Registry
	runtimes     *runtimecapability.Registry
}

func (c templateRuntimeCatalog) HasCapability(id string) bool {
	return c.capabilities != nil && c.capabilities.Has(id)
}

func (c templateRuntimeCatalog) HasRuntimeAdapter(id string) bool {
	if c.runtimes == nil {
		return false
	}
	_, ok := c.runtimes.Lookup(id)
	return ok
}

// handleProjectTemplates serves GET /api/project-templates: the project
// template library available for workspace creation.
func (s *Server) handleProjectTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	root := resolveTemplatesRoot(s.Core.ConfigManager)
	var templates []projecttemplates.Template
	var err error
	if s.projectTemplateCatalog == nil {
		templates, err = projecttemplates.ListLibrary(root)
	} else {
		templates, err = projecttemplates.ListLibraryWithCatalog(root, s.projectTemplateCatalog)
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to read templates library")
		return
	}
	if templates == nil {
		templates = []projecttemplates.Template{}
	}
	if s.Handlers != nil && s.Handlers.Plugin != nil {
		installed, listErr := s.Handlers.Plugin.Manager().List()
		if listErr != nil {
			logger.Warn("Plugin blueprints could not be listed", logger.Fields{"error": listErr.Error()})
		} else {
			templates = mergeActivePluginBlueprints(templates, activePluginBlueprintTemplates(installed))
		}
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"templates":      templates,
		"templates_root": root,
	})
}

func mergeActivePluginBlueprints(existing, contributed []projecttemplates.Template) []projecttemplates.Template {
	if len(contributed) == 0 {
		return existing
	}
	superseded := make(map[string]struct{})
	for _, template := range contributed {
		if template.PluginOwner != nil && strings.TrimSpace(template.PluginOwner.BlueprintID) != "" {
			superseded[strings.TrimSpace(template.PluginOwner.BlueprintID)] = struct{}{}
		}
	}
	merged := make([]projecttemplates.Template, 0, len(existing)+len(contributed))
	for _, template := range existing {
		if template.Builtin {
			if _, replaced := superseded[template.ID]; replaced {
				continue
			}
		}
		merged = append(merged, template)
	}
	return append(merged, contributed...)
}

func activePluginBlueprintTemplates(installed []plugin.InstalledPlugin) []projecttemplates.Template {
	var templates []projecttemplates.Template
	for _, candidate := range installed {
		if !candidate.Enabled || candidate.WorkspaceSurfaces == nil || !pluginArtifactsAvailable(candidate.ResolvedArtifacts) {
			continue
		}
		for _, blueprint := range candidate.ResolvedBlueprints {
			template := blueprint.Template
			template.Path = blueprint.SkeletonRoot
			template.HasSkeleton = true
			templates = append(templates, template)
		}
	}
	return templates
}

func pluginArtifactsAvailable(artifacts []plugin.ResolvedArtifact) bool {
	for _, artifact := range artifacts {
		if !artifact.Available {
			return false
		}
	}
	return true
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
// edit a template's metadata (template.json). Optional tags and project_entry
// fields are tri-state so older clients preserve values they do not send;
// project_entry null explicitly clears that object.
func (s *Server) handleProjectTemplateUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                   string                                    `json:"name"`
		Description            string                                    `json:"description"`
		Tags                   *[]string                                 `json:"tags"`
		Icon                   *string                                   `json:"icon"`
		BehaviorProfile        *string                                   `json:"behavior_profile"`
		StarterTasks           *[]projecttemplates.StarterTask           `json:"starter_tasks"`
		ProjectEntry           json.RawMessage                           `json:"project_entry"`
		CapabilityRequirements *[]projecttemplates.CapabilityRequirement `json:"capability_requirements"`
		DirectoryRequirements  *[]projecttemplates.DirectoryRequirement  `json:"directory_requirements"`
		AutomationRecipes      *[]projecttemplates.AutomationRecipe      `json:"automation_recipes"`
		RuntimeRequirements    json.RawMessage                           `json:"runtime_requirements"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}

	var projectEntryEdit *projecttemplates.ProjectEntryEdit
	if req.ProjectEntry != nil {
		projectEntryEdit = &projecttemplates.ProjectEntryEdit{Set: true}
		if !bytes.Equal(bytes.TrimSpace(req.ProjectEntry), []byte("null")) {
			var entry projecttemplates.ProjectEntry
			if err := json.Unmarshal(req.ProjectEntry, &entry); err != nil {
				s.respondProjectTemplateError(w, fmt.Errorf("%w: project_entry must be an object: %v", projecttemplates.ErrInvalidProjectEntry, err))
				return
			}
			projectEntryEdit.Value = &entry
		}
	}

	edit := &projecttemplates.ManifestEdit{
		Icon:                   req.Icon,
		BehaviorProfile:        req.BehaviorProfile,
		StarterTasks:           req.StarterTasks,
		ProjectEntry:           projectEntryEdit,
		CapabilityRequirements: req.CapabilityRequirements,
		DirectoryRequirements:  req.DirectoryRequirements,
		AutomationRecipes:      req.AutomationRecipes,
		RuntimeRequirements:    req.RuntimeRequirements,
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
	opener := s.resolvedDesktopOpener()
	if strings.TrimSpace(req.ID) == "" {
		if err := projecttemplates.EnsureLibrary(root); err != nil {
			logger.Warn("Failed to prepare templates library for reveal", logger.Fields{"error": err})
		}
		if err := opener.OpenFolder(root); err != nil {
			_ = orihttp.RespondInternalError(w, "Failed to open templates folder")
			return
		}
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "path": root})
		return
	}

	var tpl projecttemplates.Template
	var err error
	if s.projectTemplateCatalog == nil {
		tpl, err = projecttemplates.FindLibraryTemplate(root, req.ID)
	} else {
		tpl, err = projecttemplates.FindLibraryTemplateWithCatalog(root, req.ID, s.projectTemplateCatalog)
	}
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	if err := opener.RevealInFileManager(tpl.Path); err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to reveal template")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "path": tpl.Path})
}

func (s *Server) respondProjectTemplateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projecttemplates.ErrTemplateNotFound), errors.Is(err, projecttemplates.ErrFileNotFound):
		_ = orihttp.RespondNotFound(w, err.Error())
	case errors.Is(err, projecttemplates.ErrInvalidTemplateName), errors.Is(err, projecttemplates.ErrInvalidPath), errors.Is(err, projecttemplates.ErrInvalidPromptVariable),
		errors.Is(err, projecttemplates.ErrInvalidStarterTasks), errors.Is(err, projecttemplates.ErrInvalidProjectEntry), errors.Is(err, projecttemplates.ErrRosterRequired),
		errors.Is(err, projecttemplates.ErrInvalidCapabilityRequirements), errors.Is(err, projecttemplates.ErrInvalidDirectoryRequirements), errors.Is(err, projecttemplates.ErrInvalidAutomationRecipes),
		errors.Is(err, projecttemplates.ErrInvalidRuntimeRequirements):
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
