package personalassistant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/database"
)

// A hire the personal assistant proposed from a plain request
// (assistant-hires-from-a-request) is kept on the conversation it was offered
// in. The card the drawer shows is drawn from this record, so a reload shows
// the same card; a decline is remembered here for the rest of that
// conversation; and the hire it became (POST /api/agents/hire with this
// proposal's id as the request id) is linked back to it. Every field is short
// text the proposal wrote itself; none is a path.

// HireProposalState is where a proposal stands.
type HireProposalState string

const (
	// HireProposalOffered is a card with its buttons: nothing has happened yet.
	HireProposalOffered HireProposalState = "offered"
	// HireProposalDeclined is a card put aside with Not now.
	HireProposalDeclined HireProposalState = "declined"
	// HireProposalAdjusted is a card taken to the workspace form by hand.
	HireProposalAdjusted HireProposalState = "adjusted"
	// HireProposalHired is a card whose Hire and start made an agent.
	HireProposalHired HireProposalState = "hired"
)

// Bounds on a proposal's text.
const (
	hireProposalIDMax             = 128
	hireProposalRoleMax           = 40
	hireProposalNameMax           = 100
	hireProposalDomainMax         = 80
	hireProposalBlueprintMax      = 80
	hireProposalBlueprintLabelMax = 80
	hireProposalOpenRolesMax      = 20
)

// ErrHireProposalNotFound is a proposal id with no record.
var ErrHireProposalNotFound = errors.New("personal assistant: no such hire proposal")

