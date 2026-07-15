package agenthttp

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	homeAssistantWorkspaceStateNotNeeded   = "not_needed"
	homeAssistantWorkspaceStateConfident   = "confident"
	homeAssistantWorkspaceStateAmbiguous   = "ambiguous"
	homeAssistantWorkspaceStateNoFit       = "no_fit"
	homeAssistantWorkspaceStateNeedsRepair = "needs_repair"

	homeAssistantWorkspaceMinScore        = 4
	homeAssistantWorkspaceAmbiguousMargin = 2

	homeAssistantWorkspaceCorrectionHistoryLimit          = 50
	homeAssistantWorkspaceFuzzyCorrectionMinPromptTokens  = 3
	homeAssistantWorkspaceFuzzyCorrectionMinOverlap       = 3
	homeAssistantWorkspaceFuzzyCorrectionMinSimilarity    = 0.75
	homeAssistantWorkspaceFuzzyCorrectionStrongSimilarity = 0.85
	homeAssistantWorkspaceFuzzyCorrectionAmbiguousMargin  = 0.15
)

type HomeAssistantWorkspaceResolution struct {
	State                 string                            `json:"state"`
	SelectedWorkspaceID   string                            `json:"selected_workspace_id,omitempty"`
	SelectedWorkspaceName string                            `json:"selected_workspace_name,omitempty"`
	Confidence            float64                           `json:"confidence,omitempty"`
	Reasons               []string                          `json:"reasons,omitempty"`
	Candidates            []HomeAssistantWorkspaceCandidate `json:"candidates,omitempty"`
	RepairReason          string                            `json:"repair_reason,omitempty"`
}

type HomeAssistantWorkspaceCandidate struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons,omitempty"`
}

type homeAssistantWorkspaceRuntimeResolver interface {
	ResolveAgentForWorkspace(agentName, workspaceID, nodeID string) (*workspace.ResolvedAgentRuntime, error)
}

type homeAssistantWorkspaceFeedbackReader interface {
	PreferredWorkspaceForPrompt(ctx context.Context, prompt string) (string, bool, error)
	RecentWorkspaceCorrections(ctx context.Context, limit int) ([]HomeAssistantWorkspaceCorrection, error)
}

// homeAssistantHQProvider resolves the caller's current designated Personal
// HQ workspace, if any (PRD FR102/FR103). Kept as a narrow interface (rather
// than depending on internal/personalhq directly) so this package's tests
// can fake it without constructing a real personalhq.Service.
type homeAssistantHQProvider interface {
	CurrentHQWorkspaceID(ctx context.Context) (workspaceID string, ok bool)
}

type HomeAssistantWorkspaceResolver struct {
	WorkspaceStore  workspace.Store
	AgentStore      store.Store
	RuntimeResolver homeAssistantWorkspaceRuntimeResolver
	FeedbackReader  homeAssistantWorkspaceFeedbackReader
	HQProvider      homeAssistantHQProvider
}

func NewHomeAssistantWorkspaceResolver(workspaceStore workspace.Store, agentStore store.Store) *HomeAssistantWorkspaceResolver {
	return &HomeAssistantWorkspaceResolver{
		WorkspaceStore: workspaceStore,
		AgentStore:     agentStore,
	}
}

func (r *HomeAssistantWorkspaceResolver) SetRuntimeResolver(resolver homeAssistantWorkspaceRuntimeResolver) {
	if r == nil {
		return
	}
	r.RuntimeResolver = resolver
}

func (r *HomeAssistantWorkspaceResolver) SetFeedbackReader(reader homeAssistantWorkspaceFeedbackReader) {
	if r == nil {
		return
	}
	r.FeedbackReader = reader
}

func (r *HomeAssistantWorkspaceResolver) SetHQProvider(provider homeAssistantHQProvider) {
	if r == nil {
		return
	}
	r.HQProvider = provider
}

