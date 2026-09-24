package skillshttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type Handler struct {
	manager        *skills.Manager
	store          store.Store
	llmFactory     *llm.Factory
	configManager  *config.Manager
	skillsCLIInDir func(ctx context.Context, workingDir string, args ...string) (string, error)
}

var (
	ansiEscapePattern           = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	marketplacePackagePattern   = regexp.MustCompile(`^([A-Za-z0-9._-]+/[A-Za-z0-9._-]+@[A-Za-z0-9._-]+)\b`)
	marketplaceInstallsPattern  = regexp.MustCompile(`([0-9][0-9.,]*(?:[KMB])?\s+installs)\b`)
	marketplaceSkillNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

const (
	marketplaceSearchTimeout  = 45 * time.Second
	marketplaceInstallTimeout = 2 * time.Minute
	marketplaceUpdateTimeout  = 2 * time.Minute
	skillCreateTimeout        = 75 * time.Second
	skillPromptTimeout        = 45 * time.Second
	marketplaceMaxResults     = 24
	marketplaceMaxInstalled   = 200
	marketplaceOutputMaxChars = 3500
)

func New(manager *skills.Manager, st store.Store, llmFactory *llm.Factory, cfg *config.Manager) *Handler {
	return &Handler{
		manager:        manager,
		store:          st,
		llmFactory:     llmFactory,
		configManager:  cfg,
		skillsCLIInDir: runSkillsCLIInDir,
	}
}

// Manager returns the underlying skills manager, so other handlers (e.g. the
// catalog-create path in agenthttp) can enable skills without duplicating
// manager wiring.
func (h *Handler) Manager() *skills.Manager {
	return h.manager
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listSkills(w, r)
	case http.MethodPost:
		h.createSkill(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/skills/")
	path = strings.TrimSpace(path)
	if path == "" {
		orihttp.BadRequest(w, "skill name required")
		return
	}

	if path == "marketplace" || strings.HasPrefix(path, "marketplace/") {
		h.handleMarketplace(w, r, path)
		return
	}
	if path == "generate-prompt" {
		h.generateSkillPrompt(w, r)
		return
	}

	parts := strings.Split(path, "/")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		orihttp.BadRequest(w, "skill name required")
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.getSkill(w, r, name)
		case http.MethodPut:
			h.updateSkill(w, r, name)
		case http.MethodDelete:
			h.deleteSkill(w, r, name)
		default:
			orihttp.MethodNotAllowed(w)
		}
		return
	}

	if len(parts) == 2 {
		switch parts[1] {
		case "enable":
			h.setSkillEnabled(w, r, name)
			return
		case "trust":
			h.setSkillTrusted(w, r, name)
			return
		}
	}

	orihttp.NotFound(w, "skill endpoint not found")
}

