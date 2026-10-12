package server

import (
	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/assistantops"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// assistantOperationStore keeps "Operate Ori" operations in the application
// database, beside the conversations they belong to. Without a database there
// is no store, and the assistant is offered no operation.
func (b *ServerBuilder) assistantOperationStore() assistantops.Store {
	if b == nil || b.sessionStore == nil || b.sessionStore.DB() == nil {
		return nil
	}
	return assistantops.NewSQLiteStore(b.sessionStore.DB())
}

// wireAssistantOperations gives the work handler its operation store. The
// handler supplies the host checks itself (current user, relationship,
// conversation owner, workspace access), so every operation is revalidated
// against the same canonical state the conversation uses.
func (s *Server) wireAssistantOperations(handler *agenthttp.HomeAssistantAskHandler) {
	if s == nil || handler == nil || s.Storage == nil || s.Storage.AssistantOperations == nil {
		return
	}
	handler.SetOperationStore(s.Storage.AssistantOperations)
	// A prepared task is committed by the task form through the canonical
	// create, and started by the task owner. Operations get exactly those two
	// seams: the create handler asks the service to reserve and confirm the
	// operation's one task, and the service asks the task owner what a task
	// says about itself and for its first start. Without an orchestration
	// handler a draft can still be prepared and saved as an ordinary task.
	if handler.Operations != nil && s.Handlers != nil && s.Handlers.Orchestration != nil {
		handler.Operations.SetTaskHost(s.Handlers.Orchestration.OperationTaskHost())
		s.Handlers.Orchestration.SetTaskOperationBinder(handler.Operations)
	}
	// The drawer learns whether Watch can be offered from the status it already
	// reads, so no page makes a request of its own to find out.
	if s.Handlers != nil && s.Handlers.PersonalAssistant != nil {
		s.Handlers.PersonalAssistant.SetOperationsAvailability(func() personalassistant.SourceAvailability {
			return operationsAvailability(handler.OperationCapability())
		})
	}
}

// operationsAvailability states the host's capability in the relationship
// status vocabulary. "No model" is something to configure; anything else is
// simply unavailable here, and the manual controls keep working.
func operationsAvailability(capability agenthttp.AssistantOperationCapability) personalassistant.SourceAvailability {
	switch {
	case capability.Available:
		return personalassistant.SourceAvailability{Available: true, Status: personalassistant.AvailabilityAvailable}
	case capability.Reason == "model_not_configured":
		return personalassistant.SourceAvailability{Status: personalassistant.AvailabilityNotConfigured, Reason: capability.Reason}
	}
	return personalassistant.SourceAvailability{Status: personalassistant.AvailabilityUnavailable, Reason: capability.Reason}
}
