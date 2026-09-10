package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
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

// blueprintCatalogEntry is one selectable blueprint plus the readiness
// projection describing whether it can actually be used right now.
//
// The template is embedded, so every field existing clients already read stays
// exactly where it was; `readiness` is purely additive.
type blueprintCatalogEntry struct {
	projecttemplates.Template
	Readiness blueprintreadiness.Readiness `json:"readiness"`
}

// handleProjectTemplates serves GET /api/project-templates: the project
// template library available for workspace creation, each entry carrying its
// current dependency readiness.
func (s *Server) handleProjectTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	root := resolveTemplatesRoot(s.Core.ConfigManager)
	entries, err := s.buildBlueprintCatalog(root)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to read templates library")
		return
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"templates":      entries,
		"templates_root": root,
	})
}

// buildBlueprintCatalog assembles the creation catalog: the library, the
// blueprints installed plugins contribute, and one readiness projection per
// entry derived from the same authoritative state the create gate uses.
func (s *Server) buildBlueprintCatalog(root string) ([]blueprintCatalogEntry, error) {
	catalog := s.projectTemplateCatalog
	var templates []projecttemplates.Template
	var err error
	if catalog == nil {
		templates, err = projecttemplates.ListLibrary(root)
	} else {
		templates, err = projecttemplates.ListLibraryWithCatalog(root, catalog)
	}
	if err != nil {
		return nil, err
	}

	sources := blueprintreadiness.Sources{Catalog: catalog}
	var candidates []pluginBlueprintCandidate
	if s.Handlers != nil && s.Handlers.Plugin != nil {
		installed, listErr := s.Handlers.Plugin.Manager().List()
		if listErr != nil {
			// A failed read is not "nothing is installed". Recording it here is
			// what turns a blueprint's card into "could not check — retry"
			// instead of silently offering a stale built-in as ready.
			logger.Warn("Plugin blueprints could not be listed", logger.Fields{"error": listErr.Error()})
			sources.DependencyStateUnavailable = true
		} else {
			sources.Installed = installed
			candidates = candidatePluginBlueprintTemplates(installed)
		}
	}

	merged := mergePluginBlueprintCandidates(templates, candidates)
	entries := make([]blueprintCatalogEntry, 0, len(merged))
	for _, template := range merged {
		entries = append(entries, blueprintCatalogEntry{
			Template:  template,
			Readiness: blueprintreadiness.Derive(template, sources),
		})
	}
	return entries, nil
}

// mergePluginBlueprintCandidates folds plugin-contributed blueprints into the
// library listing and decides which matching built-in each one replaces.
//
// An active candidate supersedes its matching built-in, as it always has: the
// plugin-owned manifest is the current definition of that blueprint. An inert
// candidate — one whose plugin is installed but disabled or otherwise not
// usable yet — supersedes only a RETIRED built-in. That is the case the
// substitution exists for: the app stopped shipping a blueprint, a plugin now
// owns it, and the user must see the plugin's version (and the one action that
// makes it work) rather than a shipped copy that is no longer maintained.
//
// The converse restriction matters just as much: an inert candidate must never
// displace a built-in the app still ships, or installing a plugin that cannot
// run here would take a working blueprint away.
func mergePluginBlueprintCandidates(existing []projecttemplates.Template, candidates []pluginBlueprintCandidate) []projecttemplates.Template {
	if len(candidates) == 0 {
		return existing
	}
	supersededByActive := make(map[string]struct{})
	supersededWhenRetired := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.Template.PluginOwner == nil {
			continue
		}
		blueprintID := strings.TrimSpace(candidate.Template.PluginOwner.BlueprintID)
		if blueprintID == "" {
			continue
		}
		if candidate.Active {
			supersededByActive[blueprintID] = struct{}{}
			continue
		}
		supersededWhenRetired[blueprintID] = struct{}{}
	}

	merged := make([]projecttemplates.Template, 0, len(existing)+len(candidates))
	for _, template := range existing {
		if template.Builtin {
			if _, replaced := supersededByActive[template.ID]; replaced {
				continue
			}
			if _, replaced := supersededWhenRetired[template.ID]; replaced && !projecttemplates.IsBuiltinStarterID(template.ID) {
				continue
			}
		}
		merged = append(merged, template)
	}
	for _, candidate := range candidates {
		merged = append(merged, candidate.Template)
	}
	return merged
}

