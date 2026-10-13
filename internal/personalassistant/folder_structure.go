package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// A folder that holds several projects is proposed as one group with a
// workspace per subfolder. FolderStructure is that proposal: what every folder
// entry (the assistant's composer, Home, the assisted Create Workspace chat)
// shows before anything is created, and what a confirmed setup is held to.
const (
	FolderStructureVersion = 1
	// FolderStructureMaxMembers bounds the member list. Subfolders past it are
	// counted in Omitted and get no workspace from this proposal.
	FolderStructureMaxMembers = 24
	folderStructureMaxBytes   = 16 * 1024
)

// Group types of a proposed structure.
const (
	// FolderGroupHome is the shape's Home (today "Music Production Home"), which
	// a reviewed provider supplies.
	FolderGroupHome = "home"
	// FolderGroupPlain is a built-in group named after the folder. It needs no
	// plugin, so a structure can always be proposed.
	FolderGroupPlain = "group"
)

// folderPlainWorkspaceLabel is the type label of a member no marker names.
const folderPlainWorkspaceLabel = "Plain workspace"

// Why a part of a proposal is not what it could be. An unavailable part is
// still proposed, in its plain form, and says so; it is never dropped.
const (
	FolderAvailabilityIntegrationNotInstalled = "integration_not_installed"
	// FolderAvailabilityProviderNotInstalled: the plugin that supplies the
	// shape's Home is not on this computer. It is known without a release lookup,
	// and is the usual reason a first collection gets a plain group.
	FolderAvailabilityProviderNotInstalled = "provider_not_installed"
	// FolderAvailabilityProviderUnverified: that plugin is installed, but its
	// reviewed release could not be verified right now.
	FolderAvailabilityProviderUnverified = "provider_unverified"
	FolderAvailabilityDeclinedEarlier    = "declined_earlier"
	FolderAvailabilityPlanUnavailable    = "plan_unavailable"
)

// FolderAvailabilityCopy is the one sentence shown for an availability reason.
// subject names what is unavailable: the integration's application or the
// shape's Home. An unknown reason has no sentence.
func FolderAvailabilityCopy(reason, subject string) string {
	subject = strings.TrimSpace(subject)
	switch reason {
	case FolderAvailabilityIntegrationNotInstalled:
		if subject == "" {
			return "The integration is not installed; it can be added later."
		}
		return subject + " integration is not installed; it can be added later."
	case FolderAvailabilityProviderNotInstalled:
		if subject == "" {
			subject = "The Home"
		}
		return subject + " is not installed; a plain group is proposed instead."
	case FolderAvailabilityProviderUnverified:
		if subject == "" {
			subject = "The Home"
		}
		return subject + " could not be verified right now; a plain group is proposed instead."
	case FolderAvailabilityDeclinedEarlier:
		return "You declined this kind of setup earlier; Adjust can bring it back."
	case FolderAvailabilityPlanUnavailable:
		if subject == "" {
			return "One-click setup is unavailable right now; it can be added later."
		}
		return subject + " setup is unavailable right now; it can be added later."
	}
	return ""
}

// setupAvailability words a failed plan the way a proposal does. A host error
// that names its own reason is believed; anything else is simply unavailable.
func setupAvailability(err error) string {
	var reasoned interface{ SetupAvailability() string }
	if errors.As(err, &reasoned) {
		if reason := reasoned.SetupAvailability(); reason != "" && validFolderAvailability(reason) {
			return reason
		}
	}
	return FolderAvailabilityPlanUnavailable
}

// FolderStructureAvailabilityReader is an optional seam of the setup host: what
// a collection's proposal can use right now. Both answers are "" when the part
// is available and a FolderAvailability* reason when it is not. Neither
// installs, creates or changes anything, and neither takes a folder.
type FolderStructureAvailabilityReader interface {
	// HomeAvailability says whether the reviewed provider of a shape's Home can
	// be planned for this owner.
	HomeAvailability(ctx context.Context, userID, providerKey string) string
	// IntegrationAvailability says whether a reviewed integration is installed
	// and ready, so a workspace can start from its blueprint with nothing to
	// install.
	IntegrationAvailability(ctx context.Context, userID, integrationKey string) string
}

