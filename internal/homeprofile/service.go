// Package homeprofile keeps a Home's profile: the small record of where its
// owner works that the Home's package declares rows for. It detects what can
// be detected, takes the owner's confirmations and edits from the Home card,
// and refuses everything else. It is generic over the host's tool table and
// the installed package's declaration; nothing here names an application.
//
// Every write comes from the owner's card or from a setup card the owner
// pressed. No agent tool writes a profile.
package homeprofile

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var (
	// ErrNotFound means the workspace is not this owner's Home. Routes answer
	// 404 without saying which part failed.
	ErrNotFound = errors.New("home profile not found")
	// ErrNotDeclared means the Home's installed package declares no profile.
	ErrNotDeclared = errors.New("this Home's package declares no profile")
	// ErrReadOnly means the Home's provider is unavailable for changes.
	ErrReadOnly = errors.New("the Home provider is unavailable for changes")
	// ErrChanged means the write named a revision that is no longer current.
	ErrChanged = errors.New("home profile changed")
	// ErrInvalid means the request itself is unacceptable: an application that
	// was not detected, a value out of bounds, a reused request_id.
	ErrInvalid = errors.New("invalid home profile request")
)

const (
	actionDetect = "detect"
	actionFields = "fields"

	maxRequestID = 120
)

// Declared is the profile section of the installed Home declaration a Home is
// pinned to, and the release that declares it.
type Declared struct {
	PluginID string
	Version  string
	Profile  projecttemplates.HomeProfileDeclaration
}

// Option is one allowed value of a select row and the words shown for it.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Dependencies are the host facts the service cannot know itself. Each is
// resolved per call, so a plugin installed or disabled later is seen.
type Dependencies struct {
	Workspaces workspace.Store
	// Declared resolves the profile section of the Home's installed package.
	Declared func(home *workspace.Workspace) (Declared, bool)
	// Writable reports whether the Home's provider is available for changes.
	Writable func(home *workspace.Workspace) bool
	// InstalledApps looks for installed tool-table applications. It runs only
	// when a caller asks for a detection, never on a read.
	InstalledApps func() []folderdigest.InstalledApp
	// LibraryFormats counts the Home's library entries per catalog project
	// format. Nil or an empty result means the library says nothing.
	LibraryFormats func(home *workspace.Workspace) map[string]int
	// TimeSignatures are the values a new project's time signature may take:
	// the option list of the blueprint the Home's projects are created from.
	TimeSignatures func(home *workspace.Workspace) []Option
	Now            func() time.Time
}

// Service reads and writes Home profiles.
type Service struct {
	deps Dependencies
}

// New binds the service to its host dependencies.
func New(deps Dependencies) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps}
}

func (s *Service) now() time.Time { return s.deps.Now().UTC().Truncate(time.Second) }

// ownedHome returns the Home's program state when home is exactly one of this
// owner's Homes: not a linked project, not another person's, not in the Trash.
func ownedHome(ownerID string, home *workspace.Workspace) (*workspace.AssistantProgramState, bool) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || home == nil || home.OwnerUserID != ownerID || home.GetAssistantProjectLink() != nil ||
		home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return nil, false
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.Key.Normalize().OwnerUserID != ownerID {
		return nil, false
	}
	return state, true
}

func (s *Service) declared(home *workspace.Workspace) (Declared, bool) {
	if s.deps.Declared == nil {
		return Declared{}, false
	}
	declared, ok := s.deps.Declared(home)
	if !ok || declared.PluginID == "" || declared.Version == "" || len(declared.Profile.Fields) == 0 {
		return Declared{}, false
	}
	return declared, true
}

func (s *Service) writable(home *workspace.Workspace) bool {
	return s.deps.Writable != nil && s.deps.Writable(home)
}

func (s *Service) timeSignatures(home *workspace.Workspace) []Option {
	if s.deps.TimeSignatures == nil {
		return nil
	}
	return s.deps.TimeSignatures(home)
}

