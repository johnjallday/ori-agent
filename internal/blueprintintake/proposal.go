package blueprintintake

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const MaxProposalItems = 200

var ErrUnreadableProposal = errors.New("the skill returned something Ori could not read")

type ProposalSource struct {
	SourceID string `json:"source_id"`
	Quote    string `json:"quote"`
}

type ProposalItem struct {
	Kind           string         `json:"kind"`
	Key            string         `json:"key"`
	Title          string         `json:"title,omitempty"`
	Description    string         `json:"description,omitempty"`
	Text           string         `json:"text,omitempty"`
	MemoryType     string         `json:"memory_type,omitempty"`
	Body           string         `json:"body,omitempty"`
	DueAt          *time.Time     `json:"due_at,omitempty"`
	DueInput       string         `json:"due_input,omitempty"`
	Start          *time.Time     `json:"start,omitempty"`
	End            *time.Time     `json:"end,omitempty"`
	AllDay         bool           `json:"all_day,omitempty"`
	Location       string         `json:"location,omitempty"`
	NoTimeGiven    bool           `json:"no_time_given,omitempty"`
	PartlyRead     bool           `json:"partly_read,omitempty"`
	UnusableReason string         `json:"unusable_reason,omitempty"`
	DisabledReason string         `json:"disabled_reason,omitempty"`
	Source         ProposalSource `json:"source"`
}

type ProposalNotice struct {
	Unsupported int  `json:"unsupported,omitempty"`
	DroppedKind int  `json:"dropped_kind,omitempty"`
	Truncated   bool `json:"truncated,omitempty"`
}

type Proposal struct {
	WorkspaceID string         `json:"workspace_id"`
	IntakeKey   string         `json:"intake_key"`
	Hash        string         `json:"hash"`
	Status      string         `json:"status"`
	Items       []ProposalItem `json:"items"`
	Notice      ProposalNotice `json:"notice,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Results     []ApplyResult  `json:"results,omitempty"`
}

type proposalEnvelope struct {
	Items []rawProposalItem `json:"items"`
}

type rawProposalItem struct {
	Kind        string          `json:"kind"`
	Key         string          `json:"key"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Text        string          `json:"text,omitempty"`
	MemoryType  string          `json:"memory_type,omitempty"`
	Body        string          `json:"body,omitempty"`
	DueAt       string          `json:"due_at,omitempty"`
	Start       string          `json:"start,omitempty"`
	End         string          `json:"end,omitempty"`
	AllDay      json.RawMessage `json:"all_day,omitempty"`
	Location    string          `json:"location,omitempty"`
	Recurrence  json.RawMessage `json:"recurrence,omitempty"`
	Source      ProposalSource  `json:"source"`
}