// structureFor describes a scanned collection for one owner: the proposal from
// the scan, then what each part can be right now. offer is the root's offer;
// its portfolio evidence, when present, says the owner can be offered the
// shape's Home at all. declined reports a capability domain the owner said no
// to. It returns nil for a folder that is not a collection.
//
// Nothing here fails the proposal: a Home that cannot be planned becomes a plain
// group, and a member whose blueprint cannot be used becomes a blank workspace,
// each with its reason (PRD FR10, decision 3A).
func (s *FolderDigestService) structureFor(ctx context.Context, userID string, result folderdigest.Result, verdict folderdigest.Verdict, offer FolderOffer, declined func(domain string) bool) *FolderStructure {
	structure := BuildFolderStructure(result, verdict, s.now(), false)
	if structure == nil {
		return nil
	}
	reader, _ := s.deps.Setup.(FolderStructureAvailabilityReader)
	if declined == nil {
		declined = func(string) bool { return false }
	}
	homeProvider := ""
	if structure.Group.HomeName != "" {
		row, _ := folderdigest.CapabilityForShape(verdict.Portfolio.Shape)
		reason := FolderAvailabilityPlanUnavailable
		if offer.Portfolio != nil && reader != nil {
			reason = reader.HomeAvailability(ctx, userID, row.Offer.HomeProviderKey)
			// A review that could not read where the Home is has no Home to use,
			// whatever the provider says.
			if reason == "" && offer.ConversationReview != nil && offer.ConversationReview.DestinationUnavailable {
				reason = FolderAvailabilityPlanUnavailable
			}
		}
		structure.Group.HomeAvailable = reason == ""
		switch {
		case declined(row.Offer.Slug):
			structure.Group.Availability = FolderAvailabilityDeclinedEarlier
		case reason != "":
			structure.Group.Availability = reason
		default:
			homeProvider = row.Offer.HomeProviderKey
			structure.Group.Type, structure.Group.Name = FolderGroupHome, structure.Group.HomeName
			if offer.Portfolio.ExistingHome {
				structure.Group.DestinationState = "existing"
			}
		}
	}
	integrations := map[string]string{}
	for i := range structure.Members {
		member := &structure.Members[i]
		if identity, err := portfolioDirectoryIdentity(filepath.Join(result.Root, member.RelPath)); err == nil {
			member.Identity = identity
		}
		row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(member.Shape), member.MarkerName, member.DominantExtension)
		if !ok {
			// No integration is involved: the shape's installed blueprint, or blank.
			subject := FolderCandidateRecord{Shape: member.Shape, MarkerName: member.MarkerName, DominantExtension: member.DominantExtension}
			if id, label, _ := s.blueprintForSubject(subject); id != "" {
				member.BlueprintID, member.BlueprintLabel = id, label
			}
			continue
		}
		member.Integration = row.Offer.IntegrationName
		reason, read := integrations[row.Offer.IntegrationKey]
		if !read {
			reason = FolderAvailabilityPlanUnavailable
			if reader != nil {
				reason = reader.IntegrationAvailability(ctx, userID, row.Offer.IntegrationKey)
			}
			integrations[row.Offer.IntegrationKey] = reason
		}
		switch {
		case declined(row.Offer.Slug):
			member.Availability = FolderAvailabilityDeclinedEarlier
		case reason != "":
			member.Availability = reason
		case row.Offer.HomeProviderKey != "" && row.Offer.HomeProviderKey != homeProvider:
			// The blueprint's workspaces live in the shape's Home. In a plain group
			// the member is a blank workspace, whatever is installed.
			member.Availability = FolderAvailabilityPlanUnavailable
		default:
			member.BlueprintID, member.BlueprintLabel = row.Blueprint.BlueprintID, row.Blueprint.Label
		}
	}
	return structure
}

func validFolderAvailability(reason string) bool {
	switch reason {
	case "", FolderAvailabilityIntegrationNotInstalled, FolderAvailabilityProviderNotInstalled,
		FolderAvailabilityProviderUnverified, FolderAvailabilityDeclinedEarlier, FolderAvailabilityPlanUnavailable:
		return true
	}
	return false
}