type skillWriteRequest struct {
	Agent       string  `json:"agent"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Prompt      string  `json:"prompt"`
	OpenAIYAML  *string `json:"openai_yaml,omitempty"`
}

type skillStateRequest struct {
	Agent   string `json:"agent"`
	Enabled *bool  `json:"enabled,omitempty"`
	Trusted *bool  `json:"trusted,omitempty"`
}

type marketplaceSearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type marketplaceInstallRequest struct {
	Package string `json:"package"`
}

type marketplaceRemoveRequest struct {
	Skill string `json:"skill"`
}

type skillPromptGenerateRequest struct {
	Agent       string `json:"agent"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type marketplaceSkillResult struct {
	Package    string `json:"package"`
	Repository string `json:"repository"`
	Skill      string `json:"skill"`
	URL        string `json:"url,omitempty"`
	Installs   string `json:"installs,omitempty"`
}

func (h *Handler) listSkills(w http.ResponseWriter, r *http.Request) {
	agentName := resolveAgentName(r, h.store)
	skillsList, err := h.manager.ListSkills(agentName)
	if err != nil {
		var conflicts *skills.SkillConflictError
		if errors.As(err, &conflicts) {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error":     err.Error(),
				"conflicts": conflicts.Conflicts,
				"agent":     agentName,
			})
			return
		}
		orihttp.InternalError(w, err.Error())
		return
	}

	response := map[string]any{
		"agent":  agentName,
		"skills": skillsList,
	}
	if loadout := h.agentLoadout(agentName, skillsList); loadout != nil {
		response["loadout"] = loadout
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// agentLoadout returns the agent's active-skill slot budget (stage, cap, used,
// expert flag) so the UI can render slot usage and disable the enable control
// at cap (PRD FR11). Returns nil for agents not in the store (e.g. CLI agents
// or the global/no-agent listing), which the UI treats as "no cap".
func (h *Handler) agentLoadout(agentName string, skillsList []skills.Skill) map[string]any {
	if h.store == nil || strings.TrimSpace(agentName) == "" {
		return nil
	}
	ag, ok := h.store.GetAgent(agentName)
	if !ok || ag == nil {
		return nil
	}

	stage := types.AgentStageSpark
	if ag.Evolution != nil && ag.Evolution.Stage != "" {
		stage = ag.Evolution.Stage
	}
	used := 0
	for _, s := range skillsList {
		if s.Enabled {
			used++
		}
	}
	return map[string]any{
		"stage":       string(stage),
		"slot_cap":    types.SkillSlotsForStage(stage),
		"slots_used":  used,
		"expert_mode": ag.Metadata.IsExpertMode(ag.Role),
	}
}

func (h *Handler) createSkill(w http.ResponseWriter, r *http.Request) {
	var req skillWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	agentName := resolveAgentNameWithFallback(req.Agent, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		orihttp.BadRequest(w, "name is required")
		return
	}

	if existing, found, err := h.manager.GetSkill(agentName, name); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "failed to create skill", err)
		return
	} else if found && existing != nil {
		orihttp.RespondErrorWithErr(w, http.StatusConflict, "skill already exists", skills.ErrSkillExists)
		return
	}

	skillDir, err := h.manager.AgentSkillDir(agentName, name)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "failed to create skill", err)
		return
	}

	if _, statErr := os.Stat(skillDir); statErr == nil {
		orihttp.RespondErrorWithErr(w, http.StatusConflict, "skill already exists", skills.ErrSkillExists)
		return
	} else if !os.IsNotExist(statErr) {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "failed to create skill", statErr)
		return
	}

	skillsRootDir := filepath.Dir(skillDir)
	if err := os.MkdirAll(skillsRootDir, 0o755); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "failed to create skill", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), skillCreateTimeout)
	defer cancel()

	initializedWithCLI, err := h.tryInitializeSkillTemplate(ctx, skillsRootDir, skillDir, name)
	if err != nil {
		if errors.Is(err, skills.ErrSkillExists) {
			orihttp.RespondErrorWithErr(w, http.StatusConflict, "skill already exists", err)
			return
		}
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "failed to create skill", err)
		return
	}

	input := skills.SkillInput{
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Prompt:      req.Prompt,
		OpenAIYAML:  req.OpenAIYAML,
	}

	var skill skills.Skill
	if initializedWithCLI {
		skill, err = h.manager.UpdateSkill(agentName, name, input)
		if errors.Is(err, skills.ErrSkillNotFound) {
			_ = os.RemoveAll(skillDir)
			skill, err = h.manager.CreateSkill(agentName, input)
			initializedWithCLI = false
		}
	} else {
		skill, err = h.manager.CreateSkill(agentName, input)
	}

	if err != nil {
		if initializedWithCLI {
			_ = os.RemoveAll(skillDir)
		}
		switch {
		case errors.Is(err, skills.ErrSkillExists):
			orihttp.RespondErrorWithErr(w, http.StatusConflict, "skill already exists", err)
		default:
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "failed to create skill", err)
		}
		return
	}

	// Creating a skill is an explicit opt-in: enable it for this agent so it is
	// active immediately regardless of which create path (CLI scaffold or local
	// fallback) produced it. The global default for other skills stays disabled.
	if enableErr := h.manager.SetSkillEnabled(agentName, name, true); enableErr == nil {
		skill.Enabled = true
	}

	orihttp.Created(w, skill)
}

