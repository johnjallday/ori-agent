package chathttp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openai/openai-go/v3"
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

var travelPlanningDatePattern = regexp.MustCompile(`(?i)\b(?:\d{1,2}/\d{1,2}(?:/\d{2,4})?|(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?\s+\d{1,2})\b`)

func maybeBuildWorkspacePlanningFormResponse(ag *resolvedChatAgent, query string, routeCtx normalizedChatRouteContext) *planningFormResponse {
	if !isWorkspaceManagerAgent(ag) || strings.TrimSpace(routeCtx.WorkspaceID) == "" {
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

	form := buildWorkspaceTravelPlanningForm(trimmedQuery)
	if form == nil {
		return nil
	}

	return &planningFormResponse{
		ResponseText: "I can collect the key trip details first. Complete the planning step below, then I'll recommend the right specialist or keep it with the workspace manager only if the follow-up stays lightweight.",
		Form:         form,
	}
}

func isWorkspaceManagerAgent(ag *resolvedChatAgent) bool {
	if ag == nil || ag.Agent == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(ag.Type), "workspace-manager")
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

func buildWorkspaceTravelPlanningForm(query string) *PlanningForm {
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil
	}

	requiresDates := countTravelPlanningDateMentions(trimmedQuery) < 2
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
		dateQuestion.Label = "Date or route changes (optional)"
		dateQuestion.HelpText = "Only fill this in if the dates or city order need to be corrected or completed."
		dateQuestion.Placeholder = "Optional corrections or missing travel legs"
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
		summary += "\n\nDates and route were detected. Add corrections only if anything is missing or wrong."
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