// Read returns the Home's card: the declared rows and the stored values. It
// detects nothing and reads no folder. An owner's Home whose package declares
// no profile reads as unavailable rather than as an error.
func (s *Service) Read(ownerID, homeID string) (View, error) {
	home, err := s.lookup(ownerID, homeID)
	if err != nil {
		return View{}, err
	}
	return s.view(home, home.GetAssistantProgramState()), nil
}

func validRequestID(id string) bool {
	return id != "" && len(id) <= maxRequestID && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "\r\n\x00")
}

// errUnchanged and errReplayed end a write early without an error: nothing
// needed storing, or the request was already handled.
var (
	errUnchanged = errors.New("home profile unchanged")
	errReplayed  = errors.New("home profile request replayed")
)

// write is one owner write. change receives the Home's current record (an
// empty one when the Home has none yet) and edits it in place; returning
// errUnchanged stores nothing. ifRevision, when given, must name the record's
// current revision (0 for a Home with no record). A request_id that was
// already handled replays: nothing is written and the current card returns.
func (s *Service) write(ownerID, homeID, requestID, action string, ifRevision *int64,
	change func(home *workspace.Workspace, profile *workspace.HomeProfile, now time.Time) error) (View, error) {
	if s == nil || s.deps.Workspaces == nil {
		return View{}, ErrNotFound
	}
	homeID = strings.TrimSpace(homeID)
	if !validRequestID(requestID) {
		return View{}, fmt.Errorf("%w: request_id", ErrInvalid)
	}
	// A workspace that is not this owner's Home is refused before the store is
	// asked to update it, so a missing one is "not found", not a save failure.
	if _, err := s.lookup(ownerID, homeID); err != nil {
		return View{}, err
	}
	replayed := false
	err := s.deps.Workspaces.Update(homeID, func(home *workspace.Workspace) error {
		state, ok := ownedHome(ownerID, home)
		if !ok || home.ID != homeID {
			return ErrNotFound
		}
		declared, ok := s.declared(home)
		if !ok {
			return ErrNotDeclared
		}
		if !s.writable(home) {
			return ErrReadOnly
		}
		profile := state.GetHomeProfile()
		if profile == nil {
			profile = &workspace.HomeProfile{SchemaVersion: workspace.HomeProfileSchemaVersion}
		}
		if receipt, handled := profile.Request(requestID); handled {
			if receipt.Action != action {
				return fmt.Errorf("%w: request_id was used for another action", ErrInvalid)
			}
			return errReplayed
		}
		if ifRevision != nil && *ifRevision != profile.Revision {
			return ErrChanged
		}
		now := s.now()
		if err := change(home, profile, now); err != nil {
			return err
		}
		profile.Revision++
		profile.DeclaredBy = workspace.HomeProfileDeclaredBy{PluginID: declared.PluginID, Version: declared.Version, Title: declared.Profile.Title}
		profile.Requests = append(profile.Requests, workspace.HomeProfileRequest{ID: requestID, Action: action, Revision: profile.Revision, RecordedAt: now})
		if extra := len(profile.Requests) - workspace.HomeProfileMaxRequests; extra > 0 {
			profile.Requests = append([]workspace.HomeProfileRequest(nil), profile.Requests[extra:]...)
		}
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		state.HomeProfile = profile
		home.SetAssistantProgramState(state)
		return nil
	})
	switch {
	case errors.Is(err, errReplayed):
		replayed = true
	case err == nil, errors.Is(err, errUnchanged):
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotDeclared), errors.Is(err, ErrReadOnly),
		errors.Is(err, ErrChanged), errors.Is(err, ErrInvalid):
		return View{}, err
	case errors.Is(err, workspace.ErrStaleWorkspaceVersion):
		return View{}, fmt.Errorf("%w: %w", ErrChanged, err)
	default:
		return View{}, fmt.Errorf("save home profile: %w", err)
	}
	view, readErr := s.Read(ownerID, homeID)
	if readErr != nil {
		return View{}, readErr
	}
	view.Replayed = replayed
	return view, nil
}