func (h *Handler) generateSkillPrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req skillPromptGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}

	description := strings.TrimSpace(req.Description)
	if description == "" {
		orihttp.BadRequest(w, "description is required")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "new-skill"
	}

	agentName := resolveAgentNameWithFallback(req.Agent, h.store)
	provider, model, reasoningEffort, err := h.resolvePromptProvider(agentName)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusServiceUnavailable, "no LLM provider available for prompt generation", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), skillPromptTimeout)
	defer cancel()

	prompt, err := h.generatePromptBody(ctx, provider, model, reasoningEffort, name, description)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusBadGateway, "failed to generate prompt", err)
		return
	}

	orihttp.Success(w, map[string]any{
		"prompt": prompt,
		"model":  model,
	})
}

func (h *Handler) resolvePromptProvider(agentName string) (llm.Provider, string, string, error) {
	if h.llmFactory == nil || h.configManager == nil {
		return nil, "", "", fmt.Errorf("LLM factory is not configured")
	}

	if agentName != "" && h.store != nil {
		if ag, ok := h.store.GetAgent(agentName); ok && ag != nil {
			model := strings.TrimSpace(ag.Settings.Model)
			providerName := resolveSkillPromptProviderName(h.llmFactory, ag.Settings.Provider, model)
			model = normalizeSkillPromptModelForProvider(providerName, model)
			if providerName != "" && model != "" {
				provider, err := h.llmFactory.GetProvider(providerName)
				if err == nil {
					return provider, model, ag.Settings.EffectiveReasoningEffort(providerName), nil
				}
			}
		}
	}

	systemProvider, systemModel := h.configManager.GetSystemModel()
	result, err := h.llmFactory.GetSystemModelProvider(systemProvider, systemModel)
	if err != nil {
		return nil, "", "", err
	}
	return result.Provider, result.Model, h.configManager.GetSystemReasoningEffort(), nil
}

func (h *Handler) runSkillsCLIInDir(ctx context.Context, workingDir string, args ...string) (string, error) {
	if h != nil && h.skillsCLIInDir != nil {
		return h.skillsCLIInDir(ctx, workingDir, args...)
	}
	return runSkillsCLIInDir(ctx, workingDir, args...)
}

func (h *Handler) tryInitializeSkillTemplate(ctx context.Context, skillsRootDir, skillDir, name string) (bool, error) {
	output, err := h.runSkillsCLIInDir(ctx, skillsRootDir, "init", name)
	if skillsInitAlreadyExists(output) {
		return false, skills.ErrSkillExists
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	if _, statErr := os.Stat(skillPath); statErr == nil {
		return true, nil
	}

	if err == nil {
		_ = os.RemoveAll(skillDir)
		return false, nil
	}

	_ = os.RemoveAll(skillDir)
	return false, nil
}

func normalizeSkillPromptProviderName(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case "anthropic":
		return "claude"
	default:
		return normalized
	}
}

func isSkillPromptClaudeFamilyModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "claude-") {
		return true
	}
	return normalized == "haiku" || normalized == "sonnet" || normalized == "opus"
}

func isSkillPromptGeminiFamilyModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(normalized, "gemini")
}

func isSkillPromptCodexFamilyModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(normalized, "codex")
}