// pluginBlueprintCandidate is one blueprint an installed plugin contributes,
// paired with whether that plugin can currently supply it. Active is carried
// explicitly rather than inferred from the template, so nothing downstream has
// to reconstruct the lifecycle decision from a display field.
type pluginBlueprintCandidate struct {
	Template projecttemplates.Template
	Active   bool
}

// candidatePluginBlueprintTemplates returns every blueprint an installed
// plugin contributes, including those whose plugin is currently disabled,
// unsupported, or incompatible.
//
// Listing an unusable blueprint is deliberate and is not the same as enabling
// it: an inert candidate carries no skeleton path, so nothing can be
// instantiated from it, and creation resolves only through
// activePluginBlueprintTemplates. It exists so the user can see the blueprint
// they installed a plugin for, learn why it is not ready, and take the one
// action that fixes it.
func candidatePluginBlueprintTemplates(installed []plugin.InstalledPlugin) []pluginBlueprintCandidate {
	var candidates []pluginBlueprintCandidate
	for _, entry := range installed {
		active := pluginBlueprintsActive(entry)
		for _, blueprint := range entry.ResolvedBlueprints {
			template := blueprint.Template
			if active {
				template.Path = blueprint.SkeletonRoot
				template.HasSkeleton = true
			} else {
				// Withholding the skeleton root keeps an inert candidate
				// uninstantiable by construction rather than by a check
				// somewhere else remembering to run.
				template.Path = ""
				template.HasSkeleton = false
			}
			candidates = append(candidates, pluginBlueprintCandidate{Template: template, Active: active})
		}
	}
	return candidates
}

