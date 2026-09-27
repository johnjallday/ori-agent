package personalassistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/types"
)

// The workspace-build sidecar holds "Build with your assistant" sessions: the
// conversation in which the Personal Assistant fills the Create Workspace
// wizard. It lives beside the knowledge sidecar under <HQ>/.ori/ and is
// written with the same discipline (locked, atomic, bounded). A session's
// draft is exactly the create request the wizard submits; nothing here
// creates, installs, or attaches anything.
const (
	WorkspaceBuildSchemaVersion = 1
	workspaceBuildFileName      = "workspace-build-v1.json"
	workspaceBuildLockName      = "workspace-build-v1.lock"
	workspaceBuildMaxBytes      = 512 * 1024

	// WorkspaceBuildMaxEntries and WorkspaceBuildMaxTranscriptBytes bound the
	// stored transcript; the oldest entries go first once either is reached.
	WorkspaceBuildMaxEntries         = 40
	WorkspaceBuildMaxTranscriptBytes = 32 * 1024
	// WorkspaceBuildExpiry abandons a build nobody has touched for a week.
	WorkspaceBuildExpiry = 7 * 24 * time.Hour
	// WorkspaceBuildMaxText bounds one message the user types.
	WorkspaceBuildMaxText = 2000
	// WorkspaceBuildMaxSay bounds one assistant line.
	WorkspaceBuildMaxSay = 600
	// WorkspaceBuildMaxFirstRequest bounds the request kept for provenance.
	WorkspaceBuildMaxFirstRequest = 500

	// workspaceBuildMaxSessions keeps the open build plus a few settled ones,
	// so a create that finishes after its session settled can still find it.
	workspaceBuildMaxSessions = 8
	workspaceBuildMaxName     = 200
	workspaceBuildMaxShort    = 400
	workspaceBuildMaxChoices  = 4
	workspaceBuildMaxWhy      = 8
	workspaceBuildMaxList     = 32
)

// WorkspaceBuildStatus is where a build is in its life.
type WorkspaceBuildStatus string

const (
	WorkspaceBuildOpen      WorkspaceBuildStatus = "open"
	WorkspaceBuildCreated   WorkspaceBuildStatus = "created"
	WorkspaceBuildAbandoned WorkspaceBuildStatus = "abandoned"
)

// Transcript roles. A form entry is the user's own edit to the wizard, said
// in plain words; it is shown as a quiet note, not a bubble.
const (
	BuildRoleAssistant = "assistant"
	BuildRoleUser      = "user"
	BuildRoleForm      = "form"
)

// BuildChoice is one chip. Its id is always server-generated.
type BuildChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// BuildTranscriptEntry is one line of the conversation. Fixed marks a line the
// host wrote from fixed copy (a model failure, a substituted question); fixed
// lines are shown but never sent to the model as something it said.
type BuildTranscriptEntry struct {
	Role    string        `json:"role"`
	Text    string        `json:"text"`
	Choices []BuildChoice `json:"choices,omitempty"`
	Chosen  string        `json:"chosen,omitempty"`
	Fixed   bool          `json:"fixed,omitempty"`
	At      time.Time     `json:"at"`
}

// BuildQuestion is the question the assistant is waiting on, if any.
type BuildQuestion struct {
	Question      string        `json:"question"`
	Choices       []BuildChoice `json:"choices,omitempty"`
	AllowFreeText bool          `json:"allow_free_text"`
}

// BuildWhy is one decision line: why a section of the workspace is the way
// the assistant set it. The final set becomes the workspace's "How this was
// set up".
type BuildWhy struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

// BuildAlternative is a blueprint the assistant considered instead.
type BuildAlternative struct {
	BlueprintID string `json:"blueprint_id"`
	Label       string `json:"label"`
	Reason      string `json:"reason"`
}

// BuildRejection is one field the host refused from the assistant's last
// proposal, and why. The next model call sees it.
type BuildRejection struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// BuildNeedsHome says the chosen blueprint lives inside a group it will set
// up at Create, through the existing group-requirement review.
type BuildNeedsHome struct {
	Label string `json:"label"`
}

