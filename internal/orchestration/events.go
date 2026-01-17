package orchestration

import (
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func (o *Orchestrator) publishEvent(eventType workspace.EventType, workspaceID string, data map[string]interface{}) {
	if o.eventBus == nil {
		return
	}

	o.eventBus.Publish(workspace.Event{
		ID:          uuid.New().String(),
		Type:        eventType,
		WorkspaceID: workspaceID,
		Timestamp:   time.Now(),
		Source:      "orchestrator",
		Data:        data,
		Metadata:    map[string]string{},
	})
}
