// Package workspaceplan implements the app-owned Workspace Planning Workflow.
//
// A Plan is the durable, versioned planning record for one workspace. It moves
// from clarification and drafting through immutable review, explicit user
// approval, Task materialization, execution, and completion.
//
// Two boundaries define this package:
//
//   - Compiled Ori code owns lifecycle transitions, approval validity, Task
//     materialization, execution gates, and audit records. A model may propose
//     Plan content and nothing else (PRD FR-59, FR-60, FR-82).
//   - Workspace Tasks remain the canonical unit of executable work and
//     Workspace Runs remain the canonical execution attempt. A Plan links to
//     them and derives a summary from them; it never keeps a competing copy of
//     their state (PRD FR-11, FR-12).
package workspaceplan

import (
	"time"

	"github.com/google/uuid"
)

// Plan is the durable planning aggregate owned by exactly one workspace
// (FR-1, FR-2). Its ID survives every revision, approval, materialization,
// execution retry, and completion (FR-3).
//
// The record deliberately carries no Task execution state, Run trace, or Run
// artifact as a canonical field. Everything about what actually happened lives
// on the linked Task and Run records (FR-11); Progress is derived from them on
// read (FR-12).
type Plan struct {
	ID string `json:"id"`
	// WorkspaceID is the owning workspace. It is serialized as studio_id
	// because that is the backend/API identifier for the workspace shown in
	// the UI (FR-2, FR-163).
	WorkspaceID string `json:"studio_id"`

	// Title is the record-level display name. It tracks the working draft, so
	// editing a draft's title renames the Plan while every immutable version
	// keeps the title it was reviewed under.
	Title string `json:"title"`
	// OriginalRequest is the exact text that initiated this Plan, retained
	// verbatim and separately from any model-produced summary (FR-21). Nothing
	// in the drafting or regeneration path may rewrite it.
	OriginalRequest string `json:"original_request"`
	// Objective is the working draft's objective statement (FR-4). Immutable
	// versions snapshot their own copy.
	Objective string `json:"objective"`

	// Status is the lifecycle state. It is only ever changed through the
	// server-side transition table in status.go (FR-13, FR-14).
	Status Status `json:"status"`

	// Draft is the mutable working content: the not-yet-approved Plan body
	// currently being prepared.
	Draft PlanContent `json:"draft"`
	// DraftRevision is the optimistic-concurrency token for Draft. Every
	// accepted draft write bumps it, and a write carrying a stale value is
	// refused rather than silently overwriting a concurrent editor (FR-30).
	DraftRevision int64 `json:"draft_revision"`
	// DraftIntent classifies a draft derived from already-approved work as
	// additive, corrective, or superseding (FR-39). It is empty while the Plan
	// has never been approved.
	DraftIntent RevisionIntent `json:"draft_intent,omitempty"`

	// CurrentVersion is the number of the latest immutable review version, or
	// 0 before the first review snapshot exists.
	CurrentVersion int `json:"current_version"`
	// ApprovedVersion is the number of the latest approved version, or 0 when
	// no version has been approved. Editing after approval creates a new draft
	// and never mutates the approved version in place (FR-38).
	ApprovedVersion int `json:"approved_version"`

	// SupersededByPlanID and SupersedesPlanID record an explicit replacement
	// relationship between Plans. Both directions are kept so the superseded
	// record stays reachable rather than hidden (FR-13 "superseded").
	SupersededByPlanID string `json:"superseded_by_plan_id,omitempty"`
	SupersedesPlanID   string `json:"supersedes_plan_id,omitempty"`

	// Origin records who or what created this Plan (FR-4).
	Origin Origin `json:"origin"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// LastActivityAt drives retention: an inactive draft or needs_input Plan
	// moves to History after 30 days without activity (FR-16).
	LastActivityAt time.Time `json:"last_activity_at"`
	// ArchivedAt marks a Plan moved to History. Archiving never deletes
	// versions, approvals, Tasks, Runs, or artifacts (FR-16, FR-17).
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// ArchiveReason explains why the Plan is in History (for example
	// "cancelled" or "inactive_30d").
	ArchiveReason string `json:"archive_reason,omitempty"`

	// TaskLinks and RunLinks are provenance rows, not execution state: they
	// say which approved Plan item produced which Task or Run, and nothing
	// about how that work is going (FR-9, FR-10, FR-11).
	TaskLinks []TaskLink `json:"task_links,omitempty"`
	RunLinks  []RunLink  `json:"run_links,omitempty"`

	// Progress is derived from the linked Tasks and Runs when a Plan is read.
	// It is never persisted and never authoritative (FR-12).
	Progress *Progress `json:"progress,omitempty"`
}

// PlanContent is the typed Plan body (FR-5). Actionable structure lives in
// typed fields; free prose lives only in Explanation, so dependency and
// approval semantics can never be defined by Markdown (FR-40, FR-53).
type PlanContent struct {
	// Rationale explains why this approach was proposed.
	Rationale string `json:"rationale,omitempty"`
	// Assumptions records what the Plan takes as given, including assumptions
	// created by skipping an optional clarification question (FR-28).
	Assumptions []Assumption `json:"assumptions,omitempty"`
	// InScope lists the outcomes this Plan commits to delivering.
	InScope []string `json:"in_scope,omitempty"`
	// NonGoals lists what this Plan explicitly will not do.
	NonGoals []string `json:"non_goals,omitempty"`
	Risks    []Risk   `json:"risks,omitempty"`
	// Clarifications are the structured questions asked about this request and
	// the answers the user authored (FR-23, FR-24).
	Clarifications []Clarification `json:"clarifications,omitempty"`
	// Sources are references the Plan was built from. Research workspaces use
	// them for source tracking (FR-132).
	Sources []Source `json:"sources,omitempty"`
	// Artifacts are the documents approval would authorize writing (FR-95).
	Artifacts []ProposedArtifact `json:"artifacts,omitempty"`
	// Groups are the proposed task groups, in order.
	Groups []TaskGroup `json:"groups,omitempty"`
	// Validations are the checkpoints that must pass before the Plan may
	// complete (FR-119).
	Validations []ValidationCheckpoint `json:"validations,omitempty"`
	// Execution is the proposed execution policy for this Plan.
	Execution ExecutionPolicy `json:"execution"`
	// Explanation is optional Markdown shown alongside the structure. It is
	// presentation only: no dependency, assignment, or approval meaning may be
	// read out of it (FR-53).
	Explanation string `json:"explanation,omitempty"`
}

// Assumption is a stated premise of the Plan.
type Assumption struct {
	ID        string     `json:"id"`
	Statement string     `json:"statement"`
	Author    AuthorKind `json:"author,omitempty"`
	// ClarificationID is set when this assumption was recorded because the
	// user skipped that clarification question (FR-28).
	ClarificationID string `json:"clarification_id,omitempty"`
}

// Risk is a known way the Plan can go wrong.
type Risk struct {
	ID         string       `json:"id"`
	Statement  string       `json:"statement"`
	Severity   RiskSeverity `json:"severity,omitempty"`
	Mitigation string       `json:"mitigation,omitempty"`
	Author     AuthorKind   `json:"author,omitempty"`
}

// RiskSeverity ranks a risk. Presentation must pair it with text or an icon
// rather than relying on color alone (FR-162).
type RiskSeverity string

const (
	RiskLow    RiskSeverity = "low"
	RiskMedium RiskSeverity = "medium"
	RiskHigh   RiskSeverity = "high"
)

// Clarification is one structured planner question and its user answer
// (FR-23, FR-24).
type Clarification struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Detail string `json:"detail,omitempty"`
	// Options are optional suggested choices. They are a drafting aid; a user
	// may always answer with free text.
	Options []string `json:"options,omitempty"`
	// Required questions block the return to draft until they are answered.
	// Optional questions may be skipped, which records an assumption (FR-28).
	Required bool                `json:"required"`
	Status   ClarificationStatus `json:"status"`
	// Round groups questions asked together so the configured maximum number
	// of questions per round can be enforced by the application (FR-27).
	Round int `json:"round,omitempty"`
	// Answer is user-authored input. Regeneration may read it but must never
	// overwrite or summarize over it (FR-25).
	Answer     string     `json:"answer,omitempty"`
	AnsweredBy string     `json:"answered_by,omitempty"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
	SkipReason string     `json:"skip_reason,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ClarificationStatus is the answer state of one question.
type ClarificationStatus string

const (
	ClarificationOpen     ClarificationStatus = "open"
	ClarificationAnswered ClarificationStatus = "answered"
	ClarificationSkipped  ClarificationStatus = "skipped"
)

// Source is a reference the Plan draws on.
type Source struct {
	ID   string     `json:"id"`
	Kind SourceKind `json:"kind"`
	// Ref is the URL, workspace-relative path, or record ID. It is untrusted
	// input and must be validated at whichever boundary consumes it, never
	// here and never on the model's word (FR-169).
	Ref     string     `json:"ref"`
	Title   string     `json:"title,omitempty"`
	Excerpt string     `json:"excerpt,omitempty"`
	Author  AuthorKind `json:"author,omitempty"`
	AddedAt time.Time  `json:"added_at,omitzero"`
}

// SourceKind classifies where a source reference points.
type SourceKind string

const (
	SourceURL  SourceKind = "url"
	SourceFile SourceKind = "file"
	SourceNote SourceKind = "note"
	SourceTask SourceKind = "task"
	SourceRun  SourceKind = "run"
	SourceText SourceKind = "text"
)

// ProposedArtifact is a document approval would authorize writing (FR-95,
// FR-98). Nothing is written until an approval that listed it is consumed.
type ProposedArtifact struct {
	ID    string       `json:"id"`
	Kind  ArtifactKind `json:"kind"`
	Title string       `json:"title,omitempty"`
	// Path is workspace-relative. It is normalized and rejected if it escapes
	// the workspace root at write time, not at authoring time (FR-97).
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	// Enabled reflects whether the effective enforced policy will actually
	// render this artifact. A disabled artifact stays visible in the Plan so
	// the approval summary can say plainly that it will not be written.
	Enabled bool `json:"enabled"`
}

// ArtifactKind names a planning artifact Ori can render deterministically from
// typed Plan content (FR-96).
type ArtifactKind string

const (
	ArtifactPRD      ArtifactKind = "prd"
	ArtifactTaskList ArtifactKind = "task_list"
	ArtifactNote     ArtifactKind = "note"
	ArtifactDocument ArtifactKind = "document"
)

// TaskGroup is a proposed group of work with a stable Plan-local ID (FR-6).
type TaskGroup struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Outcome states what completing this group achieves.
	Outcome string `json:"outcome,omitempty"`
	// Items are the actionable task items, in order.
	Items []TaskItem `json:"items,omitempty"`
	// DependsOn lists other group IDs that must finish first. Entries are
	// Plan-local group IDs, never titles or array positions (FR-8).
	DependsOn []string   `json:"depends_on,omitempty"`
	Notes     string     `json:"notes,omitempty"`
	Author    AuthorKind `json:"author,omitempty"`
}

// TaskItem is one actionable unit of proposed work with a stable Plan-local ID
// (FR-7). It becomes exactly one Workspace Task when an approval that covered
// it is consumed.
type TaskItem struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Details     string `json:"details,omitempty"`
	// Assignee is an agent name. Empty means unassigned, which materializes an
	// unassigned Task rather than silently picking an agent (FR-86).
	Assignee string `json:"assignee,omitempty"`
	// AssigneeNodeID pins a specific agent instance when several share a name.
	AssigneeNodeID string `json:"assignee_node_id,omitempty"`
	// RequiredCapabilities lists abstract capability keys that must be
	// available before this item may execute. They are revalidated immediately
	// before Tasks are written (FR-85).
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	// DependsOn lists Plan-local item IDs that must satisfy their success
	// condition first (FR-8, FR-104).
	DependsOn []string `json:"depends_on,omitempty"`
	// ExpectedResult states how a reviewer will know this item succeeded.
	ExpectedResult string `json:"expected_result,omitempty"`
	Priority       int    `json:"priority,omitempty"`
	ReferenceURL   string `json:"reference_url,omitempty"`
	// Author distinguishes user-authored edits from model-generated content in
	// version provenance (FR-57).
	Author AuthorKind `json:"author,omitempty"`
}

// ValidationCheckpoint is a required or advisory check on the Plan's work
// (FR-119).
type ValidationCheckpoint struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// AppliesTo lists the group or item IDs this checkpoint gates. Empty means
	// the checkpoint applies to the whole Plan.
	AppliesTo   []string   `json:"applies_to,omitempty"`
	Expectation string     `json:"expectation,omitempty"`
	Required    bool       `json:"required"`
	Author      AuthorKind `json:"author,omitempty"`
}

// ExecutionPolicy is the proposed execution behavior for an approved Plan. It
// is approval-relevant: changing it invalidates an outstanding approval view
// (FR-33, FR-68).
type ExecutionPolicy struct {
	Mode ExecutionMode `json:"mode"`
	// Preconditions names the compiled enforcement adapters that must pass
	// before code-oriented execution begins (for example repository inspection
	// or a safe branch). The names are adapter keys resolved by the policy
	// registry, never free text the model invented (FR-47, FR-126).
	Preconditions []string `json:"preconditions,omitempty"`
}

// ExecutionMode is the supported initial execution behavior after approval
// (FR-101).
type ExecutionMode string

const (
	// ExecutionStepThrough creates Tasks on approval but starts nothing until
	// the user explicitly starts work (FR-102).
	ExecutionStepThrough ExecutionMode = "step_through"
	// ExecutionAuto authorizes automatic dispatch of eligible Tasks after a
	// successful materialization, and only through Approve and Start (FR-103).
	ExecutionAuto ExecutionMode = "auto"
)

// RevisionIntent classifies a draft derived from already-approved work so
// reconciliation can be previewed correctly (FR-39, FR-76, FR-77).
type RevisionIntent string

const (
	// RevisionAdditive retains every prior Task and materializes only newly
	// approved work (FR-76).
	RevisionAdditive RevisionIntent = "additive"
	// RevisionCorrective may cancel and replace eligible unstarted Tasks after
	// a previewed, separately confirmed reconciliation (FR-77).
	RevisionCorrective RevisionIntent = "corrective"
	// RevisionSuperseding replaces the Plan's direction entirely.
	RevisionSuperseding RevisionIntent = "superseding"
)

// AuthorKind marks whether Plan content was written by a user or produced by a
// model, so version provenance can show the difference (FR-57).
type AuthorKind string

const (
	AuthorUser  AuthorKind = "user"
	AuthorModel AuthorKind = "model"
	AuthorApp   AuthorKind = "app"
)

// Origin records what created a Plan record (FR-4). It is provenance for
// display and audit; it never confers approval authority (FR-60).
type Origin struct {
	Kind OriginKind `json:"kind"`
	// Actor is the human or system principal, and ActorID its stable ID when
	// one exists.
	Actor   string `json:"actor,omitempty"`
	ActorID string `json:"actor_id,omitempty"`
	// SessionID and MessageID link back to the conversation a Plan was started
	// from, when it was started from chat.
	SessionID string `json:"session_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	// AgentName records which agent recommended the Plan. A recommendation is
	// not an approval and creates no Tasks by itself (FR-20).
	AgentName string `json:"agent_name,omitempty"`
}