func inferSkillPromptProviderName(factory *llm.Factory, model string) string {
	trimmedModel := strings.TrimSpace(model)
	normalizedModel := strings.ToLower(trimmedModel)
	if trimmedModel == "" {
		return ""
	}

	if normalizedModel == "haiku" || normalizedModel == "sonnet" || normalizedModel == "opus" {
		if factory != nil && factory.HasProvider("claude_code") {
			return "claude_code"
		}
		if factory != nil && factory.HasProvider("claude") {
			return "claude"
		}
		return "claude"
	}

	if strings.HasPrefix(normalizedModel, "claude-") {
		return "claude"
	}
	if strings.HasPrefix(normalizedModel, "gemini") {
		return "gemini"
	}
	if strings.HasPrefix(normalizedModel, "codex") {
		return "codex"
	}
	if localProvider := llm.FindLocalProviderByModel(factory, trimmedModel); localProvider != "" {
		return localProvider
	}
	return "openai"
}

func resolveSkillPromptProviderName(factory *llm.Factory, configuredProvider, model string) string {
	explicitProvider := normalizeSkillPromptProviderName(configuredProvider)
	inferredProvider := inferSkillPromptProviderName(factory, model)

	if explicitProvider == "" {
		return inferredProvider
	}

	if factory != nil && factory.HasProvider(explicitProvider) {
		if explicitProvider == "openai" &&
			inferredProvider != "" &&
			inferredProvider != "openai" &&
			(isSkillPromptClaudeFamilyModel(model) || isSkillPromptGeminiFamilyModel(model) || isSkillPromptCodexFamilyModel(model)) {
			if factory.HasProvider(inferredProvider) {
				return inferredProvider
			}
			return explicitProvider
		}

		if explicitProvider == "claude" && isSkillPromptClaudeFamilyModel(model) &&
			(strings.EqualFold(strings.TrimSpace(model), "haiku") ||
				strings.EqualFold(strings.TrimSpace(model), "sonnet") ||
				strings.EqualFold(strings.TrimSpace(model), "opus")) &&
			factory.HasProvider("claude_code") {
			return "claude_code"
		}

		return explicitProvider
	}

	if factory != nil && inferredProvider != "" && factory.HasProvider(inferredProvider) {
		return inferredProvider
	}

	return explicitProvider
}

func normalizeSkillPromptModelForProvider(providerName, model string) string {
	trimmedModel := strings.TrimSpace(model)
	normalizedModel := strings.ToLower(trimmedModel)

	if providerName == "claude" {
		switch normalizedModel {
		case "haiku":
			return "claude-3-5-haiku-latest"
		case "sonnet":
			return "claude-3-5-sonnet-latest"
		case "opus":
			return "claude-3-opus-latest"
		}
	}

	return trimmedModel
}

func (h *Handler) generatePromptBody(ctx context.Context, provider llm.Provider, model, reasoningEffort, name, description string) (string, error) {
	systemPrompt := `You write prompt bodies for AI coding assistant skills.

Return only the prompt text with no markdown fences or explanations.
Write 8-20 lines that are concise, specific, and actionable.
Include clear execution steps and explicit output expectations.
Only allow clarifying questions when essential input is missing.`

	userPrompt := fmt.Sprintf("Skill name: %s\nSkill description: %s", strings.TrimSpace(name), strings.TrimSpace(description))

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		SystemPrompt:    systemPrompt,
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: userPrompt,
			},
		},
		Temperature: 0.2,
		MaxTokens:   700,
	})
	if err != nil {
		return "", err
	}

	prompt := sanitizeGeneratedPrompt(resp.Content)
	if prompt == "" {
		return "", fmt.Errorf("empty prompt generated")
	}
	return prompt, nil
}

