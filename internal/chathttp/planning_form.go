package chathttp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type PlanningForm struct {
	ID                 string                 `json:"id"`
	Kind               string                 `json:"kind,omitempty"`
	Title              string                 `json:"title"`
	Subtitle           string                 `json:"subtitle,omitempty"`
	Summary            string                 `json:"summary,omitempty"`
	SubmitLabel        string                 `json:"submit_label,omitempty"`
	SubmitInstructions string                 `json:"submit_instructions,omitempty"`
	Questions          []PlanningFormQuestion `json:"questions"`
}

type PlanningFormQuestion struct {
	ID          string                    `json:"id"`
	Type        string                    `json:"type"`
	Label       string                    `json:"label"`
	HelpText    string                    `json:"help_text,omitempty"`
	Placeholder string                    `json:"placeholder,omitempty"`
	Required    bool                      `json:"required,omitempty"`
	Rows        int                       `json:"rows,omitempty"`
	Options     []PlanningFormOption      `json:"options,omitempty"`
	VisibleWhen *PlanningFormVisibility   `json:"visible_when,omitempty"`
	FileConfig  *PlanningFormFileQuestion `json:"file_config,omitempty"`
}

type PlanningFormOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type PlanningFormVisibility struct {
	QuestionID string   `json:"question_id"`
	AnyOf      []string `json:"any_of,omitempty"`
	NotEmpty   bool     `json:"not_empty,omitempty"`
}

type PlanningFormFileQuestion struct {
	AttachmentKind string `json:"attachment_kind,omitempty"`
	ButtonLabel    string `json:"button_label,omitempty"`
	OpenedStatus   string `json:"opened_status,omitempty"`
}

type planningFormResponse struct {
	ResponseText string
	Form         *PlanningForm
}

type planningFormWorkspaceStore interface {
	Get(id string) (*workspace.Workspace, error)
}

type planningFormSessionStore interface {
	ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]session.WorkspaceNoteListItem, error)
	GetNote(ctx context.Context, id string) (*session.WorkspaceNote, error)
}

type travelPlanningDetectionContext struct {
	Text           string
	Summary        string
	SourceLabel    string
	UsesKnownDates bool
}

var travelPlanningDatePattern = regexp.MustCompile(`(?i)\b(?:\d{1,2}/\d{1,2}(?:/\d{2,4})?|(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?\s+\d{1,2})\b`)
var travelPlanningContextPrefixPattern = regexp.MustCompile(`^\s*(?:#{1,6}\s+|[-*]\s+|\d+\.\s+)`)

func maybeBuildWorkspacePlanningFormResponse(ag *resolvedChatAgent, query string, routeCtx normalizedChatRouteContext, workspaceStore planningFormWorkspaceStore, sessionStore planningFormSessionStore) *planningFormResponse {
	if !isWorkspaceAgent(ag) || strings.TrimSpace(routeCtx.WorkspaceID) == "" {
		return nil
	}

	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" || isPlanningFormSubmissionPrompt(trimmedQuery) {
		return nil
	}
	if planningFormAlreadyCompleted(ag.Messages, trimmedQuery) {
		return nil
	}

	if !looksLikeTravelPlanningRequest(trimmedQuery) {
		return nil
	}

	detectionContext := loadTravelPlanningDetectionContext(routeCtx, workspaceStore, sessionStore)
	form := buildWorkspaceTravelPlanningForm(trimmedQuery, detectionContext)
	if form == nil {
		return nil
	}

	return &planningFormResponse{
		ResponseText: "I can collect the key trip details first. Complete the planning step below, then I'll recommend the right specialist or keep it with the workspace manager only if the follow-up stays lightweight.",
		Form:         form,
	}
}

// isWorkspaceAgent checks if the resolved agent is operating in a workspace context.
// This replaces the old type-based check for "workspace-manager".
func isWorkspaceAgent(ag *resolvedChatAgent) bool {
	if ag == nil || ag.Agent == nil {
		return false
	}
	return ag.WorkspaceTools != nil
}

func isPlanningFormSubmissionPrompt(query string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(query))
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(trimmed, "structured planning form submission:") ||
		strings.HasPrefix(trimmed, "structured travel intake for the workspace manager:")
}

func planningFormAlreadyCompleted(messages []openai.ChatCompletionMessageParamUnion, currentQuery string) bool {
	if looksLikeFreshTravelPlanningRequest(currentQuery) {
		return false
	}

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.OfUser == nil {
			continue
		}
		if isPlanningFormSubmissionPrompt(userMessageText(msg.OfUser)) {
			return true
		}
	}
	return false
}

