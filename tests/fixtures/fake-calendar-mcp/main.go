// Command fake-calendar-mcp is a stand-in calendar connector for Calendar Ops
// testing: a real MCP stdio server exposing Google-shaped calendar tools over
// deterministic, entirely fictional data.
//
// It exists because the shipped Calendar preset targets Google's Calendar MCP,
// which needs Developer Preview enrollment, a Google Cloud project, and an
// OAuth client. Nobody can run the Calendar suite — or see the setup wizard
// finish — without one of those, so the alternative to this file is a feature
// whose end-to-end behavior is only ever verified by the person who happens to
// have the access.
//
// It is a real MCP server, not a mock inside the app: the wizard, the mapping
// editor, validation, and the agenda console all exercise the same connector
// path they use in production. What differs is only where the data comes from.
//
// Build and register (see tasks/test-guide-calendar-ops-mcp.md):
//
//	go build -o /tmp/fake-calendar-mcp ./tests/fixtures/fake-calendar-mcp
//	curl -X POST localhost:8931/api/mcp/servers -H 'Content-Type: application/json' \
//	  -d '{"name":"fake-calendar","transport":"stdio","command":"/tmp/fake-calendar-mcp","enabled":true}'
//	curl -X POST localhost:8931/api/mcp/servers/fake-calendar/connect
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The shape is Google's, because the shipped preset's mapping suggestions are
// written for it: results arrive under /items, an event's title is /summary,
// and times are nested under /start/dateTime.
type calendarItem struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type eventTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

type eventItem struct {
	ID          string    `json:"id"`
	Summary     string    `json:"summary"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Start       eventTime `json:"start"`
	End         eventTime `json:"end"`
	AllDay      bool      `json:"allDay,omitempty"`
	Private     bool      `json:"private,omitempty"`
	Attendees   []string  `json:"attendees,omitempty"`
}

type listResult struct {
	Items any `json:"items"`
}

type listCalendarsInput struct{}

type listEventsInput struct {
	CalendarID string `json:"calendarId,omitempty"`
	TimeMin    string `json:"timeMin,omitempty"`
	TimeMax    string `json:"timeMax,omitempty"`
}

type insertEventInput struct {
	CalendarID  string    `json:"calendarId,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Start       eventTime `json:"start,omitempty"`
	End         eventTime `json:"end,omitempty"`
}

type patchEventInput struct {
	CalendarID string    `json:"calendarId,omitempty"`
	EventID    string    `json:"eventId,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Start      eventTime `json:"start,omitempty"`
	End        eventTime `json:"end,omitempty"`
}

// at builds an RFC3339 timestamp on a day offset from today, so the agenda
// console always has something to show whenever the suite happens to run.
func at(dayOffset, hour, minute int) string {
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, dayOffset)
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute).Format(time.RFC3339)
}

func dateOnly(dayOffset int) string {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, dayOffset).Format("2006-01-02")
}

// eventsFor returns one calendar's fictional events. The data is chosen to
// exercise what the console actually has to get right: an all-day entry, a pair
// that overlap (a conflict), a private entry whose details must never be
// rendered, and something later in the week so day and week views differ.
func eventsFor(calendarID string) []eventItem {
	switch calendarID {
	case "team":
		return []eventItem{
			{
				ID: "team-lunch", Summary: "Team lunch",
				Description: "Somewhere with tables outside.",
				Location:    "The corner place",
				Start:       eventTime{DateTime: at(0, 12, 30)},
				End:         eventTime{DateTime: at(0, 13, 30)},
				Attendees:   []string{"ada@example.test", "grace@example.test"},
			},
			{
				ID: "sprint-planning", Summary: "Sprint planning",
				Description: "Groom the backlog, size the top items.",
				Start:       eventTime{DateTime: at(2, 10, 0)},
				End:         eventTime{DateTime: at(2, 11, 0)},
				Attendees:   []string{"ada@example.test"},
			},
		}
	default:
		return []eventItem{
			{
				ID: "offsite", Summary: "Company offsite",
				AllDay: true,
				Start:  eventTime{Date: dateOnly(0)},
				End:    eventTime{Date: dateOnly(1)},
			},
			{
				ID: "standup", Summary: "Standup",
				Description: "What moved, what is stuck.",
				Location:    "Room 2",
				Start:       eventTime{DateTime: at(0, 9, 0)},
				End:         eventTime{DateTime: at(0, 9, 30)},
				Attendees:   []string{"ada@example.test", "grace@example.test"},
			},
			{
				ID: "design-review", Summary: "Design review",
				Description: "Walk the new setup flow end to end.",
				Location:    "Room 4",
				Start:       eventTime{DateTime: at(0, 11, 0)},
				End:         eventTime{DateTime: at(0, 12, 0)},
				Attendees:   []string{"ada@example.test"},
			},
			{
				// Deliberately overlaps the design review: the console must mark
				// both as a conflict rather than quietly stacking them.
				ID: "budget-sync", Summary: "Budget sync",
				Description: "Numbers for the next quarter.",
				Start:       eventTime{DateTime: at(0, 11, 30)},
				End:         eventTime{DateTime: at(0, 12, 30)},
				Attendees:   []string{"grace@example.test"},
			},
			{
				// Private: the console must show that something exists without
				// leaking its title, location, or description.
				ID: "private-appointment", Summary: "Dentist",
				Description: "Do not render me.",
				Location:    "Do not render me either",
				Private:     true,
				Start:       eventTime{DateTime: at(0, 14, 0)},
				End:         eventTime{DateTime: at(0, 15, 0)},
			},
			{
				ID: "retro", Summary: "Retro",
				Description: "What to keep doing.",
				Start:       eventTime{DateTime: at(1, 16, 0)},
				End:         eventTime{DateTime: at(1, 17, 0)},
			},
		}
	}
}

// jsonResult returns the payload as MCP text content, which is how the real
// Google connector returns it — so the mapping's JSON pointers are exercised
// for real rather than shortcut.
func jsonResult(payload any) (*mcp.CallToolResult, any, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
	}, nil, nil
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "fake-calendar",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "calendars_list",
		Description: "List the calendars this account can see.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ listCalendarsInput) (*mcp.CallToolResult, any, error) {
		return jsonResult(listResult{Items: []calendarItem{
			{ID: "primary", Summary: "Primary"},
			{ID: "team", Summary: "Team"},
		}})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "events_list",
		Description: "List events on one calendar within a time window.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in listEventsInput) (*mcp.CallToolResult, any, error) {
		// The window is accepted and deliberately not applied: the fixture's
		// events are anchored to today so both day and week views have
		// something to render, and filtering them here would make the data
		// depend on which view asked.
		return jsonResult(listResult{Items: eventsFor(in.CalendarID)})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "events_insert",
		Description: "Create an event.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in insertEventInput) (*mcp.CallToolResult, any, error) {
		return jsonResult(eventItem{
			ID:          fmt.Sprintf("created-%d", time.Now().UnixNano()),
			Summary:     in.Summary,
			Description: in.Description,
			Location:    in.Location,
			Start:       in.Start,
			End:         in.End,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "events_patch",
		Description: "Update an existing event.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in patchEventInput) (*mcp.CallToolResult, any, error) {
		return jsonResult(eventItem{
			ID:      in.EventID,
			Summary: in.Summary,
			Start:   in.Start,
			End:     in.End,
		})
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("fake-calendar-mcp: %v", err)
	}
}
