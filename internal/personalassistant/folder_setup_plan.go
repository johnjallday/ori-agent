package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// Line kinds of a folder setup plan, in the order the card shows them.
const (
	FolderPlanIntegration = "integration"
	FolderPlanProvider    = "provider"
	FolderPlanHome        = "home"
	FolderPlanWorkspace   = "workspace"
	FolderPlanFolder      = "folder"
	FolderPlanMode        = "mode"
	FolderPlanAgents      = "agents"
	FolderPlanTask        = "task"
	// A portfolio's plan: the library lists the projects, a project gets its
	// workspace when it is opened, and the shared assistant joins it then.
	FolderPlanLibrary   = "library"
	FolderPlanSongs     = "songs"
	FolderPlanAssistant = "assistant"
	// The Home's profile: which applications are on this computer, and whether
	// one of their templates folders is listed. Present only when the Home's
	// installed package declares a profile.
	FolderPlanProfile = "profile"
	// A structure's plan: the group, the workspaces it creates with one line per
	// member, and one line for each thing the proposal goes without.
	FolderPlanGroup       = "group"
	FolderPlanMembers     = "workspaces"
	FolderPlanMember      = "member"
	FolderPlanUnavailable = "unavailable"
	// FolderPlanArrangement says a structure is a saved arrangement's exact
	// selection, and that folders it did not select stay as they are.
	FolderPlanArrangement = "arrangement"
)

// FolderRecipeStructure names the host's own setup of a folder that holds
// several projects: one group and a linked workspace per member. Like the
// software recipe it installs nothing, and its whole intent is part of what the
// plan digest covers.
const FolderRecipeStructure = "folder_structure"

// States of one plan line while a run is in progress.
const (
	FolderLineWaiting = "waiting"
	FolderLineWorking = "working"
	FolderLineDone    = "done"
	FolderLineFailed  = "failed"
)

// Statuses of a one-card setup run.
const (
	FolderSetupRunning = "running"
	FolderSetupStopped = "stopped"
	FolderSetupDone    = "done"
)

// Stop reasons the card must be able to explain.
const (
	FolderStopPlanChanged   = "plan_changed"
	FolderStopNeedsPick     = "needs_pick"
	FolderStopNeedsChoice   = "needs_choice"
	FolderStopNeedsModel    = "needs_model"
	FolderStopInstallFailed = "install_failed"
	FolderStopInterrupted   = "interrupted"
	FolderStopFailed        = "failed"
	// FolderStopConsentStale: the Home's shared assistant was agreed to for an
	// older version of the blueprint's team; the user is asked again on the Home.
	FolderStopConsentStale = "consent_stale"
	// FolderStopAssistantMissing: the shared assistant the Home's consent created
	// was deleted or renamed.
	FolderStopAssistantMissing = "assistant_missing"
	// FolderStopMemberFailed: a structure's run could not create or link one
	// member's workspace. What finished is kept; Continue carries on from there.
	FolderStopMemberFailed = "member_failed"
)

// FolderPlanLine is one consequence the user consents to. Name and Detail are
// plain text and never contain a filesystem path. State is set only while a
// run is in progress and is not part of the plan's identity.
type FolderPlanLine struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	State  string `json:"state,omitempty"`
}

// Where an installable plugin stands, as the plan promised it.
const (
	FolderInstallReady   = "ready"
	FolderInstallInstall = "install"
	FolderInstallEnable  = "enable"
	FolderInstallUpdate  = "update"
)