func ParseProposalOutput(raw string, requirement workspace.IntakeRequirement, sourceID string, location *time.Location, partlyRead bool) ([]ProposalItem, ProposalNotice, error) {
	if location == nil {
		location = time.Local
	}
	decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	var envelope proposalEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return nil, ProposalNotice{}, ErrUnreadableProposal
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ProposalNotice{}, ErrUnreadableProposal
	}
	allowed := make(map[string]bool, len(requirement.ProposalKinds))
	for _, kind := range requirement.ProposalKinds {
		allowed[kind] = true
	}
	notice := ProposalNotice{}
	items := make([]ProposalItem, 0, min(len(envelope.Items), MaxProposalItems))
	seen := make(map[string]bool)
	for index, rawItem := range envelope.Items {
		if index >= MaxProposalItems {
			notice.Truncated = true
			break
		}
		rawItem.Kind = strings.ToLower(strings.TrimSpace(rawItem.Kind))
		rawItem.Key = strings.TrimSpace(rawItem.Key)
		rawItem.Title = strings.TrimSpace(rawItem.Title)
		rawItem.Description = strings.TrimSpace(rawItem.Description)
		rawItem.Text = strings.TrimSpace(rawItem.Text)
		rawItem.MemoryType = strings.TrimSpace(rawItem.MemoryType)
		rawItem.Body = strings.TrimSpace(rawItem.Body)
		rawItem.Location = strings.TrimSpace(rawItem.Location)
		rawItem.Source.SourceID = strings.TrimSpace(rawItem.Source.SourceID)
		rawItem.Source.Quote = strings.TrimSpace(rawItem.Source.Quote)
		if !allowed[rawItem.Kind] {
			notice.DroppedKind++
			continue
		}
		if rawItem.Source.SourceID != sourceID || rawItem.Source.Quote == "" {
			notice.Unsupported++
			continue
		}
		if len(rawItem.Recurrence) > 0 {
			return nil, ProposalNotice{}, ErrUnreadableProposal
		}
		if !bounded(rawItem.Key, 128) || len([]rune(rawItem.Title)) > 240 || len([]rune(rawItem.Description)) > 4000 || len([]rune(rawItem.Source.Quote)) > 200 || seen[rawItem.Key] {
			return nil, ProposalNotice{}, ErrUnreadableProposal
		}
		seen[rawItem.Key] = true
		item := ProposalItem{Kind: rawItem.Kind, Key: rawItem.Key, Title: rawItem.Title, Description: rawItem.Description, Text: rawItem.Text, MemoryType: rawItem.MemoryType, Body: rawItem.Body, DueInput: strings.TrimSpace(rawItem.DueAt), Location: rawItem.Location, PartlyRead: partlyRead, Source: rawItem.Source}
		switch item.Kind {
		case "ticket":
			if !bounded(item.Title, 240) {
				return nil, ProposalNotice{}, ErrUnreadableProposal
			}
			if item.DueInput != "" {
				due, noTime, err := parseProposalDueAt(item.DueInput, location)
				if err != nil {
					return nil, ProposalNotice{}, ErrUnreadableProposal
				}
				item.DueAt, item.NoTimeGiven = &due, noTime
			}
		case "memory":
			validated, validateErr := workspace.ValidateMemoryText(item.Text)
			if validateErr != nil {
				item.UnusableReason = validateErr.Error()
			} else {
				item.Text = validated
			}
			item.MemoryType = string(workspace.NormalizeMemoryEntryType(item.MemoryType))
		case "note":
			if !bounded(item.Title, 240) || !bounded(item.Body, 20_000) {
				item.UnusableReason = "Note title or body is outside the allowed length."
			}
		case "calendar_event":
			if !bounded(item.Title, 240) || len([]rune(item.Location)) > 500 {
				item.UnusableReason = "Calendar event title or location is invalid."
				break
			}
			start, end, allDay, eventErr := parseProposalEventTimes(rawItem.Start, rawItem.End, rawItem.AllDay, location)
			if eventErr != nil || !end.After(start) {
				item.UnusableReason = "Calendar event start and end must be valid and ordered."
			} else {
				item.Start, item.End, item.AllDay = &start, &end, allDay
			}
		}
		items = append(items, item)
	}
	return items, notice, nil
}

func parseProposalEventTimes(startInput, endInput string, allDayInput json.RawMessage, location *time.Location) (time.Time, time.Time, bool, error) {
	if len(allDayInput) > 0 {
		var dateInput string
		if err := json.Unmarshal(allDayInput, &dateInput); err == nil && dateInput != "" {
			start, dateErr := time.ParseInLocation("2006-01-02", dateInput, location)
			return start, start.AddDate(0, 0, 1), true, dateErr
		}
		var allDay bool
		if err := json.Unmarshal(allDayInput, &allDay); err != nil || !allDay {
			return time.Time{}, time.Time{}, false, errors.New("invalid all_day value")
		}
		start, startErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(startInput), location)
		end, endErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(endInput), location)
		if startErr != nil {
			return time.Time{}, time.Time{}, false, startErr
		}
		return start, end, true, endErr
	}
	start, startErr := time.Parse(time.RFC3339, strings.TrimSpace(startInput))
	end, endErr := time.Parse(time.RFC3339, strings.TrimSpace(endInput))
	if startErr != nil {
		return time.Time{}, time.Time{}, false, startErr
	}
	return start, end, false, endErr
}

func parseProposalDueAt(value string, location *time.Location) (time.Time, bool, error) {
	if len(value) == len("2006-01-02") {
		date, err := time.ParseInLocation("2006-01-02", value, location)
		if err != nil {
			return time.Time{}, false, err
		}
		return time.Date(date.Year(), date.Month(), date.Day(), 0, 1, 0, 0, location), true, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false, err
	}
	return parsed, false, nil
}

func bounded(value string, max int) bool {
	length := len([]rune(value))
	return length > 0 && length <= max && !strings.ContainsRune(value, 0)
}

func finalizeProposal(workspaceID, intakeKey string, items []ProposalItem, notice ProposalNotice, now time.Time) (Proposal, error) {
	proposal := Proposal{WorkspaceID: workspaceID, IntakeKey: intakeKey, Status: "pending", Items: items, Notice: notice, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if err := rehashProposal(&proposal); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

func rehashProposal(proposal *Proposal) error {
	data, err := json.Marshal(struct {
		WorkspaceID string         `json:"workspace_id"`
		IntakeKey   string         `json:"intake_key"`
		Items       []ProposalItem `json:"items"`
		Notice      ProposalNotice `json:"notice"`
	}{proposal.WorkspaceID, proposal.IntakeKey, proposal.Items, proposal.Notice})
	if err != nil {
		return fmt.Errorf("hash intake proposal: %w", err)
	}
	digest := sha256.Sum256(data)
	proposal.Hash = hex.EncodeToString(digest[:])
	return nil
}
