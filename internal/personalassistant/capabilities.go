package personalassistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type CapabilityStatus string

const (
	CapabilityAvailable     CapabilityStatus = "available"
	CapabilityHealthyEmpty  CapabilityStatus = "healthy_empty"
	CapabilityNotConfigured CapabilityStatus = "not_configured"
	CapabilityUnavailable   CapabilityStatus = "unavailable"
	CapabilityRevoked       CapabilityStatus = "revoked"
)

type CapabilityCard struct {
	Key                  string           `json:"key"`
	Label                string           `json:"label"`
	Status               CapabilityStatus `json:"status"`
	Reason               string           `json:"reason,omitempty"`
	CanRead              string           `json:"can_read"`
	CanPropose           string           `json:"can_propose"`
	RequiresConfirmation string           `json:"requires_confirmation"`
	MappedWrite          bool             `json:"mapped_write"`
	ActionLabel          string           `json:"action_label"`
	ActionRoute          string           `json:"action_route"`
}

type CapabilityProjection struct {
	State string           `json:"state"`
	Cards []CapabilityCard `json:"cards"`
	// Suggestion is the domain workspace this user was told about during the
	// hire but has not created yet. Hiring deliberately creates no workspace
	// and runs no setup wizard, so this stays a suggestion the user acts on.
	// It is nil for a generic relationship, and disappears once the workspace
	// exists.
	Suggestion *CapabilitySuggestion `json:"suggestion,omitempty"`
}

