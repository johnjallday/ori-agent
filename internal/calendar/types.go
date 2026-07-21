package calendar

// CapabilityKey is the abstract capability identifier used in template
// manifests and workspace MCPBinding.CapabilityMappings for calendar
// connectors.
const CapabilityKey = "calendar"

// Calendar is the canonical representation of a connector's calendar (a
// container of events -- e.g. a Google Calendar "calendar" or an account's
// default mailbox calendar).
type Calendar struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Primary  bool   `json:"primary,omitempty"`
	TimeZone string `json:"time_zone,omitempty"`
	Color    string `json:"color,omitempty"`
}

// Attendee is a canonical event participant.
type Attendee struct {
	Email          string `json:"email,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
	ResponseStatus string `json:"response_status,omitempty"` // accepted|declined|tentative|needs_action
	Organizer      bool   `json:"organizer,omitempty"`
}

// Event is the canonical representation of a calendar event, deterministically
// assembled from a connector's mapped MCP tool result. StartTime/EndTime are
// RFC3339 strings (never a bare date-only value) so all-day events are
// distinguished via AllDay rather than by time format.
type Event struct {
	ID             string     `json:"id"`
	CalendarID     string     `json:"calendar_id,omitempty"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	Location       string     `json:"location,omitempty"`
	StartTime      string     `json:"start_time"` // RFC3339
	EndTime        string     `json:"end_time"`   // RFC3339
	TimeZone       string     `json:"time_zone,omitempty"`
	AllDay         bool       `json:"all_day,omitempty"`
	Private        bool       `json:"private,omitempty"`
	Canceled       bool       `json:"canceled,omitempty"`
	ResponseStatus string     `json:"response_status,omitempty"`
	Attendees      []Attendee `json:"attendees,omitempty"`
	ConferenceLink string     `json:"conference_link,omitempty"`
	SourceLink     string     `json:"source_link,omitempty"`
	Recurring      bool       `json:"recurring,omitempty"` // recurrence marker; no series editing in v1
}

// Account is the canonical representation of a connected calendar account
// (used by the optional list_accounts/connect_account operations).
type Account struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Email string `json:"email,omitempty"`
}

// TimeSlot is a canonical [start,end) window, used for freebusy and
// suggest_time results.
type TimeSlot struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

// Semantic calendar operation names. These are the only operation names the
// calendar capability recognizes; a mapping naming anything else is rejected
// by ValidateMapping.
const (
	OpListCalendars  = "list_calendars"
	OpListEvents     = "list_events"
	OpGetEvent       = "get_event"
	OpFreeBusy       = "freebusy"
	OpSuggestTime    = "suggest_time"
	OpCreateEvent    = "create_event"
	OpUpdateEvent    = "update_event"
	OpListAccounts   = "list_accounts"
	OpConnectAccount = "connect_account"
)

// requiredOperations must be mapped before the calendar capability is
// considered ready (FR10).
var requiredOperations = []string{OpListCalendars, OpListEvents}

// optionalOperations may be mapped; the corresponding UI action only appears
// when they are (FR16).
var optionalOperations = []string{OpGetEvent, OpFreeBusy, OpSuggestTime, OpCreateEvent, OpUpdateEvent, OpListAccounts, OpConnectAccount}

// RequiredOperations returns the calendar capability's required operation
// names. Returns a fresh slice each call so callers can't mutate the package
// default.
func RequiredOperations() []string { return append([]string{}, requiredOperations...) }

// OptionalOperations returns the calendar capability's optional operation
// names.
func OptionalOperations() []string { return append([]string{}, optionalOperations...) }

// AllOperations returns every operation name the calendar capability
// recognizes, required first.
func AllOperations() []string {
	out := append([]string{}, requiredOperations...)
	return append(out, optionalOperations...)
}

// operationContract describes what a mapping for a given operation must
// provide: which canonical fields are required/optional, whether the
// operation writes (fields come from Arguments) or reads (fields come from
// Fields), and whether its result is a collection (needs ResultCollection)
// or a single object.
type operationContract struct {
	RequiredFields []string
	OptionalFields []string
	IsWrite        bool
	IsCollection   bool
}

var eventReadFields = struct {
	required []string
	optional []string
}{
	required: []string{"id", "title", "start_time", "end_time"},
	optional: []string{
		"calendar_id", "time_zone", "all_day", "private", "canceled",
		"response_status", "attendees", "location", "description",
		"conference_link", "source_link", "recurring",
	},
}

var operationContracts = map[string]operationContract{
	OpListCalendars: {
		RequiredFields: []string{"id", "name"},
		OptionalFields: []string{"primary", "time_zone", "color"},
		IsCollection:   true,
	},
	OpListEvents: {
		RequiredFields: eventReadFields.required,
		OptionalFields: eventReadFields.optional,
		IsCollection:   true,
	},
	OpGetEvent: {
		RequiredFields: eventReadFields.required,
		OptionalFields: eventReadFields.optional,
		IsCollection:   false,
	},
	OpFreeBusy: {
		RequiredFields: []string{"start_time", "end_time"},
		IsCollection:   true,
	},
	OpSuggestTime: {
		RequiredFields: []string{"start_time", "end_time"},
		IsCollection:   true,
	},
	OpCreateEvent: {
		RequiredFields: []string{"calendar_id", "title", "start_time", "end_time"},
		OptionalFields: []string{"description", "location", "time_zone", "attendees"},
		IsWrite:        true,
	},
	OpUpdateEvent: {
		RequiredFields: []string{"id", "calendar_id"},
		OptionalFields: []string{"title", "start_time", "end_time", "description", "location", "time_zone", "attendees"},
		IsWrite:        true,
	},
	OpListAccounts: {
		RequiredFields: []string{"id"},
		OptionalFields: []string{"label", "email"},
		IsCollection:   true,
	},
	OpConnectAccount: {
		IsWrite: true,
	},
}