// FolderSetupIntent is what the confirmed plan promised about the two plugins a
// setup may install, in a form the run can check. A resumed run is held to this,
// not to a recomputed plan: the plan shrinks as steps finish, the promise does not.
// It never leaves the server.
type FolderSetupIntent struct {
	Integration        string `json:"integration,omitempty"`
	IntegrationPlugin  string `json:"integration_plugin,omitempty"`
	IntegrationVersion string `json:"integration_version,omitempty"`
	Provider           string `json:"provider,omitempty"`
	ProviderPlugin     string `json:"provider_plugin,omitempty"`
	ProviderVersion    string `json:"provider_version,omitempty"`
	// Placement is "grouped" (inside a Home) or "standalone"; CreatesHome says
	// the plan promised to create that Home first. HomeTemplate is the exact
	// template the Home is created from.
	Placement    string                  `json:"placement,omitempty"`
	CreatesHome  bool                    `json:"creates_home,omitempty"`
	HomeTemplate string                  `json:"home_template,omitempty"`
	Destination  *FolderSetupDestination `json:"destination,omitempty"`
	// A portfolio plan: Portfolio marks it; StaffsHome promised to add the Home's
	// required roles; GrantsConsent promised the standing consent for the
	// projects' shared assistant (the plan's assistant line).
	Portfolio     bool `json:"portfolio,omitempty"`
	StaffsHome    bool `json:"staffs_home,omitempty"`
	GrantsConsent bool `json:"grants_consent,omitempty"`
	// SharedProjects says some of the collection's projects can be opened with
	// the shared assistant; a collection of other formats is listed only.
	SharedProjects bool `json:"shared_projects,omitempty"`
	// ReadsSongDetails says the plan's library line told the user that each
	// project's tempo, length and track count are read; GrantsSongDetails
	// promised to record that consent on the Home the plan creates.
	ReadsSongDetails  bool `json:"reads_song_details,omitempty"`
	GrantsSongDetails bool `json:"grants_song_details,omitempty"`
	// SetsProfile says the plan showed the Home's profile line, so the run
	// looks for installed applications; GrantsTemplates says that line also
	// told the user one application's templates folder is read, which only a
	// plan that creates the Home may say.
	SetsProfile     bool `json:"sets_profile,omitempty"`
	GrantsTemplates bool `json:"grants_templates,omitempty"`
	// Recipe names a setup the host carries out itself, with nothing to install
	// (folderdigest.BuiltinSoftwareProject). Empty for an integration or
	// portfolio plan. Software is that recipe's promise.
	Recipe        string                `json:"recipe,omitempty"`
	RecipeVersion int                   `json:"recipe_version,omitempty"`
	Software      *FolderSoftwareIntent `json:"software,omitempty"`
	// Structure is the FolderRecipeStructure recipe's promise.
	Structure *FolderStructureIntent `json:"structure,omitempty"`
}

// FolderStructureIntent is everything a reviewed structure setup promised, in a
// form the run checks before each effect: which group, with which Group
// Manager, and which member folders become which workspaces. Two reviews whose
// lines read the same but whose folders or names differ are different reviews.
// It never leaves the server and holds no filesystem path.
type FolderStructureIntent struct {
	GroupName string `json:"group_name"`
	GroupType string `json:"group_type"` // FolderGroupPlain or FolderGroupHome
	// GroupExists says the group is the owner's existing Home, which the
	// members join; nothing is created for it.
	GroupExists bool `json:"group_exists,omitempty"`
	// Manager is the Group Manager a new plain group is created with, and
	// ManagerAction whether it is a new saved agent ("create") or the owner's
	// saved agent of that name ("reuse"). RosterRevision witnesses the roster
	// plan the review showed.
	Manager        string `json:"manager,omitempty"`
	ManagerAction  string `json:"manager_action,omitempty"`
	RosterRevision string `json:"roster_revision,omitempty"`
	// RootKey and RootIdentity witness the folder the review was about.
	RootKey      string                        `json:"root_key"`
	RootIdentity string                        `json:"root_identity,omitempty"`
	Members      []FolderStructureMemberIntent `json:"members"`
	// Arrangement is the saved arrangement intention a selected structure was
	// compiled from. It is part of the digest the user confirms, so a review of
	// another record, revision or operation is a different review.
	Arrangement *FolderStructureArrangement `json:"arrangement,omitempty"`
}

// FolderStructureMemberIntent is one member the plan promised a workspace for.
type FolderStructureMemberIntent struct {
	CandidateID string `json:"candidate_id"`
	Name        string `json:"name"`
	// Blueprint is the blueprint the workspace starts from; empty is blank.
	Blueprint string `json:"blueprint,omitempty"`
	// Identity witnesses the member's own directory at review time.
	Identity string `json:"identity,omitempty"`
}

