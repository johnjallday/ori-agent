package chathttp

import (
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// CommandHandler handles special chat commands
type CommandHandler struct {
	store          store.Store
	workspaceStore workspace.Store
	shutdownFunc   func()
	skillsManager  interface {
		GetSkill(string, string) (*skills.Skill, bool, error)
		ListSkills(string) ([]skills.Skill, error)
	}
}

// NewCommandHandler creates a new command handler
func NewCommandHandler(store store.Store) *CommandHandler {
	return &CommandHandler{
		store: store,
	}
}

// SetWorkspaceStore sets the workspace store for workspace commands
func (ch *CommandHandler) SetWorkspaceStore(ws workspace.Store) {
	ch.workspaceStore = ws
}

// SetShutdownFunc sets the shutdown function to be called on exit
func (ch *CommandHandler) SetShutdownFunc(fn func()) {
	ch.shutdownFunc = fn
}

// SetSkillsManager sets the skills manager for /skills command
func (ch *CommandHandler) SetSkillsManager(manager interface {
	GetSkill(string, string) (*skills.Skill, bool, error)
	ListSkills(string) ([]skills.Skill, error)
}) {
	ch.skillsManager = manager
}

// interfaceSliceToStrings converts an interface slice to a string slice
func interfaceSliceToStrings(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return nil
	}
}