// OriginKind names the surface a Plan was created from.
type OriginKind string

const (
	OriginUser          OriginKind = "user"
	OriginChat          OriginKind = "chat"
	OriginOrchestration OriginKind = "orchestration"
	OriginAPI           OriginKind = "api"
)

// Provenance is the structured record stamped onto every materialized Task and
// every Plan-linked Run. Storing it on both sides is what makes the Plan link
// bidirectional without a join being the only path back (FR-10, FR-88).
type Provenance struct {
	PlanID      string `json:"plan_id"`
	WorkspaceID string `json:"studio_id"`
	// Version is the approved Plan version that authorized this record.
	Version int `json:"plan_version"`
	// ApprovalID is the approval record that was consumed to create it.
	ApprovalID string `json:"approval_id"`
	GroupID    string `json:"group_id,omitempty"`
	// ItemID is empty for a group-level parent Task, which represents the
	// group rather than any single item.
	ItemID string   `json:"item_id,omitempty"`
	Role   LinkRole `json:"role,omitempty"`
	// ApprovedBy names the user whose approval authorized the work, so Task
	// assignment provenance can state where the assignment came from (FR-87).
	ApprovedBy string    `json:"approved_by,omitempty"`
	ApprovedAt time.Time `json:"approved_at"`
}

// LinkRole says what a linked Task represents within the Plan.
type LinkRole string