// BuildRole fills one of the blueprint's roles, by creating an agent or by
// assigning one the user already has.
type BuildRole struct {
	RoleID       string `json:"role_id"`
	Mode         string `json:"mode"`
	AgentName    string `json:"agent_name"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
}

// BuildTeamPatch is the team the assistant proposed, validated, in the Team
// step's own vocabulary. The wizard applies it through the team draft's
// operations and reports the result back as team_state and the draft's team
// keys.
type BuildTeamPatch struct {
	Mode        string      `json:"mode,omitempty"`
	Roles       []BuildRole `json:"roles,omitempty"`
	SavedAgents []string    `json:"saved_agents,omitempty"`
}

// BuildRetry is the user turn a "Try again" replays after a model failure.
type BuildRetry struct {
	Text     string `json:"text,omitempty"`
	ChoiceID string `json:"choice_id,omitempty"`
}

// BuildAssistant is the face the pane shows: the hired assistant's.
type BuildAssistant struct {
	DisplayName string                 `json:"display_name"`
	Appearance  *types.AgentAppearance `json:"appearance,omitempty"`
}

// BuildDraft is the create request, and only the create request: the subset
// of POST /api/workspaces the wizard fills. Team keys stay raw because the
// wizard derives them and the create handler is their one validator.
type BuildDraft struct {
	TemplateID             string                     `json:"template_id,omitempty"`
	Blank                  bool                       `json:"blank,omitempty"`
	Name                   string                     `json:"name,omitempty"`
	Description            string                     `json:"description,omitempty"`
	BlueprintInputs        map[string]json.RawMessage `json:"blueprint_inputs,omitempty"`
	ParentID               string                     `json:"parent_id,omitempty"`
	Color                  string                     `json:"color,omitempty"`
	Tags                   []string                   `json:"tags,omitempty"`
	WorkspacePreset        string                     `json:"workspace_preset,omitempty"`
	WorkspaceBootstrap     json.RawMessage            `json:"workspace_bootstrap,omitempty"`
	ProjectConnection      json.RawMessage            `json:"project_connection,omitempty"`
	TeamIntent             json.RawMessage            `json:"team_intent,omitempty"`
	RoleStaffing           json.RawMessage            `json:"role_staffing,omitempty"`
	TemplateAgentOverrides json.RawMessage            `json:"template_agent_overrides,omitempty"`
	TemplateAgentReview    json.RawMessage            `json:"template_agent_review,omitempty"`
	ExistingAgentNames     []string                   `json:"existing_agent_names,omitempty"`
	EntryAgentName         string                     `json:"entry_agent_name,omitempty"`
	CreateTemplateAgents   *bool                      `json:"create_template_agents,omitempty"`
}

// HasBlueprint reports whether a blueprint (or Blank) has been chosen.
func (d BuildDraft) HasBlueprint() bool {
	return d.Blank || strings.TrimSpace(d.TemplateID) != ""
}

// WorkspaceBuildSession is one build (FR11).
type WorkspaceBuildSession struct {
	ID              string                 `json:"id"`
	Status          WorkspaceBuildStatus   `json:"status"`
	Version         int64                  `json:"version"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
	EntryPoint      string                 `json:"entry_point"`
	Assistant       BuildAssistant         `json:"assistant"`
	Draft           BuildDraft             `json:"draft"`
	TeamState       json.RawMessage        `json:"team_state,omitempty"`
	TeamPatch       *BuildTeamPatch        `json:"team_patch,omitempty"`
	Transcript      []BuildTranscriptEntry `json:"transcript"`
	PendingQuestion *BuildQuestion         `json:"pending_question"`
	Why             []BuildWhy             `json:"why,omitempty"`
	Alternatives    []BuildAlternative     `json:"alternatives,omitempty"`
	Rejections      []BuildRejection       `json:"rejections,omitempty"`
	NeedsHome       *BuildNeedsHome        `json:"needs_home"`
	FurthestStep    int                    `json:"furthest_step"`
	// Applied lists the draft fields the last assistant turn set, so the
	// wizard applies (and animates) only those.
	Applied   []string `json:"applied,omitempty"`
	Ready     bool     `json:"ready,omitempty"`
	CreateNow bool     `json:"create_now,omitempty"`
	// AskFolder offers the folder chip; the browser never sends a path.
	AskFolder bool        `json:"ask_folder,omitempty"`
	Retry     *BuildRetry `json:"retry,omitempty"`
	// TurnCount counts the user's own turns; FirstRequest is their first
	// description, both kept for the workspace's provenance.
	TurnCount          int    `json:"turn_count"`
	FirstRequest       string `json:"first_request,omitempty"`
	CreatedWorkspaceID string `json:"created_workspace_id,omitempty"`
}