// CapabilitySuggestion is one bounded post-hire recommendation. Its copy comes
// from the specialist mapping; its route is validated like any other.
type CapabilitySuggestion struct {
	TemplateID  string `json:"template_id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	ActionLabel string `json:"action_label"`
	ActionRoute string `json:"action_route"`
}

type EmailCapabilityStatus struct {
	Status CapabilityStatus
	Reason string
	Route  string
}

type EmailCapabilityReader interface {
	EmailCapability(ctx context.Context, workspaceID string) EmailCapabilityStatus
}

type CapabilityService struct {
	relationships *Service
	workspaces    workspace.Store
	email         EmailCapabilityReader
}

func NewCapabilityService(relationships *Service, workspaces workspace.Store, email EmailCapabilityReader) *CapabilityService {
	return &CapabilityService{relationships: relationships, workspaces: workspaces, email: email}
}

func (s *CapabilityService) Get(ctx context.Context, userID string) (*CapabilityProjection, error) {
	if s == nil || s.relationships == nil {
		return nil, fmt.Errorf("personal assistant: capability service is unavailable")
	}
	relationship, err := s.relationships.Get(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	if relationship.State != APIStateActive && relationship.State != APIStatePaused {
		return &CapabilityProjection{State: string(relationship.State), Cards: []CapabilityCard{}}, nil
	}
	// An unrecognised persisted slug reads as no specialist, which is the
	// current fixed order and no suggestion.
	entry, hasSpecialist := specialist.Get(relationship.SpecialistSlug)
	cards := []CapabilityCard{emailCapabilityCard(ctx, s.email, relationship.HQWorkspaceID)}
	workspaceCards, domainWorkspaceExists := s.workspaceCapabilityCards(userID, relationship.HQWorkspaceID, entry, hasSpecialist)
	cards = append(cards, workspaceCards...)
	projection := &CapabilityProjection{State: string(relationship.State), Cards: cards}
	if !hasSpecialist {
		return projection, nil
	}
	projection.Cards = orderCapabilityCards(cards, entry.CapabilityOrder)
	if !domainWorkspaceExists && safeCapabilityRoute(entry.Suggestion.ActionRoute) {
		projection.Suggestion = &CapabilitySuggestion{
			TemplateID:  entry.SuggestedTemplateID,
			Title:       entry.Suggestion.Title,
			Body:        entry.Suggestion.Body,
			ActionLabel: entry.Suggestion.ActionLabel,
			ActionRoute: entry.Suggestion.ActionRoute,
		}
	}
	return projection, nil
}

// orderCapabilityCards puts the domain's preferred keys first, in the order it
// asked for. Keys the domain does not mention keep their existing relative
// order behind them, so a new card added to the projection later still appears
// on the specialist path without the mapping having to be updated.
func orderCapabilityCards(cards []CapabilityCard, order []string) []CapabilityCard {
	if len(order) == 0 {
		return cards
	}
	byKey := make(map[string]CapabilityCard, len(cards))
	for _, card := range cards {
		byKey[card.Key] = card
	}
	out := make([]CapabilityCard, 0, len(cards))
	placed := make(map[string]struct{}, len(cards))
	for _, key := range order {
		card, ok := byKey[key]
		if !ok {
			continue
		}
		if _, duplicate := placed[key]; duplicate {
			continue
		}
		placed[key] = struct{}{}
		out = append(out, card)
	}
	for _, card := range cards {
		if _, done := placed[card.Key]; done {
			continue
		}
		out = append(out, card)
	}
	return out
}

func emailCapabilityCard(ctx context.Context, reader EmailCapabilityReader, hqID string) CapabilityCard {
	card := CapabilityCard{
		Key: "email", Label: "Email", Status: CapabilityUnavailable, Reason: "source_unavailable",
		CanRead:              "Attention signals and message context from the account linked to Personal HQ.",
		CanPropose:           "Follow-ups and draft replies.",
		RequiresConfirmation: "No external email write is mapped in Personal Assistant v1.",
		MappedWrite:          false, ActionLabel: "Set up email", ActionRoute: "/settings#google-account",
	}
	if reader == nil {
		return card
	}
	status := reader.EmailCapability(ctx, hqID)
	card.Status, card.Reason = status.Status, status.Reason
	if safeCapabilityRoute(status.Route) {
		card.ActionRoute = status.Route
	}
	switch status.Status {
	case CapabilityAvailable:
		card.ActionLabel = "Review email connection"
	case CapabilityRevoked:
		card.ActionLabel = "Repair email connection"
	}
	return card
}

// workspaceCapabilityCards returns the three workspace-backed cards and, when
// a specialist is set, whether a workspace built from its suggested blueprint
// already exists — a suggestion to create something the user already created
// is noise.
func (s *CapabilityService) workspaceCapabilityCards(userID, hqID string, entry specialist.Entry, hasSpecialist bool) ([]CapabilityCard, bool) {
	calendar := CapabilityCard{
		Key: "calendar", Label: "Calendar", Status: CapabilityNotConfigured,
		CanRead:              "Events exposed by an existing Calendar Ops connection.",
		CanPropose:           "Meeting preparation and internal follow-up records.",
		RequiresConfirmation: "No external calendar write is mapped in Personal Assistant v1.",
		MappedWrite:          false, ActionLabel: "Set up Calendar Ops", ActionRoute: "/construct?template=calendar-ops",
	}
	projects := CapabilityCard{
		Key: "projects", Label: "Projects and workspaces", Status: CapabilityHealthyEmpty,
		CanRead:              "Bounded task, result, and activity summaries from your existing workspaces.",
		CanPropose:           "Internal tasks and follow-ups in a workspace you choose.",
		RequiresConfirmation: "Creating or starting work uses the existing confirmation gate.",
		MappedWrite:          true, ActionLabel: "Create or choose a workspace", ActionRoute: "/?create=1",
	}
	folders := CapabilityCard{
		Key: "folders", Label: "Approved folders", Status: CapabilityHealthyEmpty,
		CanRead:              "Only folders already approved on one of your workspaces.",
		CanPropose:           "Workspace-internal file organization where an installed capability supports it.",
		RequiresConfirmation: "Folder access and file-changing operations keep their existing approval gates.",
		MappedWrite:          false, ActionLabel: "Review workspace access", ActionRoute: "/",
	}
	if s.workspaces == nil {
		calendar.Status, projects.Status, folders.Status = CapabilityUnavailable, CapabilityUnavailable, CapabilityUnavailable
		calendar.Reason, projects.Reason, folders.Reason = "source_unavailable", "source_unavailable", "source_unavailable"
		// The workspace read failed, so whether the domain workspace exists is
		// unknown. Claiming it does not exist would push a create suggestion at
		// a user who may already have one; report it as present instead.
		return []CapabilityCard{calendar, projects, folders}, true
	}
	workspaces, err := s.workspaces.ListActive()
	if err != nil {
		calendar.Status, projects.Status, folders.Status = CapabilityUnavailable, CapabilityUnavailable, CapabilityUnavailable
		calendar.Reason, projects.Reason, folders.Reason = "read_failed", "read_failed", "read_failed"
		return []CapabilityCard{calendar, projects, folders}, true
	}
	projectCount := 0
	folderCount := 0
	domainWorkspaceExists := false
	for _, ws := range workspaces {
		if ws == nil || (strings.TrimSpace(ws.OwnerUserID) != "" && strings.TrimSpace(ws.OwnerUserID) != strings.TrimSpace(userID)) {
			continue
		}
		if ws.ID != hqID {
			projectCount++
		}
		if hasSpecialist && ws.TemplateProvenance != nil && entry.MatchesTemplate(ws.TemplateProvenance.TemplateID) {
			domainWorkspaceExists = true
		}
		if ws.TemplateProvenance != nil && ws.TemplateProvenance.TemplateID == "calendar-ops" {
			if hasReadyCalendarBinding(ws) {
				calendar.Status = CapabilityAvailable
				calendar.ActionLabel = "Open Calendar Ops"
			} else {
				calendar.Reason = "connection_not_ready"
				calendar.ActionLabel = "Finish Calendar Ops setup"
			}
			if safeWorkspaceCapabilitySlug(ws.FolderSlug) {
				calendar.ActionRoute = "/workspaces/" + ws.FolderSlug
			}
		}
		if len(ws.DirectoryReferences) > 0 {
			folderCount += len(ws.DirectoryReferences)
			if safeWorkspaceCapabilitySlug(ws.FolderSlug) {
				folders.ActionRoute = "/workspaces/" + ws.FolderSlug + "?panel=settings"
			}
		}
	}
	if projectCount > 0 {
		projects.Status = CapabilityAvailable
		projects.ActionLabel = "Open workspace Map"
		projects.ActionRoute = "/"
	}
	if folderCount > 0 {
		folders.Status = CapabilityAvailable
		folders.ActionLabel = "Review approved folders"
	}
	return []CapabilityCard{calendar, projects, folders}, domainWorkspaceExists
}

func hasReadyCalendarBinding(ws *workspace.Workspace) bool {
	if ws == nil {
		return false
	}
	for _, binding := range ws.GetMCPBindings() {
		if !binding.Enabled {
			continue
		}
		mapping, ok := binding.FindCapabilityMapping("calendar")
		if !ok {
			continue
		}
		if _, calendars := mapping.Operation("list_calendars"); !calendars {
			continue
		}
		if _, events := mapping.Operation("list_events"); events {
			return true
		}
	}
	return false
}

func safeCapabilityRoute(route string) bool {
	return strings.HasPrefix(route, "/") && !strings.HasPrefix(route, "//") && !strings.ContainsAny(route, "\r\n")
}

func safeWorkspaceCapabilitySlug(slug string) bool {
	return slug != "" && !strings.Contains(slug, "/") && !strings.Contains(slug, "..") && !strings.ContainsAny(slug, "?#\r\n")
}