func (r *HomeAssistantWorkspaceResolver) Resolve(prompt string, routeContext normalizedHomeAssistantRouteContext) *HomeAssistantWorkspaceResolution {
	if r == nil || r.WorkspaceStore == nil {
		return &HomeAssistantWorkspaceResolution{State: homeAssistantWorkspaceStateNoFit}
	}

	if workspaceID := strings.TrimSpace(routeContext.WorkspaceID); workspaceID != "" && !promptRequestsWorkspaceSwitch(prompt) {
		ws, err := r.WorkspaceStore.Get(workspaceID)
		if err == nil && isHomeAssistantRoutableWorkspace(ws) {
			return r.resolveSelectedWorkspace(ws, []string{"using active workspace context"})
		}
	}
	if !promptRequestsWorkspaceSwitch(prompt) {
		if workspaceID, reason, ok := r.preferredWorkspaceForPrompt(prompt); ok {
			ws, err := r.WorkspaceStore.Get(workspaceID)
			if err == nil && isHomeAssistantRoutableWorkspace(ws) {
				return r.resolveSelectedWorkspace(ws, []string{reason})
			}
		}
	}

	active, err := r.WorkspaceStore.ListActive()
	if err != nil || len(active) == 0 {
		if resolution, ok := r.hqFallback(prompt, routeContext, nil); ok {
			return resolution
		}
		return &HomeAssistantWorkspaceResolution{State: homeAssistantWorkspaceStateNoFit}
	}

	candidates := make([]homeAssistantWorkspaceScore, 0, len(active))
	for _, ws := range active {
		if !isHomeAssistantRoutableWorkspace(ws) {
			continue
		}
		candidates = append(candidates, scoreHomeAssistantWorkspace(prompt, ws))
	}
	if len(candidates) == 0 {
		if resolution, ok := r.hqFallback(prompt, routeContext, nil); ok {
			return resolution
		}
		return &HomeAssistantWorkspaceResolution{State: homeAssistantWorkspaceStateNoFit}
	}

	sortHomeAssistantWorkspaceScores(candidates)

	visible := buildHomeAssistantWorkspaceCandidates(candidates)
	top := candidates[0]
	// A zero score means the prompt didn't reference any workspace's
	// identity at all — a genuinely untargeted, app-wide prompt (PRD
	// FR103). A nonzero-but-below-threshold score means the prompt weakly
	// referenced a specific project that just didn't clear the confidence
	// bar; defaulting that to the HQ would risk masking a real (if messy)
	// project reference (FR104/FR105), so only the zero-score case falls
	// back to the HQ.
	if top.Score < homeAssistantWorkspaceMinScore {
		if top.Score == 0 {
			if resolution, ok := r.hqFallback(prompt, routeContext, visible); ok {
				return resolution
			}
		}
		return &HomeAssistantWorkspaceResolution{
			State:      homeAssistantWorkspaceStateNoFit,
			Candidates: visible,
		}
	}

	if len(candidates) > 1 && candidates[1].Score >= homeAssistantWorkspaceMinScore &&
		top.Score-candidates[1].Score <= homeAssistantWorkspaceAmbiguousMargin {
		return &HomeAssistantWorkspaceResolution{
			State:      homeAssistantWorkspaceStateAmbiguous,
			Confidence: workspaceScoreConfidence(top.Score),
			Candidates: visible,
		}
	}

	resolution := r.resolveSelectedWorkspace(top.Workspace, top.Reasons)
	resolution.Confidence = workspaceScoreConfidence(top.Score)
	resolution.Candidates = visible
	return resolution
}

// hqFallback resolves to the caller's designated Personal HQ when nothing
// else fit, but only for a genuinely untargeted, app-wide prompt (PRD
// FR102/FR103): the request carried no originating workspace context and
// did not explicitly ask to switch elsewhere (FR104 — a request that names
// or originates in a project stays out of this path entirely, since it
// either resolves earlier or fails to match a real candidate rather than
// reaching here with an empty routeContext.WorkspaceID). Returns ok=false
// whenever the HQ itself isn't available/ready, so the caller's ordinary
// no_fit handling still applies (FR105 — the HQ is never a forced catch-all).
func (r *HomeAssistantWorkspaceResolver) hqFallback(prompt string, routeContext normalizedHomeAssistantRouteContext, visible []HomeAssistantWorkspaceCandidate) (*HomeAssistantWorkspaceResolution, bool) {
	if r == nil || r.HQProvider == nil {
		return nil, false
	}
	if strings.TrimSpace(routeContext.WorkspaceID) != "" || promptRequestsWorkspaceSwitch(prompt) {
		return nil, false
	}
	workspaceID, ok := r.HQProvider.CurrentHQWorkspaceID(context.Background())
	if !ok || strings.TrimSpace(workspaceID) == "" {
		return nil, false
	}
	ws, err := r.WorkspaceStore.Get(workspaceID)
	if err != nil || !isHomeAssistantRoutableWorkspace(ws) {
		return nil, false
	}
	resolution := r.resolveSelectedWorkspace(ws, []string{"defaulting to your Personal HQ for an app-wide prompt"})
	if resolution.State != homeAssistantWorkspaceStateConfident {
		return nil, false
	}
	resolution.Candidates = visible
	return resolution, true
}