// Touch records a write: a new version and a new update time.
func (s *WorkspaceBuildSession) Touch(now time.Time) {
	s.Version++
	s.UpdatedAt = now.UTC()
}

// Append adds a transcript entry and keeps the transcript within its bounds.
func (s *WorkspaceBuildSession) Append(entry BuildTranscriptEntry) {
	s.Transcript = append(s.Transcript, entry)
	s.TrimTranscript()
}

// TrimTranscript drops the oldest entries once the transcript holds more
// than WorkspaceBuildMaxEntries entries or WorkspaceBuildMaxTranscriptBytes
// of text.
func (s *WorkspaceBuildSession) TrimTranscript() {
	for len(s.Transcript) > WorkspaceBuildMaxEntries || transcriptBytes(s.Transcript) > WorkspaceBuildMaxTranscriptBytes {
		if len(s.Transcript) <= 1 {
			return
		}
		s.Transcript = s.Transcript[1:]
	}
}

func transcriptBytes(entries []BuildTranscriptEntry) int {
	total := 0
	for _, entry := range entries {
		total += len(entry.Text)
		for _, choice := range entry.Choices {
			total += len(choice.Label)
		}
	}
	return total
}

// Expired reports whether an open build has gone untouched past the expiry.
func (s *WorkspaceBuildSession) Expired(now time.Time) bool {
	return s.Status == WorkspaceBuildOpen && !s.UpdatedAt.IsZero() && now.Sub(s.UpdatedAt) > WorkspaceBuildExpiry
}

// WorkspaceBuildDocument is the sidecar's content.
type WorkspaceBuildDocument struct {
	SchemaVersion int                     `json:"schema_version"`
	Version       int64                   `json:"version"`
	Owner         KnowledgeOwner          `json:"owner"`
	Sessions      []WorkspaceBuildSession `json:"sessions,omitempty"`
	Present       bool                    `json:"-"`
}

// Open returns the one open build, if any.
func (d *WorkspaceBuildDocument) Open() *WorkspaceBuildSession {
	for i := range d.Sessions {
		if d.Sessions[i].Status == WorkspaceBuildOpen {
			return &d.Sessions[i]
		}
	}
	return nil
}

// Session finds a build by id.
func (d *WorkspaceBuildDocument) Session(id string) *WorkspaceBuildSession {
	id = strings.TrimSpace(id)
	for i := range d.Sessions {
		if id != "" && d.Sessions[i].ID == id {
			return &d.Sessions[i]
		}
	}
	return nil
}