// Validate checks a structure intent's shape: known types, bounded text, at
// least one member, no path.
func (i FolderStructureIntent) Validate() error {
	if !validStructureWorkspaceName(i.GroupName) || (i.GroupType != FolderGroupPlain && i.GroupType != FolderGroupHome) ||
		validateFolderKey(i.RootKey) != nil || len(i.Members) == 0 || len(i.Members) > FolderStructureMaxMembers {
		return fmt.Errorf("%w: structure intent", errFolderDigestInvalid)
	}
	if i.Arrangement != nil && (!i.Arrangement.valid() || i.GroupType != FolderGroupPlain || i.GroupExists) {
		return fmt.Errorf("%w: structure arrangement", errFolderDigestInvalid)
	}
	switch i.ManagerAction {
	case "":
		if i.Manager != "" || i.RosterRevision != "" {
			return fmt.Errorf("%w: structure manager", errFolderDigestInvalid)
		}
	case FolderSoftwareCreate, FolderSoftwareReuse:
		if strings.TrimSpace(i.Manager) == "" || i.GroupExists {
			return fmt.Errorf("%w: structure manager", errFolderDigestInvalid)
		}
	default:
		return fmt.Errorf("%w: structure manager action", errFolderDigestInvalid)
	}
	seen := map[string]bool{}
	for _, member := range i.Members {
		if !validStructureName(member.CandidateID) || seen[member.CandidateID] || !validStructureWorkspaceName(member.Name) {
			return fmt.Errorf("%w: structure member", errFolderDigestInvalid)
		}
		seen[member.CandidateID] = true
	}
	texts := []string{i.Manager, i.RosterRevision, i.RootIdentity}
	for _, member := range i.Members {
		texts = append(texts, member.Blueprint, member.Identity)
	}
	for _, text := range texts {
		if len(text) > 200 || strings.ContainsAny(text, "\\\x00\r\n") || strings.HasPrefix(text, "/") || strings.HasPrefix(text, "~") {
			return fmt.Errorf("%w: structure intent text", errFolderDigestInvalid)
		}
	}
	return nil
}

// Outcomes a software setup can promise for the workspace, and what it does
// about the workspace's assistant and first assessment.
const (
	FolderSoftwareCreate = "create"
	FolderSoftwareReuse  = "reuse"
	// FolderSoftwareKeep leaves what the workspace already has untouched.
	FolderSoftwareKeep = "keep"
	FolderSoftwareSeed = "seed"
	// FolderSoftwareScopeReadOnly is the only access a software setup grants:
	// the selected folder, linked where it is, for reading.
	FolderSoftwareScopeReadOnly = "linked_folder_read_only"
)

// FolderSoftwareIntent is everything a reviewed software setup promised, in a
// form the run checks before each effect. It is part of what the plan digest
// covers: two reviews whose wording is identical but whose folder, workspace,
// assistant, model or evidence differ are different reviews. It never leaves
// the server and holds no filesystem path.
type FolderSoftwareIntent struct {
	// Outcome is create (a new workspace) or reuse (WorkspaceID, the user's
	// existing workspace for this exact folder).
	Outcome       string `json:"outcome"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	WorkspaceName string `json:"workspace_name"`
	// Template and TemplateRevision are the blueprint a new workspace is made
	// from, exactly as reviewed.
	Template         string `json:"template,omitempty"`
	TemplateRevision string `json:"template_revision,omitempty"`
	// SubjectKey and SubjectIdentity witness the one directory the review was
	// about: its canonical path's key and the directory itself.
	SubjectKey      string `json:"subject_key"`
	SubjectIdentity string `json:"subject_identity"`
	// Assistant is the development assistant's name. AssistantAction is create
	// (a new saved agent), reuse (a saved agent, used exactly as saved) or keep
	// (the reused workspace's own entry agent). RosterRevision witnesses the
	// reviewed roster, including the model it resolves to.
	Assistant       string `json:"assistant,omitempty"`
	AssistantAction string `json:"assistant_action"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	RosterRevision  string `json:"roster_revision,omitempty"`
	// Assessment is seed (one first assessment is added) or keep (the workspace
	// already has a first look, which is left as it is).
	Assessment string `json:"assessment"`
	// EvidenceRevision is the inspection the review's suggestions were read
	// from; empty when the user reviewed without one.
	EvidenceRevision string `json:"evidence_revision,omitempty"`
	// Scope is the access the setup grants. Planning says the workspace starts
	// with understanding and planning and nothing runs during setup.
	Scope    string `json:"scope"`
	Planning bool   `json:"planning"`
	// Workflow names the effective workflow a new workspace is given (its
	// profile, preset, how approved work starts, and where planning documents
	// go), or "keep" for a workspace whose own settings are left alone. A
	// change in what those defaults do is a change in what was reviewed.
	Workflow string `json:"workflow"`
	// Conversation is the conversation revision the review belongs to, when it
	// was made from one.
	Conversation string `json:"conversation,omitempty"`
}

