package personalassistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/linkcontext"
	"github.com/johnjallday/ori-agent/internal/types"
)

// The workspace-build sidecar holds "Build with your assistant" sessions: the
// conversation in which the Personal Assistant fills the Create Workspace
// wizard. It lives beside the knowledge sidecar under <HQ>/.ori/ and is
// written with the same discipline (locked, atomic, bounded). A session's
// draft is exactly the create request the wizard submits; nothing here
// creates, installs, or attaches anything.
const (
	WorkspaceBuildSchemaVersion = 3
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
	// buildFolderMaxCount bounds a recognition's counts: well past any scan's
	// entry budget, so only a corrupt document reaches it.
	buildFolderMaxCount = 1_000_000
)

// WorkspaceBuildStatus is where a build is in its life.
type WorkspaceBuildStatus string

const (
	WorkspaceBuildOpen    WorkspaceBuildStatus = "open"
	WorkspaceBuildCreated WorkspaceBuildStatus = "created"
	// Preparing freezes the exact accepted draft against edits and late turns.
	WorkspaceBuildPreparing WorkspaceBuildStatus = "preparing"
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
// lines are shown but never sent to the model as something it said. Host marks
// a form line that states server facts (what a folder needs, what a setup
// made) rather than the user's own edit; the model hears it as Ori's note.
type BuildTranscriptEntry struct {
	Role    string        `json:"role"`
	Text    string        `json:"text"`
	Choices []BuildChoice `json:"choices,omitempty"`
	Chosen  string        `json:"chosen,omitempty"`
	Fixed   bool          `json:"fixed,omitempty"`
	Host    bool          `json:"host,omitempty"`
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

// BuildNeedsHome says the chosen blueprint is created inside a group. A group
// that does not exist yet is prepared first, through the existing
// group-requirement review and its own confirmation; Create then makes the
// workspace inside it.
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

// BuildFolder is the folder the user attached to the build from the pane's
// own Add folder: the same path-free observation the assistant's composer
// records (names, kinds, counts, markers; never contents, never a path). The
// pane shows it, the model reads it, and Create links the folder the
// observation names — through the server's own held selection, which this
// record cannot revive once it expires.
type BuildFolder struct {
	Observation foldercontext.Observation `json:"observation"`
	// Recognition is what the folder digest concluded about the same scan,
	// kept when the folder is attached. Nil when it could not be read.
	Recognition *BuildFolderRecognition `json:"recognition,omitempty"`
}

// What a build's attached folder was recognized as.
const (
	// BuildFolderProject is one project Ori knows the kind of.
	BuildFolderProject = "project"
	// BuildFolderCollection holds several projects, which Ori sets up together
	// from Home's Explore a folder, never as one workspace.
	BuildFolderCollection = "collection"
	// BuildFolderUnrecognized is everything else.
	BuildFolderUnrecognized = "none"
)

// BuildFolderRecognition is the folder digest's verdict on a build's attached
// folder and the capability row it matched, from the host's own table: no
// model is involved, and nothing here names a path. The pane says it, the
// model reads it as reference data, and the plan starts from it.
type BuildFolderRecognition struct {
	Verdict string `json:"verdict"`
	Shape   string `json:"shape,omitempty"`
	// ProjectCount is 1 for a project and the number of projects a
	// collection holds.
	ProjectCount int `json:"project_count"`
	// CandidateID is the recognized project's id in the observation's own
	// projects list, never a path.
	CandidateID string `json:"candidate_id,omitempty"`
	// Label names the kind of project ("<integration> project"). MarkerNoun and
	// MarkerCount count the files that say so ("session", 3); a marker that is
	// a folder, or none, has no count.
	Label       string `json:"label,omitempty"`
	MarkerNoun  string `json:"marker_noun,omitempty"`
	MarkerCount int    `json:"marker_count,omitempty"`
	// BlueprintID is the project blueprint the shape suggests, by the host
	// table's own id ("<blueprint>"): a catalog entry is that blueprint when
	// its id or its plugin's blueprint id is this one. BlueprintLabel names it.
	BlueprintID    string `json:"blueprint_id,omitempty"`
	BlueprintLabel string `json:"blueprint_label,omitempty"`
	// The reviewed integration and Home provider the capability row names.
	IntegrationKey   string `json:"integration_key,omitempty"`
	IntegrationName  string `json:"integration_name,omitempty"`
	HomeProviderKey  string `json:"home_provider_key,omitempty"`
	HomeProviderName string `json:"home_provider_name,omitempty"`
	HomeName         string `json:"home_name,omitempty"`
	// HomeExists says the owner already had that Home when the folder was
	// attached. The plan re-reads it; this is what the model is told.
	HomeExists bool `json:"home_exists,omitempty"`
	// OfferID is the folder digest offer the setup runs on, once there is one.
	OfferID string `json:"offer_id,omitempty"`
	// NeedsSaid is the Needs line the host last wrote into the transcript, so
	// the same states are never said twice.
	NeedsSaid string `json:"needs_said,omitempty"`
}

// Recognized reports a single project of a kind Ori knows.
func (r *BuildFolderRecognition) Recognized() bool {
	return r != nil && r.Verdict == BuildFolderProject
}

func validateBuildFolderRecognition(r *BuildFolderRecognition, observation foldercontext.Observation) error {
	if r == nil {
		return nil
	}
	switch r.Verdict {
	case BuildFolderProject:
		if r.ProjectCount != 1 || r.CandidateID == "" || r.Label == "" {
			return fmt.Errorf("%w: folder recognition", ErrWorkspaceBuildInvalid)
		}
	case BuildFolderCollection:
		if r.ProjectCount < 2 || r.CandidateID != "" || r.OfferID != "" {
			return fmt.Errorf("%w: folder recognition", ErrWorkspaceBuildInvalid)
		}
	case BuildFolderUnrecognized:
		if *r != (BuildFolderRecognition{Verdict: BuildFolderUnrecognized}) {
			return fmt.Errorf("%w: folder recognition", ErrWorkspaceBuildInvalid)
		}
	default:
		return fmt.Errorf("%w: folder recognition", ErrWorkspaceBuildInvalid)
	}
	if r.ProjectCount > buildFolderMaxCount || r.MarkerCount < 0 || r.MarkerCount > buildFolderMaxCount {
		return fmt.Errorf("%w: folder recognition", ErrWorkspaceBuildInvalid)
	}
	// A recognized project is one the observation disclosed.
	if r.CandidateID != "" {
		disclosed := false
		for _, project := range observation.Projects {
			disclosed = disclosed || project.ID == r.CandidateID
		}
		if !disclosed {
			return fmt.Errorf("%w: folder recognition candidate", ErrWorkspaceBuildInvalid)
		}
	}
	for _, value := range []string{r.Shape, r.CandidateID, r.BlueprintID, r.IntegrationKey, r.HomeProviderKey, r.OfferID} {
		if !boundedText(value, 128) || strings.ContainsAny(value, "/\\\r\n") {
			return fmt.Errorf("%w: folder recognition", ErrWorkspaceBuildInvalid)
		}
	}
	for _, value := range []string{r.Label, r.MarkerNoun, r.BlueprintLabel, r.IntegrationName, r.HomeProviderName, r.HomeName} {
		if !boundedText(value, workspaceBuildMaxName) || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%w: folder recognition", ErrWorkspaceBuildInvalid)
		}
	}
	if !boundedText(r.NeedsSaid, workspaceBuildMaxShort) || strings.ContainsAny(r.NeedsSaid, "\r\n") {
		return fmt.Errorf("%w: folder recognition", ErrWorkspaceBuildInvalid)
	}
	return nil
}

// BuildLink is the link the user attached to the build from the pane's own
// Add link: what the page or repository says about itself (title,
// description, host, kind, a repository's public facts), never what it
// contains. The observation is self-contained: Create needs nothing the
// server holds to act on it.
type BuildLink struct {
	Observation linkcontext.Observation `json:"observation"`
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
	TemplateID  string `json:"template_id,omitempty"`
	Blank       bool   `json:"blank,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// Mission is the workspace goal Create sends: the form's Goal field.
	Mission                string                     `json:"mission,omitempty"`
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
	// Folder is the folder the user attached in the pane, if any.
	Folder *BuildFolder `json:"folder,omitempty"`
	// Link is the link the user attached in the pane, if any. One at a time:
	// attaching another replaces it.
	Link *BuildLink `json:"link,omitempty"`
	// TurnCount counts the user's own turns; FirstRequest is their first
	// description, both kept for the workspace's provenance.
	TurnCount          int                    `json:"turn_count"`
	FirstRequest       string                 `json:"first_request,omitempty"`
	CreatedWorkspaceID string                 `json:"created_workspace_id,omitempty"`
	Preparation        *BuildPreparationClaim `json:"preparation,omitempty"`
	// Setup is the folder setup run Set it up started for a recognized folder:
	// it holds the build in preparing while it runs, and stays while the build
	// is open again after a stop, so Retry continues the same run.
	Setup *BuildSetupClaim `json:"setup,omitempty"`
	// HomeWorkspaceID is the Home a setup run made or joined, beside the
	// project workspace in CreatedWorkspaceID; SetupReceipt is what the run
	// made, in the host's words. Both outlive Settle.
	HomeWorkspaceID string `json:"home_workspace_id,omitempty"`
	SetupReceipt    string `json:"setup_receipt,omitempty"`
}

// BuildSetupClaim is the setup run a build's Set it up started: the folder
// digest offer it runs on, the plan digest the user confirmed, and the request
// that started it.
type BuildSetupClaim struct {
	OfferID    string    `json:"offer_id"`
	PlanDigest string    `json:"plan_digest"`
	RequestID  string    `json:"request_id"`
	StartedAt  time.Time `json:"started_at"`
	// StopReason is why the run last stopped (FolderStop*), while the build is
	// open again; empty while it runs.
	StopReason string `json:"stop_reason,omitempty"`
}

func (c BuildSetupClaim) validate() error {
	for _, value := range []string{c.OfferID, c.RequestID} {
		if strings.TrimSpace(value) == "" || !boundedText(value, 128) || strings.ContainsAny(value, "/\\\r\n") {
			return fmt.Errorf("%w: setup claim", ErrWorkspaceBuildInvalid)
		}
	}
	if !buildPreparationDigest(c.PlanDigest) || c.StartedAt.IsZero() || !boundedText(c.StopReason, 64) || strings.ContainsAny(c.StopReason, "/\\\r\n") {
		return fmt.Errorf("%w: setup claim", ErrWorkspaceBuildInvalid)
	}
	return nil
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

// Settle drops what only an open build needs — the conversation, the form's
// team state, the pending question — once it is created or abandoned. A
// settled build keeps its id, status, and counters, so the file stays small
// however many builds a user finishes.
func (s *WorkspaceBuildSession) Settle() {
	s.Transcript = []BuildTranscriptEntry{}
	s.TeamState = nil
	s.TeamPatch = nil
	s.PendingQuestion = nil
	s.Why = nil
	s.Alternatives = nil
	s.Rejections = nil
	s.Applied = nil
	s.Retry = nil
	s.Folder = nil
	s.Link = nil
	s.Setup = nil
	s.Draft = BuildDraft{Name: s.Draft.Name}
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
		if d.Sessions[i].Status == WorkspaceBuildOpen || d.Sessions[i].Status == WorkspaceBuildPreparing {
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
			if d.Sessions[i].Status != WorkspaceBuildOpen && d.Sessions[i].Status != WorkspaceBuildPreparing {
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
			d.Sessions[i].Settle()
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
		case WorkspaceBuildOpen, WorkspaceBuildPreparing:
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
	if session.Preparation != nil && session.Setup != nil {
		return fmt.Errorf("%w: preparation and setup", ErrWorkspaceBuildInvalid)
	}
	if session.Preparation != nil {
		if err := session.Preparation.Validate(); err != nil {
			return err
		}
		if session.Status != WorkspaceBuildPreparing && session.Status != WorkspaceBuildCreated && session.Status != WorkspaceBuildAbandoned {
			return fmt.Errorf("%w: preparation lifecycle", ErrWorkspaceBuildInvalid)
		}
	} else if session.Setup != nil {
		// A setup run belongs to an open or preparing build; Settle drops it.
		if err := session.Setup.validate(); err != nil {
			return err
		}
		if session.Status != WorkspaceBuildPreparing && session.Status != WorkspaceBuildOpen {
			return fmt.Errorf("%w: setup lifecycle", ErrWorkspaceBuildInvalid)
		}
		if session.Status == WorkspaceBuildPreparing && session.Setup.StopReason != "" {
			return fmt.Errorf("%w: setup lifecycle", ErrWorkspaceBuildInvalid)
		}
	} else if session.Status == WorkspaceBuildPreparing {
		return fmt.Errorf("%w: missing preparation", ErrWorkspaceBuildInvalid)
	}
	if !boundedText(session.SetupReceipt, workspaceBuildMaxShort) || !boundedText(session.HomeWorkspaceID, 128) ||
		strings.ContainsAny(session.HomeWorkspaceID, "/\\\r\n") {
		return fmt.Errorf("%w: setup receipt", ErrWorkspaceBuildInvalid)
	}
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
		if !boundedText(entry.Text, WorkspaceBuildMaxText) || len(entry.Choices) > workspaceBuildMaxChoices ||
			(entry.Host && entry.Role != BuildRoleForm) {
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
	// An attached folder is held to the observation's own bounds: path-free,
	// at most 8 KiB, names of at most 96 runes.
	if folder := session.Folder; folder != nil {
		if folder.Observation.Validate() != nil {
			return fmt.Errorf("%w: folder", ErrWorkspaceBuildInvalid)
		}
		if err := validateBuildFolderRecognition(folder.Recognition, folder.Observation); err != nil {
			return err
		}
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