type homeAssistantWorkspaceScore struct {
	Workspace *workspace.Workspace
	Score     int
	Reasons   []string
}

func scoreHomeAssistantWorkspace(prompt string, ws *workspace.Workspace) homeAssistantWorkspaceScore {
	normalizedPrompt := normalizeRouteToken(prompt)
	promptTokens := collectHomeAssistantWorkspacePromptTokens(prompt)
	score := homeAssistantWorkspaceScore{Workspace: ws}

	if name := normalizeRouteToken(ws.Name); name != "" && containsRoutePhrase(normalizedPrompt, name) {
		score.Score += 10
		score.Reasons = appendWorkspaceReason(score.Reasons, "matched workspace name")
	}

	addWorkspaceFieldScore(&score, normalizedPrompt, "workspace name", ws.Name, promptTokens, 4, 2)
	addWorkspaceFieldScore(&score, normalizedPrompt, "workspace slug", ws.FolderSlug, promptTokens, 3, 2)
	addWorkspaceFieldScore(&score, normalizedPrompt, "workspace project path", ws.ProjectPath, promptTokens, 3, 2)
	addWorkspaceFieldScore(&score, normalizedPrompt, "workspace description", ws.Description, promptTokens, 2, 2)
	addWorkspaceFieldScore(&score, normalizedPrompt, "workspace goal", workspaceSharedString(ws, "goal"), promptTokens, 3, 2)
	addWorkspaceFieldScore(&score, normalizedPrompt, "workspace systems", workspaceSharedString(ws, "systems"), promptTokens, 3, 3)
	addWorkspaceFieldScore(&score, normalizedPrompt, "workspace capabilities", workspaceSharedString(ws, "capabilities"), promptTokens, 2, 2)
	addWorkspaceFieldScore(&score, normalizedPrompt, "workspace context", workspaceSharedString(ws, "context"), promptTokens, 2, 2)
	addWorkspaceFieldScore(&score, normalizedPrompt, "workspace directories", workspaceDirectoryReferenceText(ws), promptTokens, 2, 2)
	return score
}

func (r *HomeAssistantWorkspaceResolver) resolveSelectedWorkspace(ws *workspace.Workspace, reasons []string) *HomeAssistantWorkspaceResolution {
	resolution := &HomeAssistantWorkspaceResolution{
		State:                 homeAssistantWorkspaceStateConfident,
		SelectedWorkspaceID:   strings.TrimSpace(ws.ID),
		SelectedWorkspaceName: strings.TrimSpace(ws.Name),
		Reasons:               append([]string(nil), reasons...),
	}
	if ok, reason := r.workspaceReadyForHandoff(ws); !ok {
		resolution.State = homeAssistantWorkspaceStateNeedsRepair
		resolution.RepairReason = reason
	}
	return resolution
}

func (r *HomeAssistantWorkspaceResolver) workspaceReadyForHandoff(ws *workspace.Workspace) (bool, string) {
	if ws == nil {
		return false, "workspace is unavailable"
	}
	entryAgentName := strings.TrimSpace(ws.EntryAgentName())
	if entryAgentName == "" {
		return false, "workspace has no entry agent"
	}
	if r.RuntimeResolver != nil {
		resolved, err := r.RuntimeResolver.ResolveAgentForWorkspace(entryAgentName, ws.ID, "")
		if err != nil || resolved == nil || resolved.Agent == nil {
			return false, "workspace entry agent is unavailable"
		}
		return true, ""
	}
	if r.AgentStore != nil {
		if _, ok := r.AgentStore.GetAgent(entryAgentName); !ok {
			return false, "workspace entry agent is unavailable"
		}
	}
	return true, ""
}

func (r *HomeAssistantWorkspaceResolver) preferredWorkspaceForPrompt(prompt string) (string, string, bool) {
	if r == nil || r.FeedbackReader == nil || strings.TrimSpace(prompt) == "" {
		return "", "", false
	}
	workspaceID, ok, err := r.FeedbackReader.PreferredWorkspaceForPrompt(context.Background(), prompt)
	if err == nil && ok {
		workspaceID = strings.TrimSpace(workspaceID)
		if workspaceID != "" {
			return workspaceID, "using prior workspace correction", true
		}
	}

	workspaceID, ok = r.fuzzyPreferredWorkspaceForPrompt(prompt)
	if !ok {
		return "", "", false
	}
	return workspaceID, "using similar prior workspace correction", true
}