func activePluginBlueprintTemplates(installed []plugin.InstalledPlugin) []projecttemplates.Template {
	var templates []projecttemplates.Template
	for _, candidate := range installed {
		if !pluginBlueprintsActive(candidate) {
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

// pluginBlueprintsActive reports whether an installed plugin's contributed
// blueprints may be instantiated: it is enabled, it actually declares a
// workspace surface contribution, every artifact this platform needs resolved,
// and its declared surface protocol range includes the running host.
//
// The protocol check is repeated here rather than trusted from install time
// because an Ori upgrade can move the host out of a range that was valid when
// the record was written.
func pluginBlueprintsActive(candidate plugin.InstalledPlugin) bool {
	if !candidate.Enabled || candidate.WorkspaceSurfaces == nil {
		return false
	}
	if !pluginArtifactsAvailable(candidate.ResolvedArtifacts) {
		return false
	}
	protocol := candidate.WorkspaceSurfaces.Protocol
	maximum := max(protocol.Max, protocol.Min)
	return protocol.Min <= plugin.SurfaceProtocolVersion && maximum >= plugin.SurfaceProtocolVersion
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
	if _, ok := s.currentTemplateAuthor(w, r); !ok {
		return
	}
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
	userID, ok := s.currentTemplateAuthor(w, r)
	if !ok {
		return
	}
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
	tpl, err := projecttemplates.UpdateManifestWithGuard(
		resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Name, req.Description,
		req.Tags, edit, s.userSetupQuestMutationGuard(r.Context(), userID), s.projectTemplateCatalog,
	)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "template": tpl})
}

type userSetupQuestIntegrationOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type userSetupQuestAuthoringResponse struct {
	Source       string                                     `json:"source"`
	TemplateID   string                                     `json:"template_id"`
	Editable     bool                                       `json:"editable"`
	Locked       bool                                       `json:"locked"`
	LockMessage  string                                     `json:"lock_message,omitempty"`
	RecoveryCode string                                     `json:"recovery_code,omitempty"`
	Eligibility  projecttemplates.UserSetupQuestEligibility `json:"eligibility"`
	Revision     string                                     `json:"revision"`
	Error        string                                     `json:"error,omitempty"`
	Quest        *projecttemplates.UserSetupQuest           `json:"user_setup_quest,omitempty"`
	Draft        projecttemplates.UserSetupQuestDraft       `json:"draft"`
	Integrations []userSetupQuestIntegrationOption          `json:"integrations"`
}

func (s *Server) userSetupQuestAuthoring(ctx context.Context, template projecttemplates.Template) userSetupQuestAuthoringResponse {
	draft := projecttemplates.DefaultUserSetupQuestDraft()
	if template.UserSetupQuest != nil {
		draft = projecttemplates.UserSetupQuestDraftFor(template.UserSetupQuest)
	}
	integrations := make([]userSetupQuestIntegrationOption, 0, len(reviewedintegration.All()))
	for _, entry := range reviewedintegration.All() {
		integrations = append(integrations, userSetupQuestIntegrationOption{Key: entry.Key, Label: entry.SourceLabel})
	}
	if _, ok := reviewedintegration.Get(draft.IntegrationKey); !ok && len(integrations) > 0 {
		draft.IntegrationKey = integrations[0].Key
	}
	response := userSetupQuestAuthoringResponse{
		Source: "user_template", TemplateID: template.ID,
		Editable:    !template.Builtin && template.PluginOwner == nil,
		Eligibility: template.UserSetupQuestEligibility,
		Revision:    template.UserSetupQuestRevision, Error: template.UserSetupQuestError,
		Quest: template.UserSetupQuest.Clone(), Draft: draft, Integrations: integrations,
	}
	if s != nil && s.setupJourneyStore != nil {
		bindings, err := s.setupJourneyStore.ListUserTemplateBindings(ctx, template.ID)
		if err != nil {
			response.Locked = true
			response.RecoveryCode = "binding_state_unavailable"
			response.LockMessage = "Ori could not verify durable setup bindings, so protected editing is unavailable. Retry after storage recovers."
		} else if len(bindings) > 0 {
			response.Locked = true
			response.LockMessage = "This setup definition is locked because setup has started. Duplicate the template to create a new editable version."
			for _, binding := range bindings {
				if binding.DefinitionDigest != projecttemplates.UserSetupQuestDefinitionDigest(template.UserSetupQuest) ||
					binding.ExecutionDigest != projecttemplates.UserSetupQuestExecutionDigest(template) {
					response.RecoveryCode = "protected_content_changed"
					response.LockMessage = "Protected setup content changed outside Ori after this journey started. Ori will not rebind it. Restore the original template or duplicate and create a new setup quest."
					break
				}
			}
		}
	}
	return response
}

func (s *Server) currentTemplateAuthor(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s == nil || s.Storage == nil || s.Storage.UserProvider == nil {
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "Template authoring ownership is unavailable")
		return "", false
	}
	userID, err := s.Storage.UserProvider.CurrentUserID(r.Context())
	if err != nil || strings.TrimSpace(userID) == "" {
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "Template authoring ownership is unavailable")
		return "", false
	}
	return strings.TrimSpace(userID), true
}

func (s *Server) userSetupQuestMutationGuard(ctx context.Context, userID string) projecttemplates.UserSetupQuestMutationGuard {
	return func(before, after projecttemplates.Template) error {
		_ = userID // Ownership is resolved at the boundary; the shared template locks after any user's first run.
		if s == nil || s.setupJourneyStore == nil {
			return nil
		}
		bindings, err := s.setupJourneyStore.ListUserTemplateBindings(ctx, before.ID)
		if err != nil {
			return err
		}
		if len(bindings) == 0 {
			return nil
		}
		definitionChanged := before.UserSetupQuestRevision != after.UserSetupQuestRevision
		executionChanged := projecttemplates.UserSetupQuestExecutionDigest(before) != projecttemplates.UserSetupQuestExecutionDigest(after)
		for _, binding := range bindings {
			if binding.DefinitionDigest != projecttemplates.UserSetupQuestDefinitionDigest(before.UserSetupQuest) ||
				binding.ExecutionDigest != projecttemplates.UserSetupQuestExecutionDigest(before) {
				if !definitionChanged && !executionChanged {
					continue
				}
				if binding.DefinitionDigest == projecttemplates.UserSetupQuestDefinitionDigest(after.UserSetupQuest) &&
					binding.ExecutionDigest == projecttemplates.UserSetupQuestExecutionDigest(after) {
					continue
				}
				return projecttemplates.ErrUserSetupQuestLocked
			}
			if definitionChanged || executionChanged {
				return projecttemplates.ErrUserSetupQuestLocked
			}
		}
		return nil
	}
}