func sanitizeGeneratedPrompt(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}

	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) > 0 {
			lines = lines[1:]
		}
		if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) == "```" {
			lines = lines[:n-1]
		}
		text = strings.TrimSpace(strings.Join(lines, "\n"))
	}

	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "prompt:") {
		text = strings.TrimSpace(text[len("prompt:"):])
	}

	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned = append(cleaned, strings.TrimRight(line, " \t"))
	}

	for len(cleaned) > 0 && strings.TrimSpace(cleaned[0]) == "" {
		cleaned = cleaned[1:]
	}
	for len(cleaned) > 0 && strings.TrimSpace(cleaned[len(cleaned)-1]) == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	if len(cleaned) > 20 {
		cleaned = cleaned[:20]
	}

	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func (h *Handler) getSkill(w http.ResponseWriter, r *http.Request, name string) {
	agentName := resolveAgentName(r, h.store)
	skill, found, err := h.manager.GetSkill(agentName, name)
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	if !found {
		orihttp.NotFound(w, "skill not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(skill)
}

func (h *Handler) updateSkill(w http.ResponseWriter, r *http.Request, name string) {
	var req skillWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	agentName := resolveAgentNameWithFallback(req.Agent, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}

	existing, found, err := h.manager.GetSkill(agentName, name)
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	if !found || existing == nil {
		orihttp.NotFound(w, skills.ErrSkillNotFound.Error())
		return
	}

	input := skills.SkillInput{
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Prompt:      req.Prompt,
		OpenAIYAML:  req.OpenAIYAML,
	}

	var skill skills.Skill
	switch existing.Source {
	case skills.SourceAgent:
		skill, err = h.manager.UpdateSkill(agentName, name, input)
	case skills.SourcePersonal, skills.SourceAgentsCompat:
		skill, err = h.manager.UpdateSkillAtPath(existing.Source, existing.Path, name, input)
	default:
		orihttp.RespondErrorWithErr(w, http.StatusForbidden, "skill source is read-only", skills.ErrSkillReadOnly)
		return
	}

	if err != nil {
		switch {
		case errors.Is(err, skills.ErrSkillNotFound):
			orihttp.NotFound(w, err.Error())
		case errors.Is(err, skills.ErrSkillRenameNotSupported):
			orihttp.BadRequest(w, err.Error())
		case errors.Is(err, skills.ErrSkillReadOnly):
			orihttp.RespondErrorWithErr(w, http.StatusForbidden, "skill source is read-only", err)
		default:
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "failed to update skill", err)
		}
		return
	}

	orihttp.Success(w, skill)
}

func (h *Handler) deleteSkill(w http.ResponseWriter, r *http.Request, name string) {
	agentName := resolveAgentName(r, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}

	if err := h.manager.DeleteSkill(agentName, name); err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) {
			orihttp.NotFound(w, err.Error())
			return
		}
		orihttp.InternalError(w, err.Error())
		return
	}

	orihttp.RespondNoContent(w)
}

func (h *Handler) setSkillEnabled(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req skillStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	if req.Enabled == nil {
		orihttp.BadRequest(w, "enabled flag required")
		return
	}
	agentName := resolveAgentNameWithFallback(req.Agent, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}
	if _, found, err := h.manager.GetSkill(agentName, name); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	} else if !found {
		orihttp.NotFound(w, "skill not found")
		return
	}
	if err := h.manager.SetSkillEnabled(agentName, name, *req.Enabled); err != nil {
		if errors.Is(err, skills.ErrSkillSlotCapReached) {
			// The cap message already names the stage and slot count; surface it
			// verbatim as a 409 so the UI can show it to the user.
			orihttp.Conflict(w, err.Error())
			return
		}
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.Success(w, map[string]any{
		"agent":   agentName,
		"name":    name,
		"enabled": *req.Enabled,
	})
}

func (h *Handler) setSkillTrusted(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req skillStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	if req.Trusted == nil {
		orihttp.BadRequest(w, "trusted flag required")
		return
	}
	agentName := resolveAgentNameWithFallback(req.Agent, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}
	if _, found, err := h.manager.GetSkill(agentName, name); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	} else if !found {
		orihttp.NotFound(w, "skill not found")
		return
	}
	if err := h.manager.SetSkillTrusted(agentName, name, *req.Trusted); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.Success(w, map[string]any{
		"agent":   agentName,
		"name":    name,
		"trusted": *req.Trusted,
	})
}