// FolderStructure is a path-free proposal: one group and its member
// workspaces. It is a description, never a plan or an execution grant.
type FolderStructure struct {
	Version int `json:"version"`
	// Folder is the attached folder's display name.
	Folder  string                  `json:"folder"`
	Group   FolderStructureGroup    `json:"group"`
	Members []FolderStructureMember `json:"members"`
	// MinMembers is the recognition rule the proposal was made under: how many
	// project subfolders make a folder a collection.
	MinMembers int `json:"min_members"`
	// Projects counts the members recognized as projects; the rest are other
	// folders that hold files.
	Projects int `json:"projects"`
	// Omitted counts subfolders left out of the bounded member list.
	Omitted int `json:"omitted,omitempty"`
	// Notes are the availability sentences of the group and the included
	// members, each said once. Projected for display; never stored.
	Notes []string `json:"notes,omitempty"`
}

// FolderStructureGroup is the proposed parent of the member workspaces.
type FolderStructureGroup struct {
	Name string `json:"name"`
	Type string `json:"type"` // FolderGroupHome or FolderGroupPlain
	// DestinationState is "new" for a group this setup creates and "existing"
	// for the owner's Home the members join.
	DestinationState string `json:"destination_state"`
	// HomeName names the shape's Home when the collection has one to offer,
	// whether or not it is the proposed type. HomeAvailable says it can be
	// chosen now; Availability says why not.
	HomeName      string `json:"home_name,omitempty"`
	HomeAvailable bool   `json:"home_available,omitempty"`
	Availability  string `json:"availability,omitempty"`
}

// FolderStructureMember is one subfolder and the workspace proposed for it.
type FolderStructureMember struct {
	// CandidateID is opaque within the scan the proposal was built from.
	CandidateID string `json:"candidate_id"`
	// Name is the proposed workspace name; Folder the subfolder's display name,
	// which a rename never changes.
	Name   string `json:"name"`
	Folder string `json:"folder"`
	// TypeLabel says what the folder was recognized as (its marker's label), or
	// "Plain workspace".
	TypeLabel string `json:"type_label"`
	Shape     string `json:"shape,omitempty"`
	Marker    string `json:"marker,omitempty"`
	// Project says the folder carries a project signal of its own.
	Project     bool   `json:"project,omitempty"`
	Files       int    `json:"files"`
	LastChanged string `json:"last_changed,omitempty"`
	Included    bool   `json:"included"`
	// BlueprintID is the blueprint the workspace would start from; empty is the
	// blank workspace. Integration names the reviewed integration the member's
	// own blueprint needs, when it needs one.
	BlueprintID    string `json:"blueprint_id,omitempty"`
	BlueprintLabel string `json:"blueprint_label,omitempty"`
	Integration    string `json:"integration,omitempty"`
	Availability   string `json:"availability,omitempty"`
	// ExistingWorkspace names the workspace this folder already belongs to.
	ExistingWorkspace string `json:"existing_workspace,omitempty"`

	// The fields below stay on the server: they are cleared from every view.
	// RelPath is the subfolder's own name below the root, and MarkerName and
	// DominantExtension are what it was recognized by, for the run and for a
	// later integration to find the workspace again.
	RelPath           string `json:"rel_path,omitempty"`
	MarkerName        string `json:"marker_name,omitempty"`
	DominantExtension string `json:"dominant_ext,omitempty"`
	// Identity witnesses the member's directory once a review is opened on it.
	Identity string `json:"identity,omitempty"`
}