func (s *Server) userSetupQuestContentGuard(ctx context.Context, userID string) projecttemplates.UserSetupQuestContentGuard {
	return func(template projecttemplates.Template) error {
		_ = userID
		if s == nil || s.setupJourneyStore == nil {
			return nil
		}
		bindings, err := s.setupJourneyStore.ListUserTemplateBindings(ctx, template.ID)
		if err != nil {
			return err
		}
		if len(bindings) == 0 {
			return nil
		}
		return projecttemplates.ErrUserSetupQuestLocked
	}
}

func (s *Server) loadUserSetupQuestTemplate(id string) (projecttemplates.Template, error) {
	return projecttemplates.FindLibraryTemplateWithCatalog(resolveTemplatesRoot(s.Core.ConfigManager), id, s.projectTemplateCatalog)
}

// handleUserSetupQuestGet returns authoring metadata only. It never calls the
// journey service or a canonical integration/workspace owner.
func (s *Server) handleUserSetupQuestGet(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "query parameters are not supported")
		return
	}
	if _, ok := s.currentTemplateAuthor(w, r); !ok {
		return
	}
	template, err := s.loadUserSetupQuestTemplate(r.PathValue("templateID"))
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, s.userSetupQuestAuthoring(r.Context(), template))
}

func (s *Server) handleUserSetupQuestPreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "query parameters are not supported")
		return
	}
	if _, ok := s.currentTemplateAuthor(w, r); !ok {
		return
	}
	var request struct {
		Quest json.RawMessage `json:"user_setup_quest"`
	}
	if err := decodeStrictTemplateRequest(w, r, &request); err != nil || len(bytes.TrimSpace(request.Quest)) == 0 || bytes.Equal(bytes.TrimSpace(request.Quest), []byte("null")) {
		_ = orihttp.RespondBadRequest(w, "user_setup_quest must be an object")
		return
	}
	var draft projecttemplates.UserSetupQuestDraft
	if err := decodeStrictJSON(request.Quest, &draft); err != nil {
		_ = orihttp.RespondBadRequest(w, "user_setup_quest contains unsupported or malformed fields")
		return
	}
	if _, ok := reviewedintegration.Get(draft.IntegrationKey); !ok {
		_ = orihttp.RespondBadRequest(w, "user_setup_quest must select a host-reviewed integration")
		return
	}
	template, err := s.loadUserSetupQuestTemplate(r.PathValue("templateID"))
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	preview, err := projecttemplates.PreviewUserSetupQuest(template, draft)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{
		"label": "Preview — no setup started", "source": "user_template", "template_id": template.ID,
		"user_setup_quest": preview,
	})
}

func (s *Server) handleUserSetupQuestPut(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "query parameters are not supported")
		return
	}
	userID, ok := s.currentTemplateAuthor(w, r)
	if !ok {
		return
	}
	var request struct {
		IfRevision string          `json:"if_revision"`
		Quest      json.RawMessage `json:"user_setup_quest"`
	}
	if err := decodeStrictTemplateRequest(w, r, &request); err != nil {
		_ = orihttp.RespondBadRequest(w, "setup quest save request is invalid")
		return
	}
	if len(request.IfRevision) != 64 {
		_ = orihttp.RespondBadRequest(w, "if_revision is required")
		return
	}
	if request.Quest == nil {
		template, err := s.loadUserSetupQuestTemplate(r.PathValue("templateID"))
		if err != nil {
			s.respondProjectTemplateError(w, err)
			return
		}
		_ = orihttp.RespondSuccess(w, s.userSetupQuestAuthoring(r.Context(), template))
		return
	}
	edit := projecttemplates.UserSetupQuestEdit{ExpectedRevision: request.IfRevision}
	if bytes.Equal(bytes.TrimSpace(request.Quest), []byte("null")) {
		edit.Remove = true
	} else {
		var draft projecttemplates.UserSetupQuestDraft
		if err := decodeStrictJSON(request.Quest, &draft); err != nil {
			_ = orihttp.RespondBadRequest(w, "user_setup_quest contains unsupported or malformed fields")
			return
		}
		if _, ok := reviewedintegration.Get(draft.IntegrationKey); !ok {
			_ = orihttp.RespondBadRequest(w, "user_setup_quest must select a host-reviewed integration")
			return
		}
		edit.Draft = &draft
	}
	updated, err := projecttemplates.UpdateUserSetupQuestWithCatalog(
		resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), edit,
		s.userSetupQuestMutationGuard(r.Context(), userID), s.projectTemplateCatalog,
	)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, s.userSetupQuestAuthoring(r.Context(), updated))
}

