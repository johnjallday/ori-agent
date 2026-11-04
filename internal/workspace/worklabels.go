package workspace

// The words the server uses when it shows a person a unit of work.
//
// A unit of work an agent does is a ticket; Backlog is the state a ticket
// waits in (TaskStatusBacklog), not another name for it. The Go type, its
// routes and JSON fields keep saying "task" — only display text uses these.
// The web UI's counterpart is internal/web/static/js/modules/work-labels.js.
const (
	// LabelTicket is the singular noun for a work item.
	LabelTicket = "Ticket"
	// LabelTickets is the plural noun and the title of a list of work items.
	LabelTickets = "Tickets"
	// LabelBacklog is the state (and list) of tickets saved for later.
	LabelBacklog = "Backlog"
	// LabelRunYourFirstTicket is the first-run checklist step that runs work.
	LabelRunYourFirstTicket = "Run your first ticket"
	// LabelGiveAnAgentATicket is the checklist step that delegates work.
	LabelGiveAnAgentATicket = "Give an agent a ticket"
	// LabelResultGroupTitle names the default group of items parsed from a
	// run's result when the result has no headed sections.
	LabelResultGroupTitle = LabelTickets
)