// Validate checks a software intent's shape: known outcomes, bounded text and
// no path. It does not check the intent against anything on disk.
func (i FolderSoftwareIntent) Validate() error {
	switch i.Outcome {
	case FolderSoftwareCreate:
		if i.WorkspaceID != "" || i.Template == "" {
			return fmt.Errorf("%w: software create intent", errFolderDigestInvalid)
		}
	case FolderSoftwareReuse:
		if i.WorkspaceID == "" {
			return fmt.Errorf("%w: software reuse intent", errFolderDigestInvalid)
		}
	default:
		return fmt.Errorf("%w: software outcome", errFolderDigestInvalid)
	}
	switch i.AssistantAction {
	case FolderSoftwareCreate, FolderSoftwareReuse:
		if strings.TrimSpace(i.Assistant) == "" {
			return fmt.Errorf("%w: software assistant", errFolderDigestInvalid)
		}
	case FolderSoftwareKeep:
		if i.Outcome != FolderSoftwareReuse {
			return fmt.Errorf("%w: software assistant", errFolderDigestInvalid)
		}
	default:
		return fmt.Errorf("%w: software assistant action", errFolderDigestInvalid)
	}
	if i.Assessment != FolderSoftwareSeed && i.Assessment != FolderSoftwareKeep {
		return fmt.Errorf("%w: software assessment", errFolderDigestInvalid)
	}
	if i.Scope != FolderSoftwareScopeReadOnly || !i.Planning {
		return fmt.Errorf("%w: software scope", errFolderDigestInvalid)
	}
	if kept := i.Workflow == FolderSoftwareKeep; i.Workflow == "" || len(i.Workflow) > 120 || strings.ContainsAny(i.Workflow, "\\\x00\r\n") ||
		kept != (i.Outcome == FolderSoftwareReuse) {
		return fmt.Errorf("%w: software workflow", errFolderDigestInvalid)
	}
	if validateFolderKey(i.SubjectKey) != nil || strings.TrimSpace(i.SubjectIdentity) == "" || validateFolderName(i.WorkspaceName) != nil {
		return fmt.Errorf("%w: software subject", errFolderDigestInvalid)
	}
	for _, text := range []string{i.WorkspaceID, i.Template, i.TemplateRevision, i.SubjectIdentity, i.Assistant,
		i.Provider, i.Model, i.RosterRevision, i.EvidenceRevision, i.Conversation} {
		if len(text) > 200 || strings.ContainsAny(text, "\\\x00\r\n") || strings.HasPrefix(text, "/") || strings.HasPrefix(text, "~") {
			return fmt.Errorf("%w: software intent text", errFolderDigestInvalid)
		}
	}
	return nil
}