// BuildFolderStructure describes a scanned root as a group with one workspace
// per subfolder, or returns nil when the root is not a collection (PRD FR1).
// Every immediate subfolder that holds a file is a member, included by default
// (FR2). The group is the shape's Home when the scan saw a portfolio and
// homeAvailable says that Home can be planned; otherwise a plain group named
// after the folder (FR4). What each member's workspace starts from, and why a
// part is unavailable, is filled in by the host afterwards.
func BuildFolderStructure(result folderdigest.Result, verdict folderdigest.Verdict, now time.Time, homeAvailable bool) *FolderStructure {
	if !folderdigest.IsCollection(result, now) {
		return nil
	}
	folder := foldercontext.DisplayName(result.Name)
	if folder == "" {
		folder = "Selected folder"
	}
	structure := &FolderStructure{
		Version: FolderStructureVersion, Folder: folder, MinMembers: folderdigest.CollectionMinProjects,
		Group:   FolderStructureGroup{Name: folder, Type: FolderGroupPlain, DestinationState: "new"},
		Members: []FolderStructureMember{},
	}
	// A member is a subfolder that holds a file, or one a marker alone makes a
	// project. When there are more than the list can hold, projects are kept
	// before other folders; the list itself stays in the scan's name order.
	keep := map[int]bool{}
	for _, wantProject := range []bool{true, false} {
		for index, candidate := range result.Candidates {
			project := folderdigest.IsProject(candidate, now)
			if candidate.IsRoot || project != wantProject || (!project && candidate.FileCount < 1) {
				continue
			}
			if len(keep) >= FolderStructureMaxMembers {
				structure.Omitted++
				continue
			}
			keep[index] = true
		}
	}
	for index, candidate := range result.Candidates {
		if !keep[index] {
			continue
		}
		name := foldercontext.DisplayName(candidate.Name)
		if name == "" {
			name = "Unnamed folder"
		}
		member := FolderStructureMember{
			CandidateID: "candidate-" + strconv.Itoa(index), Name: name, Folder: name,
			TypeLabel: folderPlainWorkspaceLabel, Shape: string(folderdigest.ShapeFor(candidate)),
			Project: folderdigest.IsProject(candidate, now), Files: candidate.FileCount, Included: true,
			RelPath: candidate.RelPath, DominantExtension: candidate.DominantExtension,
		}
		if member.Files > foldercontext.MaxEntries {
			member.Files = foldercontext.MaxEntries
		}
		if candidate.Marker != nil {
			member.Marker, member.MarkerName = foldercontext.DisplayName(candidate.Marker.Label), candidate.Marker.Name
			if member.Marker != "" {
				member.TypeLabel = member.Marker
			}
		}
		if !candidate.NewestModTime.IsZero() {
			member.LastChanged = candidate.NewestModTime.Format(foldercontext.DateLayout)
		}
		if member.Project {
			structure.Projects++
		}
		structure.Members = append(structure.Members, member)
	}
	if verdict.Portfolio != nil {
		if row, ok := folderdigest.CapabilityForShape(verdict.Portfolio.Shape); ok && row.Offer != nil && row.Offer.HomeName != "" {
			structure.Group.HomeName = row.Offer.HomeName
			if homeAvailable {
				structure.Group.HomeAvailable = true
				structure.Group.Type, structure.Group.Name = FolderGroupHome, row.Offer.HomeName
			}
		}
	}
	return structure
}

// Included returns the members a setup would create a workspace for.
func (s *FolderStructure) Included() []FolderStructureMember {
	if s == nil {
		return nil
	}
	included := make([]FolderStructureMember, 0, len(s.Members))
	for _, member := range s.Members {
		if member.Included {
			included = append(included, member)
		}
	}
	return included
}

// summary names the proposal in a few words ("Music Production Home with 6
// workspaces", "Group with 2 workspaces").
func (s *FolderStructure) summary() string {
	group := "Group"
	if s.Group.Type == FolderGroupHome {
		group = s.Group.Name
	}
	count := len(s.Included())
	if count == 1 {
		return group + " with 1 workspace"
	}
	return fmt.Sprintf("%s with %d workspaces", group, count)
}

// Member finds a member by its candidate id.
func (s *FolderStructure) Member(candidateID string) *FolderStructureMember {
	if s == nil {
		return nil
	}
	for i := range s.Members {
		if s.Members[i].CandidateID == candidateID {
			return &s.Members[i]
		}
	}
	return nil
}

// availabilityNotes are the sentences a card shows: the group's first, then
// each included member's, every sentence once.
func (s *FolderStructure) availabilityNotes() []string {
	if s == nil {
		return nil
	}
	var notes []string
	add := func(note string) {
		if note == "" {
			return
		}
		for _, seen := range notes {
			if seen == note {
				return
			}
		}
		notes = append(notes, note)
	}
	add(FolderAvailabilityCopy(s.Group.Availability, s.Group.HomeName))
	for _, member := range s.Members {
		if member.Included {
			add(FolderAvailabilityCopy(member.Availability, member.Integration))
		}
	}
	return notes
}

