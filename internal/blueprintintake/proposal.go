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
	Kind        string         `json:"kind"`
	Key         string         `json:"key"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	DueAt       *time.Time     `json:"due_at,omitempty"`
	DueInput    string         `json:"due_input,omitempty"`
	NoTimeGiven bool           `json:"no_time_given,omitempty"`
	PartlyRead  bool           `json:"partly_read,omitempty"`
	Source      ProposalSource `json:"source"`
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
	Kind        string         `json:"kind"`
	Key         string         `json:"key"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	DueAt       string         `json:"due_at,omitempty"`
	Source      ProposalSource `json:"source"`
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
		if !bounded(rawItem.Key, 128) || !bounded(rawItem.Title, 240) || len([]rune(rawItem.Description)) > 4000 || len([]rune(rawItem.Source.Quote)) > 200 || seen[rawItem.Key] {
			return nil, ProposalNotice{}, ErrUnreadableProposal
		}
		seen[rawItem.Key] = true
		item := ProposalItem{Kind: rawItem.Kind, Key: rawItem.Key, Title: rawItem.Title, Description: rawItem.Description, DueInput: strings.TrimSpace(rawItem.DueAt), PartlyRead: partlyRead, Source: rawItem.Source}
		if item.Kind == "ticket" && item.DueInput != "" {
			due, noTime, err := parseProposalDueAt(item.DueInput, location)
			if err != nil {
				return nil, ProposalNotice{}, ErrUnreadableProposal
			}
			item.DueAt, item.NoTimeGiven = &due, noTime
		}
		items = append(items, item)
	}
	return items, notice, nil
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
	data, err := json.Marshal(struct {
		WorkspaceID string         `json:"workspace_id"`
		IntakeKey   string         `json:"intake_key"`
		Items       []ProposalItem `json:"items"`
		Notice      ProposalNotice `json:"notice"`
	}{workspaceID, intakeKey, items, notice})
	if err != nil {
		return Proposal{}, fmt.Errorf("hash intake proposal: %w", err)
	}
	digest := sha256.Sum256(data)
	proposal.Hash = hex.EncodeToString(digest[:])
	return proposal, nil
}
