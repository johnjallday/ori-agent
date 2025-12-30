package chathttp

import (
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/store"
)

// CommandHandler handles special chat commands
type CommandHandler struct {
	store          store.Store
	workspaceStore agentstudio.Store
	enumExtractor  *pluginhttp.EnumExtractor
	shutdownFunc   func()
}

// NewCommandHandler creates a new command handler
func NewCommandHandler(store store.Store) *CommandHandler {
	return &CommandHandler{
		store:         store,
		enumExtractor: pluginhttp.NewEnumExtractor(),
	}
}

// SetWorkspaceStore sets the workspace store for workspace commands
func (ch *CommandHandler) SetWorkspaceStore(ws agentstudio.Store) {
	ch.workspaceStore = ws
}

// SetShutdownFunc sets the shutdown function to be called on exit
func (ch *CommandHandler) SetShutdownFunc(fn func()) {
	ch.shutdownFunc = fn
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