type homeAssistantWorkspaceCorrectionMatch struct {
	WorkspaceID string
	Similarity  float64
	Count       int
}

func (r *HomeAssistantWorkspaceResolver) fuzzyPreferredWorkspaceForPrompt(prompt string) (string, bool) {
	promptTokens := collectHomeAssistantWorkspacePromptTokens(prompt)
	if len(promptTokens) < homeAssistantWorkspaceFuzzyCorrectionMinPromptTokens {
		return "", false
	}

	corrections, err := r.FeedbackReader.RecentWorkspaceCorrections(
		context.Background(),
		homeAssistantWorkspaceCorrectionHistoryLimit,
	)
	if err != nil || len(corrections) == 0 {
		return "", false
	}

	byWorkspace := make(map[string]homeAssistantWorkspaceCorrectionMatch)
	seen := make(map[string]struct{}, len(corrections))
	for _, correction := range corrections {
		workspaceID := strings.TrimSpace(correction.WorkspaceID)
		correctionTokens := collectHomeAssistantWorkspacePromptTokens(correction.Prompt)
		if workspaceID == "" || len(correctionTokens) < homeAssistantWorkspaceFuzzyCorrectionMinPromptTokens {
			continue
		}

		dedupeKey := strings.ToLower(strings.TrimSpace(correction.Prompt)) + "\x00" + workspaceID
		if _, ok := seen[dedupeKey]; ok {
			continue
		}
		seen[dedupeKey] = struct{}{}

		overlap := signalTokenOverlap(promptTokens, correctionTokens)
		if overlap < homeAssistantWorkspaceFuzzyCorrectionMinOverlap {
			continue
		}
		similarity := signalTokenJaccard(promptTokens, correctionTokens, overlap)
		if similarity < homeAssistantWorkspaceFuzzyCorrectionMinSimilarity {
			continue
		}

		match := byWorkspace[workspaceID]
		match.WorkspaceID = workspaceID
		match.Count++
		if similarity > match.Similarity {
			match.Similarity = similarity
		}
		byWorkspace[workspaceID] = match
	}
	if len(byWorkspace) == 0 {
		return "", false
	}

	matches := make([]homeAssistantWorkspaceCorrectionMatch, 0, len(byWorkspace))
	for _, match := range byWorkspace {
		matches = append(matches, match)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Similarity != matches[j].Similarity {
			return matches[i].Similarity > matches[j].Similarity
		}
		if matches[i].Count != matches[j].Count {
			return matches[i].Count > matches[j].Count
		}
		return matches[i].WorkspaceID < matches[j].WorkspaceID
	})

	top := matches[0]
	if top.Similarity < homeAssistantWorkspaceFuzzyCorrectionStrongSimilarity && top.Count < 2 {
		return "", false
	}
	if len(matches) > 1 && top.Similarity-matches[1].Similarity < homeAssistantWorkspaceFuzzyCorrectionAmbiguousMargin {
		return "", false
	}
	return top.WorkspaceID, true
}

func isHomeAssistantRoutableWorkspace(ws *workspace.Workspace) bool {
	if ws == nil || ws.GetStatus() != workspace.StatusActive {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(ws.Kind), "group")
}

func promptRequestsWorkspaceSwitch(prompt string) bool {
	normalized := normalizeRouteToken(prompt)
	if normalized == "" {
		return false
	}
	return promptContainsAnyRoutePhrase(normalized, []string{
		"switch workspace",
		"switch to workspace",
		"use workspace",
		"open workspace",
	})
}

func collectHomeAssistantWorkspacePromptTokens(prompt string) []string {
	raw := tokenizePrompt(prompt)
	tokens := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, token := range raw {
		if !isSignalPromptToken(token) || seen[token] {
			continue
		}
		seen[token] = true
		tokens = append(tokens, token)
	}
	return tokens
}

func signalTokenJaccard(left, right []string, overlap int) float64 {
	if overlap <= 0 {
		return 0
	}
	union := len(left) + len(right) - overlap
	if union <= 0 {
		return 0
	}
	return float64(overlap) / float64(union)
}