func (h *Handler) handleMarketplace(w http.ResponseWriter, r *http.Request, path string) {
	if path == "marketplace/search" {
		h.searchMarketplace(w, r)
		return
	}
	if path == "marketplace/install" {
		h.installMarketplaceSkill(w, r)
		return
	}
	if path == "marketplace/installed" {
		h.listMarketplaceInstalledSkills(w, r)
		return
	}
	if path == "marketplace/check" {
		h.checkMarketplaceUpdates(w, r)
		return
	}
	if path == "marketplace/update" {
		h.updateMarketplaceSkill(w, r)
		return
	}
	if path == "marketplace/remove" {
		h.removeMarketplaceSkill(w, r)
		return
	}
	orihttp.NotFound(w, "skills marketplace endpoint not found")
}

func (h *Handler) searchMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req marketplaceSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}

	query := sanitizeMarketplaceQuery(req.Query)
	if query == "" {
		orihttp.BadRequest(w, "query is required")
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > marketplaceMaxResults {
		limit = marketplaceMaxResults
	}

	ctx, cancel := context.WithTimeout(r.Context(), marketplaceSearchTimeout)
	defer cancel()

	output, err := h.runSkillsCLIInDir(ctx, "", "find", query)
	if errors.Is(err, exec.ErrNotFound) {
		respondNodeRequired(w)
		return
	}
	if err != nil {
		_ = orihttp.RespondJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "failed to search skills marketplace",
			"details": truncateMarketplaceOutput(output),
		})
		return
	}

	results := parseSkillsFindOutput(output, limit)
	orihttp.Success(w, map[string]any{
		"query":   query,
		"results": results,
		"count":   len(results),
	})
}

func (h *Handler) installMarketplaceSkill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req marketplaceInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}

	packageSpec := strings.TrimSpace(req.Package)
	if !isValidMarketplacePackageSpec(packageSpec) {
		orihttp.BadRequest(w, "invalid package format (expected owner/repo@skill)")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), marketplaceInstallTimeout)
	defer cancel()

	installed, output, err := h.installSkillPackage(ctx, packageSpec)
	if err != nil {
		respondMarketplaceError(w, "failed to install skill package", output, err)
		return
	}

	orihttp.Success(w, map[string]any{
		"package":  packageSpec,
		"status":   "installed",
		"skill":    installed.Name,
		"location": installed.Location,
		"path":     installed.Path,
		"details":  truncateMarketplaceOutput(output),
	})
}

// respondMarketplaceError maps a marketplace failure to its response: a name
// already in the Skills folder is a conflict, a missing Node.js says so, and
// a failed download carries the tool's output.
func respondMarketplaceError(w http.ResponseWriter, message, output string, err error) {
	var taken *skillNameTakenError
	var download *skillDownloadError
	switch {
	case errors.As(err, &taken):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": taken.Error()})
	case errors.Is(err, errNodeRequired):
		respondNodeRequired(w)
	case errors.As(err, &download):
		_ = orihttp.RespondJSON(w, http.StatusBadGateway, map[string]any{
			"error":   message,
			"details": truncateMarketplaceOutput(download.Output),
		})
	default:
		_ = orihttp.RespondJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   message + ": " + err.Error(),
			"details": truncateMarketplaceOutput(output),
		})
	}
}

func respondNodeRequired(w http.ResponseWriter) {
	_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": nodeRequiredMessage})
}

// listMarketplaceInstalledSkills lists the skills in the Skills folder that
// were installed from the marketplace, read from their source files.
func (h *Handler) listMarketplaceInstalledSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	installed, err := h.listMarketplaceSkills()
	if err != nil {
		_ = orihttp.RespondJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to list installed skills: " + err.Error(),
		})
		return
	}
	orihttp.Success(w, map[string]any{
		"skills": installed,
		"count":  len(installed),
	})
}