// FolderSetupPlan is everything one press of Set up will do. The browser sends
// Digest back with the click and the server recomputes the plan: a mismatch
// means the card on screen is stale and the click is refused.
type FolderSetupPlan struct {
	Lines       []FolderPlanLine        `json:"lines"`
	Digest      string                  `json:"digest"`
	Destination *FolderSetupDestination `json:"destination,omitempty"`
	// Prerequisites say what the setup needs and what it can use, as it stands
	// now. They are read fresh for each plan and are not part of the digest: a
	// prerequisite that changes the setup itself (the assistant's model) is in
	// the intent, and one that does not (an optional connection) must not make
	// an approved review stale.
	Prerequisites []FolderPrerequisite `json:"prerequisites,omitempty"`
	// Intent is the machine-readable promise behind the lines; server only.
	Intent FolderSetupIntent `json:"-"`
}

// States of a prerequisite. They are deliberately distinct: a thing that is
// configured has not been shown to work, and a thing Ori cannot check is
// unknown, not absent.
const (
	FolderPrerequisiteVerified   = "verified"
	FolderPrerequisiteConfigured = "configured"
	FolderPrerequisiteAbsent     = "absent"
	FolderPrerequisiteUnknown    = "unknown"
)

// FolderPrerequisite is one thing a setup needs or can use, where the fact came
// from, and where to fix it. Every field is plain text and none is a path.
type FolderPrerequisite struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state"`
	// Detail says what the state means here, in one sentence.
	Detail string `json:"detail,omitempty"`
	// Source says where the fact was read ("Ori's settings", "this project's
	// files"), so a reader can tell a check from a guess.
	Source string `json:"source,omitempty"`
	// Required is false for something a local setup works without.
	Required bool `json:"required"`
	// Route is an existing page where the user can set it up or connect it.
	Route string `json:"route,omitempty"`
}

// FolderPlanDigest is the SHA-256 of the lines' canonical JSON with State
// excluded, so progress on a line never changes what was consented to.
func FolderPlanDigest(lines []FolderPlanLine) string {
	canonical := make([]FolderPlanLine, len(lines))
	for i, line := range lines {
		canonical[i] = FolderPlanLine{Kind: line.Kind, Name: line.Name, Detail: line.Detail}
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// NewFolderSetupPlan builds a plan and stamps its digest.
func NewFolderSetupPlan(lines []FolderPlanLine) FolderSetupPlan {
	copied := append([]FolderPlanLine(nil), lines...)
	return FolderSetupPlan{Lines: copied, Digest: FolderPlanDigest(copied)}
}

// Stamped returns the plan with its digest recomputed from its lines and any
// destination witness. It copies the lines/witness, keeping the intent. A host's plan is never trusted to carry a
// digest it computed itself.
func (p FolderSetupPlan) Stamped() FolderSetupPlan {
	stamped := NewFolderSetupPlan(p.Lines)
	stamped.Intent = p.Intent
	stamped.Prerequisites = append([]FolderPrerequisite(nil), p.Prerequisites...)
	if p.Destination != nil {
		destination := *p.Destination
		stamped.Destination = &destination
		stamped.Intent.Destination = &destination
		stamped.Digest = DestinationPlanDigest(stamped.Lines, &destination)
	}
	if stamped.Intent.Recipe != "" {
		stamped.Digest = recipePlanDigest(stamped.Digest, stamped.Intent)
	}
	return stamped
}

// recipePlanDigest binds a built-in recipe's whole intent into the digest the
// user confirms. The lines say what will happen in words; the intent says
// which folder, workspace, assistant, model and evidence those words were
// about. A review is stale when either changes, even if the words do not.
func recipePlanDigest(base string, intent FolderSetupIntent) string {
	encoded, err := json.Marshal(intent)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte(base+"\x00recipe\x00"), encoded...))
	return hex.EncodeToString(sum[:])
}

// FolderSetupUpdate is one snapshot of a run, written onto the offer.
type FolderSetupUpdate struct {
	Lines           []FolderPlanLine
	Status          string
	StopReason      string
	RunID           string
	EntryName       string
	EntryCandidates []string
	// HomeID is the Home a portfolio run built or joined; Receipt is what it
	// read back from canonical state when it finished.
	HomeID  string
	Receipt []FolderReceiptRow
	// keepLines makes the service keep the stored lines and change only the
	// status; the service uses it to record a stop it decided on itself.
	keepLines bool
}