func looksLikeFreshTravelPlanningRequest(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return false
	}

	return containsAnyPhrase(lower, []string{
		"let's plan",
		"plan a trip",
		"plan my trip",
		"help me plan",
		"travel itinerary",
		"trip itinerary",
		"travel planning",
		"trip planning",
		"replan",
		"start over",
		"new trip",
	})
}

func looksLikeTravelPlanningRequest(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return false
	}

	keywords := []string{
		"plan a trip",
		"plan my trip",
		"travel itinerary",
		"trip itinerary",
		"itinerary",
		"travel plan",
		"travel planning",
		"trip planning",
		"booked flight",
		"booked hotel",
		"flight",
		"hotel",
		"vacation",
		"leave spain",
		"arrival",
		"depart",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}

	return false
}

func countTravelPlanningDateMentions(query string) int {
	return len(travelPlanningDatePattern.FindAllString(query, -1))
}

func buildWorkspaceTravelPlanningForm(query string, detectionContext travelPlanningDetectionContext) *PlanningForm {
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil
	}

	knownDateText := strings.TrimSpace(detectionContext.Text)
	knownDateMentions := countTravelPlanningDateMentions(knownDateText)
	queryDateMentions := countTravelPlanningDateMentions(trimmedQuery)
	requiresDates := countTravelPlanningDateMentions(strings.TrimSpace(trimmedQuery+"\n"+knownDateText)) < 2
	usingWorkspaceKnownDates := queryDateMentions < 2 && knownDateMentions >= 2 && detectionContext.UsesKnownDates
	dateQuestion := PlanningFormQuestion{
		ID:          "date_details",
		Type:        "textarea",
		Label:       "Travel dates and route",
		HelpText:    "Example: 5/11 Lisbon arrival, 5/14 San Sebastian arrival, 5/17 Madrid arrival, 5/23 leave Spain.",
		Placeholder: "List your arrival, transfer, and departure dates.",
		Required:    requiresDates,
		Rows:        3,
	}
	if !requiresDates {
		dateQuestion.Type = "text"
		if usingWorkspaceKnownDates {
			dateQuestion.Label = "Confirm travel dates and route"
			dateQuestion.HelpText = fmt.Sprintf("Known dates were detected from %s. Leave this blank if they are correct, or add corrections.", travelPlanningSourceLabel(detectionContext.SourceLabel))
			dateQuestion.Placeholder = "Optional corrections to the known dates or city order"
		} else {
			dateQuestion.Label = "Date or route changes (optional)"
			dateQuestion.HelpText = "Only fill this in if the dates or city order need to be corrected or completed."
			dateQuestion.Placeholder = "Optional corrections or missing travel legs"
		}
	}

	selectOptions := []PlanningFormOption{
		{Value: "", Label: "Choose one"},
		{Value: "yes", Label: "Yes"},
		{Value: "partially", Label: "Partially"},
		{Value: "no", Label: "No"},
		{Value: "not_sure", Label: "Not sure yet"},
	}

	questions := []PlanningFormQuestion{
		dateQuestion,
		{
			ID:       "flights_booked",
			Type:     "select",
			Label:    "Are flights already booked?",
			HelpText: "If yes or partial, attach the confirmation in this workspace.",
			Required: true,
			Options:  selectOptions,
		},
		{
			ID:       "flight_confirmation",
			Type:     "file",
			Label:    "Attach flight confirmation",
			HelpText: "If flights are already booked, attach the confirmation or travel file to this workspace.",
			VisibleWhen: &PlanningFormVisibility{
				QuestionID: "flights_booked",
				AnyOf:      []string{"yes", "partially"},
			},
			FileConfig: &PlanningFormFileQuestion{
				AttachmentKind: "flight",
				ButtonLabel:    "Attach Flight File",
				OpenedStatus:   "Upload modal opened for flight confirmation.",
			},
		},
		{
			ID:       "hotels_booked",
			Type:     "select",
			Label:    "Are hotels already booked?",
			HelpText: "If yes or partial, attach the confirmation in this workspace.",
			Required: true,
			Options:  selectOptions,
		},
		{
			ID:       "hotel_confirmation",
			Type:     "file",
			Label:    "Attach hotel confirmation",
			HelpText: "If hotels are already booked, attach the confirmation or travel file to this workspace.",
			VisibleWhen: &PlanningFormVisibility{
				QuestionID: "hotels_booked",
				AnyOf:      []string{"yes", "partially"},
			},
			FileConfig: &PlanningFormFileQuestion{
				AttachmentKind: "hotel",
				ButtonLabel:    "Attach Hotel File",
				OpenedStatus:   "Upload modal opened for hotel confirmation.",
			},
		},
		{
			ID:       "pace",
			Type:     "select",
			Label:    "What pace do you want?",
			HelpText: "Use this to guide how packed or relaxed the trip should feel.",
			Options: []PlanningFormOption{
				{Value: "", Label: "Choose one"},
				{Value: "relaxed", Label: "Relaxed"},
				{Value: "balanced", Label: "Balanced"},
				{Value: "packed", Label: "Packed"},
			},
		},
		{
			ID:       "budget",
			Type:     "select",
			Label:    "What budget level fits best?",
			HelpText: "This helps decide neighborhoods, transport, and hotel recommendations later.",
			Options: []PlanningFormOption{
				{Value: "", Label: "Choose one"},
				{Value: "budget", Label: "Budget"},
				{Value: "mid_range", Label: "Mid-range"},
				{Value: "premium", Label: "Premium"},
			},
		},
		{
			ID:          "preferences",
			Type:        "textarea",
			Label:       "What do you care about most in Spain?",
			HelpText:    "Share must-dos, neighborhoods, food, museums, nightlife, beaches, pace, accessibility, or any other constraints.",
			Placeholder: "Examples: pintxos in San Sebastian, art museums in Madrid, walkable areas, avoid late-night nightlife.",
			Rows:        3,
		},
	}

	summary := fmt.Sprintf("Original request:\n%s", trimmedQuery)
	if requiresDates {
		summary += "\n\nTravel dates were not clearly detected. Add the missing dates or route details below."
	} else {
		if usingWorkspaceKnownDates {
			summary += fmt.Sprintf("\n\nKnown dates and route were detected from %s.", travelPlanningSourceLabel(detectionContext.SourceLabel))
			if details := strings.TrimSpace(detectionContext.Summary); details != "" {
				summary += "\n" + details
			}
			summary += "\n\nConfirm below only if anything is missing or wrong."
		} else {
			summary += "\n\nDates and route were detected. Add corrections only if anything is missing or wrong."
		}
	}

	return &PlanningForm{
		ID:          "travel_intake",
		Kind:        "travel_intake",
		Title:       "Collect trip details before specialist handoff",
		Subtitle:    "The workspace manager will review these answers, recommend the right travel specialist, and only keep the work at the manager level for lightweight follow-ups.",
		Summary:     summary,
		SubmitLabel: "Review Intake And Choose Next Agent",
		SubmitInstructions: strings.Join([]string{
			"Treat this as planning intake context, not a request for a full itinerary.",
			"Ask only the remaining missing questions.",
			"If the intake is sufficient, recommend the right specialist handoff first.",
			"For itinerary, day-by-day, or multi-city planning, default to asking permission to invite or create the travel itinerary specialist.",
			"For hotel-only or flight-only gaps, default to asking permission to invite or create the matching specialist.",
			"Only continue as the workspace manager when the next step is a lightweight clarification or the user explicitly says to keep it with the manager.",
		}, " "),
		Questions: questions,
	}
}