// Add stores a new build and prunes the oldest settled ones past the cap.
func (d *WorkspaceBuildDocument) Add(session WorkspaceBuildSession) {
	d.Sessions = append(d.Sessions, session)
	for len(d.Sessions) > workspaceBuildMaxSessions {
		removed := false
		for i := range d.Sessions {
			if d.Sessions[i].Status != WorkspaceBuildOpen {
				d.Sessions = append(d.Sessions[:i], d.Sessions[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			return
		}
	}
}

// ExpireStale abandons every open build untouched past the expiry and
// reports whether anything changed.
func (d *WorkspaceBuildDocument) ExpireStale(now time.Time) bool {
	changed := false
	for i := range d.Sessions {
		if d.Sessions[i].Expired(now) {
			d.Sessions[i].Status = WorkspaceBuildAbandoned
			d.Sessions[i].Touch(now)
			changed = true
		}
	}
	return changed
}

// ErrWorkspaceBuildInvalid is returned when a document breaks its bounds.
var ErrWorkspaceBuildInvalid = errors.New("personal assistant: workspace build document invalid")

// ErrWorkspaceBuildOpen refuses a second open build (FR10).
var ErrWorkspaceBuildOpen = errors.New("personal assistant: a workspace build is already open")

func validateWorkspaceBuild(doc WorkspaceBuildDocument) error {
	if doc.SchemaVersion != WorkspaceBuildSchemaVersion {
		return fmt.Errorf("%w: schema", ErrWorkspaceBuildInvalid)
	}
	if len(doc.Sessions) > workspaceBuildMaxSessions {
		return fmt.Errorf("%w: sessions", ErrWorkspaceBuildInvalid)
	}
	open := 0
	seen := map[string]bool{}
	for _, session := range doc.Sessions {
		if session.ID == "" || len(session.ID) > 128 || seen[session.ID] {
			return fmt.Errorf("%w: session id", ErrWorkspaceBuildInvalid)
		}
		seen[session.ID] = true
		switch session.Status {
		case WorkspaceBuildOpen:
			open++
		case WorkspaceBuildCreated, WorkspaceBuildAbandoned:
		default:
			return fmt.Errorf("%w: status", ErrWorkspaceBuildInvalid)
		}
		if err := validateBuildSession(session); err != nil {
			return err
		}
	}
	if open > 1 {
		return ErrWorkspaceBuildOpen
	}
	return nil
}

func validateBuildSession(session WorkspaceBuildSession) error {
	if session.Version < 1 || session.FurthestStep < 0 || session.FurthestStep > 4 || session.TurnCount < 0 {
		return fmt.Errorf("%w: counters", ErrWorkspaceBuildInvalid)
	}
	if len(session.Transcript) > WorkspaceBuildMaxEntries || transcriptBytes(session.Transcript) > WorkspaceBuildMaxTranscriptBytes {
		return fmt.Errorf("%w: transcript", ErrWorkspaceBuildInvalid)
	}
	for _, entry := range session.Transcript {
		switch entry.Role {
		case BuildRoleAssistant, BuildRoleUser, BuildRoleForm:
		default:
			return fmt.Errorf("%w: transcript role", ErrWorkspaceBuildInvalid)
		}
		if !boundedText(entry.Text, WorkspaceBuildMaxText) || len(entry.Choices) > workspaceBuildMaxChoices {
			return fmt.Errorf("%w: transcript entry", ErrWorkspaceBuildInvalid)
		}
		if err := validateBuildChoices(entry.Choices); err != nil {
			return err
		}
	}
	if q := session.PendingQuestion; q != nil {
		if !boundedText(q.Question, WorkspaceBuildMaxSay) || len(q.Choices) > workspaceBuildMaxChoices {
			return fmt.Errorf("%w: question", ErrWorkspaceBuildInvalid)
		}
		if err := validateBuildChoices(q.Choices); err != nil {
			return err
		}
	}
	if len(session.Why) > workspaceBuildMaxWhy || len(session.Alternatives) > 2 || len(session.Rejections) > workspaceBuildMaxList ||
		len(session.Applied) > workspaceBuildMaxList {
		return fmt.Errorf("%w: lists", ErrWorkspaceBuildInvalid)
	}
	for _, why := range session.Why {
		if !boundedText(why.Section, 32) || !boundedText(why.Text, workspaceBuildMaxShort) {
			return fmt.Errorf("%w: why", ErrWorkspaceBuildInvalid)
		}
	}
	if !boundedText(session.Draft.Name, workspaceBuildMaxName) || !boundedText(session.Draft.Description, WorkspaceBuildMaxText) ||
		!boundedText(session.FirstRequest, WorkspaceBuildMaxFirstRequest) || !boundedText(session.EntryPoint, 64) ||
		!boundedText(session.Assistant.DisplayName, workspaceBuildMaxName) {
		return fmt.Errorf("%w: text", ErrWorkspaceBuildInvalid)
	}
	if team := session.TeamPatch; team != nil && (len(team.Roles) > workspaceBuildMaxList || len(team.SavedAgents) > workspaceBuildMaxList) {
		return fmt.Errorf("%w: team", ErrWorkspaceBuildInvalid)
	}
	return nil
}

func validateBuildChoices(choices []BuildChoice) error {
	for _, choice := range choices {
		if choice.ID == "" || !boundedText(choice.ID, 64) || !boundedText(choice.Label, 120) || choice.Label == "" {
			return fmt.Errorf("%w: choice", ErrWorkspaceBuildInvalid)
		}
	}
	return nil
}

func boundedText(value string, maxRunes int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes && !strings.ContainsRune(value, 0)
}