// FolderSetupRequest is what the host needs to plan or run a setup. Path is the
// folder's canonical path from the offer the server holds; it is set only for
// Run and never reaches the browser.
type FolderSetupRequest struct {
	UserID string
	Offer  FolderOffer
	// AppInstalled says whether the application itself is on this computer, which
	// the plan states honestly; setup never depends on it.
	AppInstalled bool
	Path         string
	EntryName    string
	// WorkspaceName names the new workspace when the review gave it a name of
	// its own; empty names it after the folder.
	WorkspaceName string
	// Plan is the confirmed plan a Run is held to.
	Plan FolderSetupPlan
	// Software is set for a built-in software offer: what the service holds
	// about the folder and the review so far.
	Software *FolderSoftwareRequest
	// Update records the run's progress on the offer.
	Update func(ctx context.Context, update FolderSetupUpdate) error
}

// FolderSetupRunner is the host seam behind one-card setup. Plan reads the
// consequences for the offer; Run drives them to the end or to the first stop,
// reporting through req.Update, and returns only for an unexpected error.
type FolderSetupRunner interface {
	Plan(ctx context.Context, req FolderSetupRequest) (FolderSetupPlan, error)
	Run(ctx context.Context, req FolderSetupRequest) error
}

// FolderSetupDestinationReader resolves destination metadata independently of
// software/model prerequisites, without initializing a setup journey.
type FolderSetupDestinationReader interface {
	ReadSetupDestination(context.Context, FolderSetupRequest) (*FolderSetupDestination, error)
}

// FolderSetupDestinationGuard revalidates a persisted witness on resume,
// without regenerating the consented remaining-step plan or doing setup work.
type FolderSetupDestinationGuard interface {
	ValidateSetupDestination(context.Context, FolderSetupRequest) error
}

// FolderSetupInput is the click on Set up: the plan digest the card showed, the
// idempotency key, and the project file when the user chose one.
type FolderSetupInput struct {
	RequestID  string
	PlanDigest string
	EntryName  string
}

// FolderSetupView is a run as the browser sees it.
type FolderSetupView struct {
	// PlanDigest is the plan the run was confirmed on; a resume sends it back.
	PlanDigest      string                  `json:"plan_digest"`
	Status          string                  `json:"status"`
	StopReason      string                  `json:"stop_reason,omitempty"`
	Lines           []FolderPlanLine        `json:"lines"`
	EntryCandidates []string                `json:"entry_candidates,omitempty"`
	Destination     *FolderSetupDestination `json:"destination,omitempty"`
}

const (
	// A structure's plan has a line per member workspace besides its fixed
	// lines, and its receipt a row per workspace it made.
	folderSetupMaxLines   = 16 + FolderStructureMaxMembers
	folderSetupMaxText    = 512
	folderSetupMaxEntries = 64
)