func (h *Handler) checkMarketplaceUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	statuses, err := h.checkSkillUpdates(r.Context())
	if err != nil {
		_ = orihttp.RespondJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to check skill updates: " + err.Error(),
		})
		return
	}
	available := 0
	for _, status := range statuses {
		if status.UpdateAvailable {
			available++
		}
	}
	summary := "All marketplace skills are up to date."
	if available > 0 {
		summary = fmt.Sprintf("%d update%s available.", available, map[bool]string{true: "", false: "s"}[available == 1])
	}
	orihttp.Success(w, map[string]any{
		"status":  "checked",
		"skills":  statuses,
		"summary": summary,
	})
}

type marketplaceUpdateRequest struct {
	Skill string `json:"skill"`
	Force bool   `json:"force,omitempty"`
}

// updateMarketplaceSkill replaces one marketplace skill with its latest
// version. A skill the user edited is refused with code locally_modified
// unless the request carries force: true.
func (h *Handler) updateMarketplaceSkill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req marketplaceUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	skillName := strings.TrimSpace(req.Skill)
	if !isPlainSkillFolderName(skillName) {
		orihttp.BadRequest(w, "invalid skill name")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), marketplaceUpdateTimeout)
	defer cancel()

	updated, output, err := h.updateSkillFolder(ctx, skillName, req.Force)
	switch {
	case errors.Is(err, errSkillLocallyModified):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"code":  "locally_modified",
			"error": "You changed this skill since it was installed. Updating will replace your changes.",
		})
		return
	case errors.Is(err, os.ErrNotExist):
		orihttp.NotFound(w, "no skill with that name is in your Skills folder")
		return
	case errors.Is(err, errNotAMarketplaceSkill):
		orihttp.BadRequest(w, err.Error())
		return
	case err != nil:
		respondMarketplaceError(w, "failed to update the skill", output, err)
		return
	}

	orihttp.Success(w, map[string]any{
		"skill":      skillName,
		"status":     "updated",
		"updated_at": updated.UpdatedAt,
		"summary":    "Updated " + skillName + ".",
	})
}

func (h *Handler) removeMarketplaceSkill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req marketplaceRemoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}

	skillName := normalizeMarketplaceSkillName(req.Skill)
	if !isPlainSkillFolderName(skillName) {
		orihttp.BadRequest(w, "invalid skill name")
		return
	}

	_, err := h.removeSkillFolder(skillName)
	if errors.Is(err, os.ErrNotExist) {
		orihttp.Success(w, map[string]any{"skill": skillName, "status": "not_found", "removed": false})
		return
	}
	if err != nil {
		_ = orihttp.RespondJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to remove installed skill: " + err.Error(),
		})
		return
	}

	orihttp.Success(w, map[string]any{
		"skill":   skillName,
		"status":  "removed",
		"removed": true,
		"trashed": true,
		"summary": "Moved to Trash",
	})
}

func sanitizeMarketplaceQuery(query string) string {
	normalized := strings.TrimSpace(query)
	if normalized == "" {
		return ""
	}
	normalized = strings.Join(strings.Fields(normalized), " ")
	if len(normalized) > 160 {
		normalized = normalized[:160]
	}
	return normalized
}

func stripANSI(input string) string {
	if input == "" {
		return ""
	}
	return ansiEscapePattern.ReplaceAllString(input, "")
}

func isValidMarketplacePackageSpec(value string) bool {
	trimmed := strings.TrimSpace(value)
	return marketplacePackagePattern.MatchString(trimmed) && marketplacePackagePattern.FindString(trimmed) == trimmed
}

func isValidMarketplaceSkillName(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && marketplaceSkillNamePattern.MatchString(trimmed)
}

