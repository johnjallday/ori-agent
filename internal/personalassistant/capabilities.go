package personalassistant

import (
	"context"
	"fmt"
	"strings"

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
	cards := []CapabilityCard{emailCapabilityCard(ctx, s.email, relationship.HQWorkspaceID)}
	cards = append(cards, s.workspaceCapabilityCards(userID, relationship.HQWorkspaceID)...)
	return &CapabilityProjection{State: string(relationship.State), Cards: cards}, nil
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

func (s *CapabilityService) workspaceCapabilityCards(userID, hqID string) []CapabilityCard {
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
		return []CapabilityCard{calendar, projects, folders}
	}
	workspaces, err := s.workspaces.ListActive()
	if err != nil {
		calendar.Status, projects.Status, folders.Status = CapabilityUnavailable, CapabilityUnavailable, CapabilityUnavailable
		calendar.Reason, projects.Reason, folders.Reason = "read_failed", "read_failed", "read_failed"
		return []CapabilityCard{calendar, projects, folders}
	}
	projectCount := 0
	folderCount := 0
	for _, ws := range workspaces {
		if ws == nil || (strings.TrimSpace(ws.OwnerUserID) != "" && strings.TrimSpace(ws.OwnerUserID) != strings.TrimSpace(userID)) {
			continue
		}
		if ws.ID != hqID {
			projectCount++
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
	return []CapabilityCard{calendar, projects, folders}
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