func loadTravelPlanningDetectionContext(
	routeCtx normalizedChatRouteContext,
	workspaceStore planningFormWorkspaceStore,
	sessionStore planningFormSessionStore,
) travelPlanningDetectionContext {
	workspaceID := strings.TrimSpace(routeCtx.WorkspaceID)
	if workspaceID == "" || workspaceStore == nil {
		return travelPlanningDetectionContext{}
	}

	ws, err := workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		return travelPlanningDetectionContext{}
	}

	var texts []string
	var summaries []string
	sourceLabel := "workspace context"

	if bootstrapText := workspaceBootstrapPlanningText(ws); bootstrapText != "" {
		texts = append(texts, bootstrapText)
		if summary := extractTravelDateContextSummary(bootstrapText); summary != "" {
			summaries = append(summaries, summary)
		}
	}

	if noteText, noteSource := workspaceTravelPlanningNoteText(context.Background(), sessionStore, workspaceID); noteText != "" {
		texts = append(texts, noteText)
		if summary := extractTravelDateContextSummary(noteText); summary != "" {
			summaries = append(summaries, summary)
		}
		sourceLabel = noteSource
	}

	combinedText := strings.TrimSpace(strings.Join(uniquePlanningContextParts(texts), "\n"))
	return travelPlanningDetectionContext{
		Text:           combinedText,
		Summary:        strings.TrimSpace(strings.Join(uniquePlanningContextParts(summaries), "\n")),
		SourceLabel:    sourceLabel,
		UsesKnownDates: countTravelPlanningDateMentions(combinedText) >= 2,
	}
}