const (
	// LinkRoleGroup is the parent Task standing for a whole task group.
	LinkRoleGroup LinkRole = "group"
	// LinkRoleItem is the Task for one actionable Plan item.
	LinkRoleItem LinkRole = "item"
	// LinkRoleFollowUp is corrective work created for a Task that had already
	// started or completed and therefore stays immutable (FR-78).
	LinkRoleFollowUp LinkRole = "follow_up"
)

// TaskLink is the Plan-side half of the Task provenance pair (FR-9, FR-10).
// It records which approved item produced which Task and never mirrors that
// Task's execution status (FR-11).
type TaskLink struct {
	PlanID      string `json:"plan_id"`
	WorkspaceID string `json:"studio_id"`
	Version     int    `json:"plan_version"`
	ApprovalID  string `json:"approval_id"`
	GroupID     string `json:"group_id,omitempty"`
	ItemID      string `json:"item_id,omitempty"`
	// TaskID is the existing Workspace Task record, which remains the
	// authoritative source for everything about that work.
	TaskID    string    `json:"task_id"`
	Role      LinkRole  `json:"role"`
	CreatedAt time.Time `json:"created_at"`

	// ReplacedByTaskID, RetiredAt, and RetiredReason are reconciliation
	// bookkeeping for a corrective or superseding revision. Retiring a link
	// records that a later approved version replaced this work; it never
	// deletes the Task, and it is never set for a Task that already started
	// (FR-77, FR-78).
	ReplacedByTaskID string     `json:"replaced_by_task_id,omitempty"`
	RetiredAt        *time.Time `json:"retired_at,omitempty"`
	RetiredReason    string     `json:"retired_reason,omitempty"`
}