// validateFolderSetupRun keeps a stored run bounded and path-free: lines are
// plain text, and project files are bare names.
func validateFolderSetupRun(run FolderSetupRun) error {
	switch run.Status {
	case FolderSetupRunning, FolderSetupStopped, FolderSetupDone:
	default:
		return fmt.Errorf("%w: setup status", errFolderDigestInvalid)
	}
	if len(run.Lines) > folderSetupMaxLines || len(run.EntryCandidates) > folderSetupMaxEntries ||
		len(run.PlanDigest) > 64 || len(run.RunID) > 200 {
		return fmt.Errorf("%w: setup limits", errFolderDigestInvalid)
	}
	for _, line := range run.Lines {
		if line.Kind == "" || len(line.Name) > folderSetupMaxText || len(line.Detail) > folderSetupMaxText ||
			strings.ContainsAny(line.Name+line.Detail, "\x00\r\n") || looksLikeFilesystemPath(line.Name) || looksLikeFilesystemPath(line.Detail) {
			return fmt.Errorf("%w: setup line", errFolderDigestInvalid)
		}
	}
	intent := run.Intent
	if intent.Destination != nil {
		if err := intent.Destination.Validate(); err != nil {
			return err
		}
	}
	// A recipe is one the host knows, with its whole promise present and well
	// formed; anything else is refused rather than run on a guess.
	switch intent.Recipe {
	case "":
		if intent.RecipeVersion != 0 || intent.Software != nil || intent.Structure != nil {
			return fmt.Errorf("%w: setup recipe", errFolderDigestInvalid)
		}
	case folderdigest.BuiltinSoftwareProject:
		if intent.RecipeVersion < 1 || intent.Software == nil || intent.Structure != nil {
			return fmt.Errorf("%w: setup recipe", errFolderDigestInvalid)
		}
		if err := intent.Software.Validate(); err != nil {
			return err
		}
	case FolderRecipeStructure:
		if intent.RecipeVersion < 1 || intent.Structure == nil || intent.Software != nil {
			return fmt.Errorf("%w: setup recipe", errFolderDigestInvalid)
		}
		if err := intent.Structure.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unknown setup recipe", errFolderDigestInvalid)
	}
	for _, text := range []string{intent.Integration, intent.IntegrationPlugin, intent.IntegrationVersion,
		intent.Provider, intent.ProviderPlugin, intent.ProviderVersion, intent.Placement, intent.HomeTemplate} {
		if len(text) > 160 || strings.ContainsAny(text, "/\\\x00\r\n") {
			return fmt.Errorf("%w: setup intent", errFolderDigestInvalid)
		}
	}
	for _, name := range append([]string{run.EntryName}, run.EntryCandidates...) {
		if name != "" && validateFolderName(name) != nil {
			return fmt.Errorf("%w: setup entry", errFolderDigestInvalid)
		}
	}
	if len(run.HomeID) > 200 || strings.ContainsAny(run.HomeID, "/\\\x00\r\n") || len(run.Receipt) > folderSetupMaxLines {
		return fmt.Errorf("%w: setup home", errFolderDigestInvalid)
	}
	for _, row := range run.Receipt {
		if row.Kind == "" || len(row.Kind) > 40 || len(row.Name) > folderSetupMaxText || len(row.Detail) > folderSetupMaxText ||
			len(row.Route) > 512 || (row.Route != "" && !strings.HasPrefix(row.Route, "/workspaces/")) ||
			strings.ContainsAny(row.Name+row.Detail, "\x00\r\n") || looksLikeFilesystemPath(row.Name) || looksLikeFilesystemPath(row.Detail) {
			return fmt.Errorf("%w: setup receipt", errFolderDigestInvalid)
		}
	}
	return nil
}

// looksLikeFilesystemPath is true for text that names a place on disk: a leading
// slash or tilde, a backslash, or a slash-led word anywhere. A reviewed source
// label such as "owner/repo" is not one.
func looksLikeFilesystemPath(text string) bool {
	return strings.HasPrefix(text, "/") || strings.HasPrefix(text, "~") ||
		strings.Contains(text, "\\") || strings.Contains(text, " /") || strings.Contains(text, " ~/")
}

// FolderSetupRun is the persisted state of one offer's one-card setup. It is
// stored with the offer so the card survives a reload. A Running status with
// no live run (the server restarted) is reported as Stopped/interrupted.
type FolderSetupRun struct {
	PlanDigest string           `json:"plan_digest"`
	Lines      []FolderPlanLine `json:"lines"`
	Status     string           `json:"status"`
	StopReason string           `json:"stop_reason,omitempty"`
	// EntryName is the project file the run uses; EntryCandidates are the file
	// names (never paths) offered when several exist.
	EntryName       string    `json:"entry_name,omitempty"`
	EntryCandidates []string  `json:"entry_candidates,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	// Intent is what the confirmed plan promised, kept for a resume.
	Intent FolderSetupIntent `json:"intent,omitempty"`
	// HomeID and Receipt are a portfolio run's Home and what it read back from
	// canonical state when it finished; the offer resolves to that Home.
	HomeID  string             `json:"home_id,omitempty"`
	Receipt []FolderReceiptRow `json:"receipt,omitempty"`
}