// HireProposal is one proposed hire as the assistant keeps it.
type HireProposal struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	ConversationID string `json:"conversation_id"`
	// MessageID is the assistant reply the card sits under.
	MessageID string `json:"message_id,omitempty"`
	// What was proposed: a catalog role, a name for the agent, the Specialist's
	// domain, the brief in the user's words, and the workspace's name.
	Role          string `json:"role"`
	Name          string `json:"name"`
	Domain        string `json:"domain,omitempty"`
	Brief         string `json:"brief"`
	WorkspaceName string `json:"workspace_name"`
	// The blueprint the role's hire starts from, as the card names it.
	Blueprint      string            `json:"blueprint"`
	BlueprintLabel string            `json:"blueprint_label"`
	OpenRoles      int               `json:"open_roles"`
	State          HireProposalState `json:"state"`
	// What the proposal became, once hired.
	HireID      string    `json:"hire_id,omitempty"`
	AgentName   string    `json:"agent_name,omitempty"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// HireProposalStore keeps the assistant's hire proposals.
type HireProposalStore interface {
	Create(ctx context.Context, proposal HireProposal) (HireProposal, error)
	Get(ctx context.Context, id string) (HireProposal, error)
	// ByConversation lists one user's proposals in one conversation, oldest
	// first.
	ByConversation(ctx context.Context, userID, conversationID string) ([]HireProposal, error)
	// Update changes one proposal in a transaction. mutate may change the
	// proposal's text, state and what it became; its identity, owner,
	// conversation and creation time stay as they were.
	Update(ctx context.Context, id string, mutate func(*HireProposal) error) (HireProposal, error)
}

// SQLiteHireProposals stores proposals in the shared application database.
type SQLiteHireProposals struct {
	db  *database.DB
	now func() time.Time
}

var _ HireProposalStore = (*SQLiteHireProposals)(nil)

// NewSQLiteHireProposals constructs the proposal store.
func NewSQLiteHireProposals(db *database.DB) *SQLiteHireProposals {
	return &SQLiteHireProposals{db: db, now: time.Now}
}

func (s *SQLiteHireProposals) ready() error {
	if s == nil || s.db == nil {
		return errors.New("personal assistant: hire proposals are not configured")
	}
	return nil
}

// validate keeps a proposal bounded, path-free and in a known state.
func (p *HireProposal) validate() error {
	required := []struct {
		label, value string
		max          int
	}{
		{"proposal id", p.ID, hireProposalIDMax},
		{"user id", p.UserID, hireProposalIDMax},
		{"conversation id", p.ConversationID, hireProposalIDMax},
		{"role", p.Role, hireProposalRoleMax},
		{"name", p.Name, hireProposalNameMax},
		{"workspace name", p.WorkspaceName, HireWorkspaceNameMaxRunes},
		{"blueprint", p.Blueprint, hireProposalBlueprintMax},
		{"blueprint label", p.BlueprintLabel, hireProposalBlueprintLabelMax},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" || utf8.RuneCountInString(field.value) > field.max ||
			strings.ContainsAny(field.value, "\x00\r\n\t") || looksLikeFilesystemPath(field.value) {
			return fmt.Errorf("%w: hire proposal %s", ErrValidation, field.label)
		}
	}
	optional := []struct {
		label, value string
		max          int
	}{
		{"message id", p.MessageID, hireProposalIDMax},
		{"domain", p.Domain, hireProposalDomainMax},
		{"hire id", p.HireID, hireProposalIDMax},
		{"agent name", p.AgentName, hireProposalNameMax},
		{"workspace id", p.WorkspaceID, hireProposalIDMax},
	}
	for _, field := range optional {
		if utf8.RuneCountInString(field.value) > field.max || strings.ContainsAny(field.value, "\x00\r\n\t") || looksLikeFilesystemPath(field.value) {
			return fmt.Errorf("%w: hire proposal %s", ErrValidation, field.label)
		}
	}
	if _, err := validateText("brief", p.Brief, WorkspaceGoalMaxRunes, true); err != nil {
		return fmt.Errorf("%w: %s", ErrValidation, err.Error())
	}
	if p.OpenRoles < 0 || p.OpenRoles > hireProposalOpenRolesMax {
		return fmt.Errorf("%w: hire proposal open roles", ErrValidation)
	}
	switch p.State {
	case HireProposalOffered, HireProposalDeclined, HireProposalAdjusted, HireProposalHired:
	default:
		return fmt.Errorf("%w: hire proposal state", ErrValidation)
	}
	return nil
}

// Create stores a new proposal.
func (s *SQLiteHireProposals) Create(ctx context.Context, proposal HireProposal) (HireProposal, error) {
	if err := s.ready(); err != nil {
		return HireProposal{}, err
	}
	now := s.now().UTC()
	proposal.CreatedAt, proposal.UpdatedAt = now, now
	if err := proposal.validate(); err != nil {
		return HireProposal{}, err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO assistant_hire_proposals (id, user_id, conversation_id, message_id, role, name, domain, brief,
			workspace_name, blueprint, blueprint_label, open_roles, state, hire_id, agent_name, workspace_id,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		proposal.ID, proposal.UserID, proposal.ConversationID, proposal.MessageID, proposal.Role, proposal.Name,
		proposal.Domain, proposal.Brief, proposal.WorkspaceName, proposal.Blueprint, proposal.BlueprintLabel,
		proposal.OpenRoles, string(proposal.State), proposal.HireID, proposal.AgentName, proposal.WorkspaceID,
		proposal.CreatedAt, proposal.UpdatedAt)
	if err != nil {
		return HireProposal{}, fmt.Errorf("personal assistant: create hire proposal: %w", err)
	}
	return proposal, nil
}

const hireProposalColumns = `id, user_id, conversation_id, message_id, role, name, domain, brief,
	workspace_name, blueprint, blueprint_label, open_roles, state, hire_id, agent_name, workspace_id,
	created_at, updated_at`

func scanHireProposal(row hireRowScanner) (HireProposal, error) {
	var p HireProposal
	var state string
	err := row.Scan(&p.ID, &p.UserID, &p.ConversationID, &p.MessageID, &p.Role, &p.Name, &p.Domain, &p.Brief,
		&p.WorkspaceName, &p.Blueprint, &p.BlueprintLabel, &p.OpenRoles, &state, &p.HireID, &p.AgentName, &p.WorkspaceID,
		&p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HireProposal{}, ErrHireProposalNotFound
	}
	if err != nil {
		return HireProposal{}, fmt.Errorf("personal assistant: read hire proposal: %w", err)
	}
	p.State = HireProposalState(state)
	return p, nil
}

// Get reads one proposal by its id.
func (s *SQLiteHireProposals) Get(ctx context.Context, id string) (HireProposal, error) {
	if err := s.ready(); err != nil {
		return HireProposal{}, err
	}
	return scanHireProposal(s.db.QueryRowContext(ctx, `SELECT `+hireProposalColumns+` FROM assistant_hire_proposals WHERE id = ?`, id))
}

// ByConversation lists one user's proposals in one conversation, oldest first.
func (s *SQLiteHireProposals) ByConversation(ctx context.Context, userID, conversationID string) ([]HireProposal, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+hireProposalColumns+` FROM assistant_hire_proposals
		WHERE user_id = ? AND conversation_id = ? ORDER BY created_at, id`, userID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: list hire proposals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var proposals []HireProposal
	for rows.Next() {
		proposal, err := scanHireProposal(rows)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, proposal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("personal assistant: list hire proposals: %w", err)
	}
	return proposals, nil
}

// Update changes one proposal in a transaction.
func (s *SQLiteHireProposals) Update(ctx context.Context, id string, mutate func(*HireProposal) error) (HireProposal, error) {
	if err := s.ready(); err != nil {
		return HireProposal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HireProposal{}, fmt.Errorf("personal assistant: update hire proposal: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanHireProposal(tx.QueryRowContext(ctx, `SELECT `+hireProposalColumns+` FROM assistant_hire_proposals WHERE id = ?`, id))
	if err != nil {
		return HireProposal{}, err
	}
	next := current
	if err := mutate(&next); err != nil {
		return HireProposal{}, err
	}
	next.ID, next.UserID, next.ConversationID, next.CreatedAt = current.ID, current.UserID, current.ConversationID, current.CreatedAt
	next.UpdatedAt = s.now().UTC()
	if err := next.validate(); err != nil {
		return HireProposal{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE assistant_hire_proposals SET message_id = ?, role = ?, name = ?, domain = ?, brief = ?, workspace_name = ?,
			blueprint = ?, blueprint_label = ?, open_roles = ?, state = ?, hire_id = ?, agent_name = ?, workspace_id = ?, updated_at = ?
		WHERE id = ?`,
		next.MessageID, next.Role, next.Name, next.Domain, next.Brief, next.WorkspaceName,
		next.Blueprint, next.BlueprintLabel, next.OpenRoles, string(next.State), next.HireID, next.AgentName, next.WorkspaceID, next.UpdatedAt,
		next.ID); err != nil {
		return HireProposal{}, fmt.Errorf("personal assistant: update hire proposal: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return HireProposal{}, fmt.Errorf("personal assistant: update hire proposal: %w", err)
	}
	return next, nil
}