// clone returns a copy that shares no slice with the original.
func (s *FolderStructure) clone() *FolderStructure {
	if s == nil {
		return nil
	}
	copied := *s
	copied.Members = append([]FolderStructureMember(nil), s.Members...)
	copied.Notes = append([]string(nil), s.Notes...)
	return &copied
}

// view is the structure as a browser or a model may see it: the server's own
// fields are cleared and the availability sentences are filled in.
func (s *FolderStructure) view() *FolderStructure {
	if s == nil {
		return nil
	}
	view := s.clone()
	for i := range view.Members {
		view.Members[i].RelPath, view.Members[i].MarkerName = "", ""
		view.Members[i].DominantExtension, view.Members[i].Identity = "", ""
	}
	view.Notes = view.availabilityNotes()
	return view
}

// stored is the structure as the sidecar keeps it: without the projected notes.
func (s *FolderStructure) stored() *FolderStructure {
	if s == nil {
		return nil
	}
	stored := s.clone()
	stored.Notes = nil
	return stored
}

func validStructureName(value string) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= foldercontext.MaxNameRunes &&
		foldercontext.DisplayName(value) == value
}

// Validate keeps a structure bounded and path-free, the way an observation is:
// display names only, known types and reasons, counts within the scan's bounds.
func (s FolderStructure) Validate() error {
	invalid := func(part string) error { return fmt.Errorf("%w: structure %s", errFolderDigestInvalid, part) }
	if s.Version != FolderStructureVersion || !validStructureName(s.Folder) || s.MinMembers != folderdigest.CollectionMinProjects {
		return invalid("header")
	}
	if !validStructureName(s.Group.Name) || (s.Group.Type != FolderGroupHome && s.Group.Type != FolderGroupPlain) ||
		(s.Group.DestinationState != "new" && s.Group.DestinationState != "existing") ||
		(s.Group.HomeName != "" && !validStructureName(s.Group.HomeName)) || !validFolderAvailability(s.Group.Availability) {
		return invalid("group")
	}
	// A Home is proposed only while it can be chosen, and only a Home can be one
	// the owner already has.
	if (s.Group.Type == FolderGroupHome && (!s.Group.HomeAvailable || s.Group.HomeName == "")) ||
		(s.Group.Type == FolderGroupPlain && s.Group.DestinationState != "new") {
		return invalid("group type")
	}
	if len(s.Members) < s.MinMembers || len(s.Members) > FolderStructureMaxMembers ||
		s.Projects < s.MinMembers || s.Projects > len(s.Members) || s.Omitted < 0 || s.Omitted > foldercontext.MaxEntries {
		return invalid("members")
	}
	seen := map[string]bool{}
	for _, member := range s.Members {
		if !validStructureName(member.CandidateID) || seen[member.CandidateID] || !validStructureName(member.Name) ||
			!validStructureName(member.Folder) || !validStructureName(member.TypeLabel) ||
			member.Files < 0 || member.Files > foldercontext.MaxEntries || (member.Files == 0 && !member.Project) ||
			!validFolderAvailability(member.Availability) {
			return invalid("member")
		}
		seen[member.CandidateID] = true
		for _, text := range []string{member.Shape, member.Marker, member.BlueprintID, member.BlueprintLabel,
			member.Integration, member.ExistingWorkspace, member.MarkerName, member.DominantExtension, member.Identity} {
			if len(text) > 200 || strings.ContainsAny(text, "/\\\x00\r\n") {
				return invalid("member text")
			}
		}
		if member.RelPath != "" && validateFolderName(member.RelPath) != nil {
			return invalid("member folder")
		}
		if member.LastChanged != "" {
			if _, err := time.Parse(foldercontext.DateLayout, member.LastChanged); err != nil {
				return invalid("member date")
			}
		}
	}
	if len(s.Notes) > FolderStructureMaxMembers+1 {
		return invalid("notes")
	}
	encoded, err := json.Marshal(s)
	if err != nil || len(encoded) > folderStructureMaxBytes {
		return invalid("size")
	}
	return nil
}