func decodeStrictTemplateRequest(w http.ResponseWriter, r *http.Request, target any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 40<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain exactly one object")
	}
	return nil
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("value must contain exactly one object")
	}
	return nil
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
	if _, ok := s.currentTemplateAuthor(w, r); !ok {
		return
	}
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
	if _, ok := s.currentTemplateAuthor(w, r); !ok {
		return
	}
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
	userID, ok := s.currentTemplateAuthor(w, r)
	if !ok || !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	trashed, err := projecttemplates.DeleteWithGuard(
		resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"),
		s.userSetupQuestContentGuard(r.Context(), userID), s.projectTemplateCatalog,
	)
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
	userID, ok := s.currentTemplateAuthor(w, r)
	if !ok {
		return
	}
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
	if err := projecttemplates.WriteFileContentWithGuard(
		resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Path, req.Content,
		s.userSetupQuestContentGuard(r.Context(), userID), s.projectTemplateCatalog,
	); err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "path": req.Path})
}

// handleProjectTemplateFileCreate serves POST
// /api/project-templates/{templateID}/files: create a new file or folder
// ({"path": ..., "type": "file"|"dir"}).
func (s *Server) handleProjectTemplateFileCreate(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentTemplateAuthor(w, r)
	if !ok {
		return
	}
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
	node, err := projecttemplates.CreateEntryWithGuard(
		resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Path, req.Type,
		s.userSetupQuestContentGuard(r.Context(), userID), s.projectTemplateCatalog,
	)
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
	userID, ok := s.currentTemplateAuthor(w, r)
	if !ok {
		return
	}
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
	node, err := projecttemplates.RenameEntryWithGuard(
		resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.From, req.To,
		s.userSetupQuestContentGuard(r.Context(), userID), s.projectTemplateCatalog,
	)
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
	userID, ok := s.currentTemplateAuthor(w, r)
	if !ok || !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	path := r.URL.Query().Get("path")
	if err := projecttemplates.DeleteEntryWithGuard(
		resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), path,
		s.userSetupQuestContentGuard(r.Context(), userID), s.projectTemplateCatalog,
	); err != nil {
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
	userID, ok := s.currentTemplateAuthor(w, r)
	if !ok {
		return
	}
	var req projecttemplates.ToolDefaults
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	tpl, err := projecttemplates.SetToolsWithGuard(
		resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req,
		s.userSetupQuestMutationGuard(r.Context(), userID), s.projectTemplateCatalog,
	)
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
	userID, ok := s.currentTemplateAuthor(w, r)
	if !ok {
		return
	}
	var req struct {
		Agents []projecttemplates.AgentSpec `json:"agents"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if !s.guardTemplateMutable(w, r.PathValue("templateID")) {
		return
	}
	tpl, err := projecttemplates.SetAgentsWithGuard(
		resolveTemplatesRoot(s.Core.ConfigManager), r.PathValue("templateID"), req.Agents,
		s.userSetupQuestMutationGuard(r.Context(), userID), s.projectTemplateCatalog,
	)
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
		errors.Is(err, projecttemplates.ErrInvalidRuntimeRequirements), errors.Is(err, projecttemplates.ErrInvalidUserSetupQuest):
		_ = orihttp.RespondBadRequest(w, err.Error())
	case errors.Is(err, projecttemplates.ErrTemplateExists), errors.Is(err, projecttemplates.ErrFileExists),
		errors.Is(err, projecttemplates.ErrUserSetupQuestStale), errors.Is(err, projecttemplates.ErrUserSetupQuestLocked):
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