func workspaceBootstrapPlanningText(ws *workspace.Workspace) string {
	if ws == nil || len(ws.SharedData) == 0 {
		return ""
	}

	raw, ok := ws.SharedData["workspace_bootstrap"]
	if !ok || raw == nil {
		return ""
	}

	bootstrap, ok := raw.(map[string]interface{})
	if !ok {
		return strings.TrimSpace(fmt.Sprint(raw))
	}

	parts := []string{
		workspaceBootstrapFieldText(bootstrap, "goal"),
		workspaceBootstrapFieldText(bootstrap, "context"),
		workspaceBootstrapFieldText(bootstrap, "capabilities"),
	}
	return strings.TrimSpace(strings.Join(uniquePlanningContextParts(parts), "\n"))
}

func workspaceBootstrapFieldText(bootstrap map[string]interface{}, key string) string {
	if len(bootstrap) == 0 {
		return ""
	}
	value, ok := bootstrap[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func workspaceTravelPlanningNoteText(ctx context.Context, sessionStore planningFormSessionStore, workspaceID string) (string, string) {
	if sessionStore == nil || strings.TrimSpace(workspaceID) == "" {
		return "", ""
	}

	notes, err := sessionStore.ListNotesByWorkspace(ctx, workspaceID)
	if err != nil {
		return "", ""
	}

	bestName := ""
	bestID := ""
	bestScore := 0
	for _, item := range notes {
		score := travelPlanningNoteScore(item.Name)
		if score <= 0 {
			continue
		}
		if score > bestScore {
			bestScore = score
			bestName = strings.TrimSpace(item.Name)
			bestID = item.ID
		}
	}

	if bestID == "" {
		return "", ""
	}

	note, err := sessionStore.GetNote(ctx, bestID)
	if err != nil || note == nil {
		return "", ""
	}
	content := strings.TrimSpace(note.Content)
	if content == "" {
		return "", ""
	}
	return content, fmt.Sprintf("workspace note %q", bestName)
}

func travelPlanningNoteScore(name string) int {
	normalized := normalizePlanningNoteToken(name)
	if normalized == "" {
		return 0
	}
	switch {
	case normalized == "workspacebrief":
		return 400
	case strings.Contains(normalized, "tripintake"):
		return 350
	case strings.Contains(normalized, "travelintake"):
		return 350
	case strings.Contains(normalized, "tripbrief"):
		return 300
	case strings.Contains(normalized, "travelbrief"):
		return 300
	case strings.Contains(normalized, "itinerarybrief"):
		return 200
	default:
		return 0
	}
}

func travelPlanningSourceLabel(sourceLabel string) string {
	if strings.TrimSpace(sourceLabel) == "" {
		return "workspace context"
	}
	return strings.TrimSpace(sourceLabel)
}

func normalizePlanningNoteToken(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			builder.WriteRune(unicode.ToLower(r))
		}
	}
	return builder.String()
}

func extractTravelDateContextSummary(text string) string {
	normalizedText := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\r", "\n")
	if normalizedText == "" {
		return ""
	}

	lines := strings.Split(normalizedText, "\n")
	candidates := make([]string, 0, 3)
	for _, rawLine := range lines {
		line := normalizeTravelContextLine(rawLine)
		if line == "" || countTravelPlanningDateMentions(line) == 0 {
			continue
		}
		candidates = append(candidates, line)
		if len(candidates) >= 3 {
			break
		}
	}
	if len(candidates) > 0 {
		return strings.Join(uniquePlanningContextParts(candidates), "\n")
	}

	sentences := regexp.MustCompile(`[\n.!?]+`).Split(normalizedText, -1)
	for _, rawSentence := range sentences {
		sentence := normalizeTravelContextLine(rawSentence)
		if sentence == "" || countTravelPlanningDateMentions(sentence) == 0 {
			continue
		}
		candidates = append(candidates, sentence)
		if len(candidates) >= 2 {
			break
		}
	}

	return strings.Join(uniquePlanningContextParts(candidates), "\n")
}

func normalizeTravelContextLine(value string) string {
	line := strings.TrimSpace(value)
	line = travelPlanningContextPrefixPattern.ReplaceAllString(line, "")
	line = strings.Join(strings.Fields(line), " ")
	return strings.TrimSpace(line)
}

func uniquePlanningContextParts(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