// RunLink is the Plan-side half of the Run provenance pair (FR-9, FR-10). Run
// status, traces, validation, artifacts, and results stay authoritative on the
// Run record (FR-11, FR-100).
type RunLink struct {
	PlanID      string    `json:"plan_id"`
	WorkspaceID string    `json:"studio_id"`
	Version     int       `json:"plan_version"`
	GroupID     string    `json:"group_id,omitempty"`
	ItemID      string    `json:"item_id,omitempty"`
	TaskID      string    `json:"task_id,omitempty"`
	RunID       string    `json:"run_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// Progress is the aggregated read model for a Plan, computed from the linked
// Tasks and Runs each time a Plan is read. It is deliberately not persisted:
// the Task and Run records are the only authoritative source (FR-12, FR-107).
type Progress struct {
	// Total counts linked actionable Tasks.
	Total int `json:"total"`
	// Completed, Running, Ready, Blocked, and Failed are derived from linked
	// Task and Run state.
	Completed int `json:"completed"`
	Running   int `json:"running"`
	Ready     int `json:"ready"`
	Blocked   int `json:"blocked"`
	Failed    int `json:"failed"`
	// Remaining counts work with no terminal outcome yet.
	Remaining int `json:"remaining"`
	// WaitingForSlot is set when this Plan is approved for automatic execution
	// but another Plan holds the workspace execution slot (FR-107).
	WaitingForSlot int `json:"waiting_for_slot"`
}

// ID prefixes. Plan-local IDs are stable for the lifetime of the Plan:
// reordering, regenerating a neighbouring section, or editing text never
// changes them (FR-52, FR-55).
const (
	planIDPrefix          = "plan_"
	groupIDPrefix         = "grp_"
	itemIDPrefix          = "itm_"
	clarificationIDPrefix = "clr_"
	assumptionIDPrefix    = "asm_"
	riskIDPrefix          = "rsk_"
	sourceIDPrefix        = "src_"
	artifactIDPrefix      = "art_"
	validationIDPrefix    = "val_"
)

func newUUID() string { return uuid.NewString() }

// NewPlanID returns a new globally unique Plan ID.
func NewPlanID() string { return planIDPrefix + newUUID() }

// NewGroupID returns a new stable Plan-local task group ID.
func NewGroupID() string { return groupIDPrefix + newUUID() }

// NewItemID returns a new stable Plan-local task item ID.
func NewItemID() string { return itemIDPrefix + newUUID() }

// NewClarificationID returns a new stable clarification question ID (FR-24).
func NewClarificationID() string { return clarificationIDPrefix + newUUID() }

// NewAssumptionID returns a new stable assumption ID.
func NewAssumptionID() string { return assumptionIDPrefix + newUUID() }

// NewRiskID returns a new stable risk ID.
func NewRiskID() string { return riskIDPrefix + newUUID() }

// NewSourceID returns a new stable source reference ID.
func NewSourceID() string { return sourceIDPrefix + newUUID() }

// NewArtifactID returns a new stable proposed-artifact ID.
func NewArtifactID() string { return artifactIDPrefix + newUUID() }

// NewValidationID returns a new stable validation checkpoint ID.
func NewValidationID() string { return validationIDPrefix + newUUID() }

// Group returns the task group with the given Plan-local ID.
func (c PlanContent) Group(id string) (TaskGroup, bool) {
	for _, group := range c.Groups {
		if group.ID == id {
			return group, true
		}
	}
	return TaskGroup{}, false
}

// Item returns the task item with the given Plan-local ID, along with the
// group that owns it.
func (c PlanContent) Item(id string) (TaskItem, TaskGroup, bool) {
	for _, group := range c.Groups {
		for _, item := range group.Items {
			if item.ID == id {
				return item, group, true
			}
		}
	}
	return TaskItem{}, TaskGroup{}, false
}

// Clarification returns the clarification question with the given ID.
func (c PlanContent) Clarification(id string) (Clarification, bool) {
	for _, question := range c.Clarifications {
		if question.ID == id {
			return question, true
		}
	}
	return Clarification{}, false
}

// ActionableItemCount counts the task items across every group. This is the
// count the 200-item bound applies to (FR-42).
func (c PlanContent) ActionableItemCount() int {
	total := 0
	for _, group := range c.Groups {
		total += len(group.Items)
	}
	return total
}

// EachItem calls fn for every task item in Plan order. Returning false stops
// the walk.
func (c PlanContent) EachItem(fn func(group TaskGroup, item TaskItem) bool) {
	for _, group := range c.Groups {
		for _, item := range group.Items {
			if !fn(group, item) {
				return
			}
		}
	}
}

// UnansweredRequired returns the required clarification questions that are
// still open. A Plan may not leave needs_input while any remain (FR-26).
func (c PlanContent) UnansweredRequired() []Clarification {
	var open []Clarification
	for _, question := range c.Clarifications {
		if question.Required && question.Status == ClarificationOpen {
			open = append(open, question)
		}
	}
	return open
}

// EnabledArtifacts returns the artifacts the approved policy would actually
// write, in Plan order.
func (c PlanContent) EnabledArtifacts() []ProposedArtifact {
	var enabled []ProposedArtifact
	for _, artifact := range c.Artifacts {
		if artifact.Enabled {
			enabled = append(enabled, artifact)
		}
	}
	return enabled
}

// Clone returns a deep copy. Stores hand out clones so a caller mutating what
// it received can never reach into persisted state.
func (p *Plan) Clone() *Plan {
	if p == nil {
		return nil
	}
	out := *p
	out.Draft = p.Draft.Clone()
	out.TaskLinks = cloneTaskLinks(p.TaskLinks)
	out.RunLinks = cloneRunLinks(p.RunLinks)
	if p.ArchivedAt != nil {
		archivedAt := *p.ArchivedAt
		out.ArchivedAt = &archivedAt
	}
	if p.Progress != nil {
		progress := *p.Progress
		out.Progress = &progress
	}
	return &out
}

// Clone returns a deep copy of the Plan body.
func (c PlanContent) Clone() PlanContent {
	out := c
	out.Assumptions = append([]Assumption(nil), c.Assumptions...)
	out.InScope = cloneStrings(c.InScope)
	out.NonGoals = cloneStrings(c.NonGoals)
	out.Risks = append([]Risk(nil), c.Risks...)
	out.Sources = append([]Source(nil), c.Sources...)
	out.Artifacts = append([]ProposedArtifact(nil), c.Artifacts...)
	out.Execution.Preconditions = cloneStrings(c.Execution.Preconditions)

	if c.Clarifications != nil {
		out.Clarifications = make([]Clarification, len(c.Clarifications))
		for i, question := range c.Clarifications {
			cloned := question
			cloned.Options = cloneStrings(question.Options)
			if question.AnsweredAt != nil {
				answeredAt := *question.AnsweredAt
				cloned.AnsweredAt = &answeredAt
			}
			out.Clarifications[i] = cloned
		}
	}

	if c.Groups != nil {
		out.Groups = make([]TaskGroup, len(c.Groups))
		for i, group := range c.Groups {
			cloned := group
			cloned.DependsOn = cloneStrings(group.DependsOn)
			if group.Items != nil {
				cloned.Items = make([]TaskItem, len(group.Items))
				for j, item := range group.Items {
					clonedItem := item
					clonedItem.RequiredCapabilities = cloneStrings(item.RequiredCapabilities)
					clonedItem.DependsOn = cloneStrings(item.DependsOn)
					cloned.Items[j] = clonedItem
				}
			}
			out.Groups[i] = cloned
		}
	}

	if c.Validations != nil {
		out.Validations = make([]ValidationCheckpoint, len(c.Validations))
		for i, checkpoint := range c.Validations {
			cloned := checkpoint
			cloned.AppliesTo = cloneStrings(checkpoint.AppliesTo)
			out.Validations[i] = cloned
		}
	}

	return out
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneTaskLinks(links []TaskLink) []TaskLink {
	if links == nil {
		return nil
	}
	out := make([]TaskLink, len(links))
	for i, link := range links {
		cloned := link
		if link.RetiredAt != nil {
			retiredAt := *link.RetiredAt
			cloned.RetiredAt = &retiredAt
		}
		out[i] = cloned
	}
	return out
}

func cloneRunLinks(links []RunLink) []RunLink {
	if links == nil {
		return nil
	}
	return append([]RunLink(nil), links...)
}