func normalizeMarketplaceSkillName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if isValidMarketplacePackageSpec(trimmed) {
		_, skillName := parsePackageSpec(trimmed)
		return skillName
	}
	return trimmed
}

func parsePackageSpec(spec string) (repository, skillName string) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return "", ""
	}
	parts := strings.SplitN(trimmed, "@", 2)
	if len(parts) != 2 {
		return trimmed, ""
	}
	return parts[0], parts[1]
}

func parseSkillsFindOutput(output string, limit int) []marketplaceSkillResult {
	cleaned := stripANSI(output)
	if cleaned == "" {
		return []marketplaceSkillResult{}
	}

	if limit <= 0 || limit > marketplaceMaxResults {
		limit = 8
	}

	lines := strings.Split(cleaned, "\n")
	results := make([]marketplaceSkillResult, 0, limit)
	indexByPackage := make(map[string]int)
	currentIndex := -1

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		line = strings.TrimSpace(strings.TrimLeft(line, "│└├─•·"))
		if line == "" {
			continue
		}

		if packageMatch := marketplacePackagePattern.FindStringSubmatch(line); len(packageMatch) > 1 {
			packageSpec := strings.TrimSpace(packageMatch[1])
			resultIndex, exists := indexByPackage[packageSpec]
			if !exists {
				if len(results) >= limit {
					currentIndex = -1
					continue
				}
				repository, skillName := parsePackageSpec(packageSpec)
				results = append(results, marketplaceSkillResult{
					Package:    packageSpec,
					Repository: repository,
					Skill:      skillName,
				})
				resultIndex = len(results) - 1
				indexByPackage[packageSpec] = resultIndex
			}
			currentIndex = resultIndex
			if installMatch := marketplaceInstallsPattern.FindStringSubmatch(line); len(installMatch) > 1 {
				results[currentIndex].Installs = strings.TrimSpace(installMatch[1])
			}
			continue
		}

		if currentIndex < 0 || currentIndex >= len(results) {
			continue
		}

		if urlIndex := strings.Index(line, "https://skills.sh/"); urlIndex >= 0 {
			url := strings.TrimSpace(line[urlIndex:])
			if url != "" {
				results[currentIndex].URL = url
			}
			continue
		}

		if results[currentIndex].Installs == "" {
			if installMatch := marketplaceInstallsPattern.FindStringSubmatch(line); len(installMatch) > 1 {
				results[currentIndex].Installs = strings.TrimSpace(installMatch[1])
			}
		}
	}

	return results
}

func runSkillsCLIInDir(ctx context.Context, workingDir string, args ...string) (string, error) {
	commandArgs := append([]string{"--yes", "skills"}, args...)
	cmd := exec.CommandContext(ctx, "npx", commandArgs...)
	if dir := strings.TrimSpace(workingDir); dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "CI=1", "NO_COLOR=1", "FORCE_COLOR=0")

	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)
	if err != nil {
		if output == "" {
			output = err.Error()
		}
		return output, fmt.Errorf("skills command failed: %w", err)
	}
	return output, nil
}

func skillsInitAlreadyExists(output string) bool {
	cleaned := strings.ToLower(stripANSI(output))
	return strings.Contains(cleaned, "skill already exists")
}

func truncateMarketplaceOutput(output string) string {
	cleaned := strings.TrimSpace(stripANSI(output))
	if cleaned == "" {
		return ""
	}
	if len(cleaned) <= marketplaceOutputMaxChars {
		return cleaned
	}
	return cleaned[:marketplaceOutputMaxChars] + "..."
}

func resolveAgentName(r *http.Request, st store.Store) string {
	agentName := r.URL.Query().Get("agent")
	return resolveAgentNameWithFallback(agentName, st)
}

func resolveAgentNameWithFallback(agentName string, st store.Store) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" && st != nil {
		if agent, ok := st.GetAgent("Ori"); ok && agent != nil {
			return "Ori"
		}
		return store.FirstAgentName(st)
	}
	return agentName
}
