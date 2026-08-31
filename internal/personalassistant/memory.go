package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type MemoryDestination string

const (
	MemoryDestinationProfile    MemoryDestination = "profile"
	MemoryDestinationPersonalHQ MemoryDestination = "personal_hq"
)

// ProfileMemoryStore is the canonical global preference boundary.
type ProfileMemoryStore interface {
	SetFields(ctx context.Context, id string, fields map[string]any) (*userprofile.UserProfile, error)
}

// HQMemoryStore is implemented by workspace.MemoryStore.
type HQMemoryStore interface {
	AppendUnique(workspaceID string, entry workspace.MemoryEntry) (bool, error)
}

type RememberRequest struct {
	IfVersion   int64
	Destination MemoryDestination
	Text        string
	Preference  string
	Value       string
}

type RememberResult struct {
	Destination MemoryDestination `json:"destination"`
	Text        string            `json:"text"`
	Href        string            `json:"href"`
	Created     bool              `json:"created"`
}

// MemoryService saves only explicit, confirmed memory into existing canonical
// stores. It keeps no cache or reflection queue.
type MemoryService struct {
	store    Store
	hq       PersonalHQReader
	profiles ProfileMemoryStore
	memory   HQMemoryStore
	now      func() time.Time
}

func NewMemoryService(store Store, hq PersonalHQReader, profiles ProfileMemoryStore, memory HQMemoryStore) *MemoryService {
	return &MemoryService{store: store, hq: hq, profiles: profiles, memory: memory, now: time.Now}
}

func (s *MemoryService) Remember(ctx context.Context, userID string, request RememberRequest) (*RememberResult, error) {
	if s == nil || s.store == nil || request.IfVersion < 1 {
		return nil, fmt.Errorf("%w: a positive current state version is required", ErrConflict)
	}
	state, err := s.store.GetState(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	if state.StateVersion != request.IfVersion {
		return nil, fmt.Errorf("%w: expected state version %d", ErrConflict, request.IfVersion)
	}
	if state.Status != StatusActive && state.Status != StatusPaused || state.RenameStep != RenameNone {
		return nil, ErrRepairNeeded
	}
	if s.hq == nil {
		return nil, ErrRepairNeeded
	}
	hq, err := s.hq.Status(ctx, state.UserID)
	if err != nil || hq == nil || !hq.Valid || hq.Workspace == nil || hq.WorkspaceID != state.HQWorkspaceID {
		return nil, ErrRepairNeeded
	}
	linked := false
	for _, instance := range hq.Workspace.AgentInstances {
		if instance.ID == state.HQEntryAgentInstanceID && instance.EntryPoint && strings.EqualFold(instance.Name, state.GlobalAgentProfileName) {
			linked = true
			break
		}
	}
	if !linked {
		return nil, ErrRepairNeeded
	}
	text, err := workspace.ValidateMemoryText(request.Text)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	switch request.Destination {
	case MemoryDestinationProfile:
		if s.profiles == nil {
			return nil, errors.New("personal assistant: profile store is unavailable")
		}
		key := strings.TrimSpace(request.Preference)
		if key != "response_style" && key != "units" && key != "language" {
			return nil, fmt.Errorf("%w: unsupported global preference", ErrValidation)
		}
		value := strings.Join(strings.Fields(request.Value), " ")
		if value == "" {
			return nil, fmt.Errorf("%w: preference value is required", ErrValidation)
		}
		if _, err := s.profiles.SetFields(ctx, state.UserID, map[string]any{"preferences." + key: value}); err != nil {
			return nil, err
		}
		return &RememberResult{Destination: request.Destination, Text: text, Href: "/profile", Created: true}, nil
	case MemoryDestinationPersonalHQ:
		if s.memory == nil {
			return nil, errors.New("personal assistant: workspace memory is unavailable")
		}
		created, err := s.memory.AppendUnique(state.HQWorkspaceID, workspace.MemoryEntry{
			Type: workspace.MemoryTypeFact, Date: s.now().UTC().Format("2006-01-02"), Provenance: "user", Text: text,
		})
		if err != nil {
			return nil, err
		}
		return &RememberResult{
			Destination: request.Destination, Text: text,
			Href: "/workspaces/" + hq.Workspace.FolderSlug + "#memory", Created: created,
		}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported memory destination", ErrValidation)
	}
}