func addWorkspaceFieldScore(score *homeAssistantWorkspaceScore, normalizedPrompt, label, field string, promptTokens []string, weight, phraseBonus int) {
	if score == nil || weight <= 0 {
		return
	}
	fieldTokens := collectHomeAssistantWorkspacePromptTokens(field)
	if len(fieldTokens) == 0 {
		return
	}
	matches := signalTokenOverlap(promptTokens, fieldTokens)
	if matches == 0 {
		return
	}
	score.Score += matches * weight
	score.Reasons = appendWorkspaceReason(score.Reasons, "matched "+label)
	if phraseBonus > 0 && workspaceFieldHasPromptPhrase(normalizedPrompt, field) {
		score.Score += phraseBonus
		score.Reasons = appendWorkspaceReason(score.Reasons, "matched "+label+" phrase")
	}
}

func workspaceSharedString(ws *workspace.Workspace, key string) string {
	if ws == nil || ws.SharedData == nil {
		return ""
	}
	rawBootstrap, ok := ws.SharedData["workspace_bootstrap"]
	if !ok {
		return ""
	}
	bootstrap, ok := rawBootstrap.(map[string]any)
	if !ok {
		return ""
	}
	raw, ok := bootstrap[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(toWorkspaceResolverString(raw))
}

func toWorkspaceResolverString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []string:
		return strings.Join(typed, " ")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := toWorkspaceResolverString(item); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func workspaceDirectoryReferenceText(ws *workspace.Workspace) string {
	if ws == nil || len(ws.DirectoryReferences) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ws.DirectoryReferences)*2)
	for _, ref := range ws.DirectoryReferences {
		if name := strings.TrimSpace(ref.Name); name != "" {
			parts = append(parts, name)
		}
		if path := strings.TrimSpace(ref.Path); path != "" {
			parts = append(parts, path, filepath.Base(path))
		}
	}
	return strings.Join(parts, " ")
}

func workspaceFieldHasPromptPhrase(normalizedPrompt, field string) bool {
	if normalizedPrompt == "" || strings.TrimSpace(field) == "" {
		return false
	}
	for _, phrase := range workspaceFieldPhrases(field) {
		if len(collectHomeAssistantWorkspacePromptTokens(phrase)) < 2 {
			continue
		}
		if containsRoutePhrase(normalizedPrompt, phrase) {
			return true
		}
	}
	return false
}

func workspaceFieldPhrases(field string) []string {
	raw := strings.FieldsFunc(field, func(r rune) bool {
		switch r {
		case '\n', '\r', ',', ';', '|':
			return true
		default:
			return false
		}
	})
	phrases := make([]string, 0, len(raw)+1)
	seen := make(map[string]struct{}, len(raw)+1)
	add := func(value string) {
		value = strings.TrimSpace(strings.Trim(value, "-*"))
		if value == "" {
			return
		}
		normalized := normalizeRouteToken(value)
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		phrases = append(phrases, value)
	}
	add(field)
	for _, phrase := range raw {
		add(phrase)
	}
	return phrases
}

func appendWorkspaceReason(reasons []string, reason string) []string {
	if reason == "" {
		return reasons
	}
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	if len(reasons) >= 6 {
		return reasons
	}
	return append(reasons, reason)
}

func sortHomeAssistantWorkspaceScores(scores []homeAssistantWorkspaceScore) {
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Score != scores[j].Score {
			return scores[i].Score > scores[j].Score
		}
		leftName := ""
		rightName := ""
		if scores[i].Workspace != nil {
			leftName = strings.ToLower(scores[i].Workspace.Name)
		}
		if scores[j].Workspace != nil {
			rightName = strings.ToLower(scores[j].Workspace.Name)
		}
		return leftName < rightName
	})
}

func buildHomeAssistantWorkspaceCandidates(scores []homeAssistantWorkspaceScore) []HomeAssistantWorkspaceCandidate {
	out := make([]HomeAssistantWorkspaceCandidate, 0, len(scores))
	for _, score := range scores {
		if score.Workspace == nil {
			continue
		}
		out = append(out, HomeAssistantWorkspaceCandidate{
			ID:      score.Workspace.ID,
			Name:    score.Workspace.Name,
			Score:   score.Score,
			Reasons: append([]string(nil), score.Reasons...),
		})
	}
	return out
}

func workspaceScoreConfidence(score int) float64 {
	if score <= 0 {
		return 0
	}
	confidence := 0.35 + float64(score)*0.06
	if confidence > 0.99 {
		return 0.99
	}
	return confidence
}
