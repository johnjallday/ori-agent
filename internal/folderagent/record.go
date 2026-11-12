// Package folderagent is "feed a folder, get a workspace with an agent": the
// one Start action (anchored workspace, its agent, the first ticket), the
// host's walk of the folder, the report, and the evolution a Set it up runs.
//
// State lives in one record per workspace, a JSON file in the workspace's own
// sidecar folder (<managed folder>/.ori/folder-agent-v1.json). The record
// holds the folder map and facts (relative paths only), never the anchor's
// absolute path: that stays in the workspace's directory reference. Deleting
// the workspace moves its managed folder to the trash and the record with it.
package folderagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// RecordVersion is the schema version of a stored record.
const RecordVersion = 1

// recordFile is the record's name inside the workspace's .ori folder.
const recordFile = "folder-agent-v1.json"

// sidecarDirName mirrors workspace.SidecarDirName.
const sidecarDirName = ".ori"

// State is where a folder's first look stands.
type State string

const (
	StateLooking   State = "looking"
	StateReported  State = "reported"
	StateKept      State = "kept"
	StateSettingUp State = "setting_up"
	StateSetUp     State = "set_up"
	StateFailed    State = "failed"
)

// Progress is the walk's live count, shown on the tile and the Needs you row.
// Opened, Folder and Turn carry the Commander's narration on the map: how many
// folders it has opened, the base name of the one it is in (never a path, see
// ProgressFolder), and whether the agent's own turn has begun (Folder is then
// the folder its look tool last opened).
type Progress struct {
	Entries     int    `json:"entries"`
	FoldersLeft int    `json:"folders_left"`
	Opened      int    `json:"opened,omitempty"`
	Folder      string `json:"folder,omitempty"`
	Turn        bool   `json:"turn,omitempty"`
}

// progressFolderMax caps the narrated folder name, in runes.
const progressFolderMax = 80

// ProgressFolder bounds a folder for the narration: its base name only, with
// control characters dropped and at most progressFolderMax runes. The fed
// folder itself ("" or ".") is the record's own name, passed as fallback.
func ProgressFolder(rel, fallback string) string {
	name := strings.TrimSpace(rel)
	if name == "" || name == "." || name == "/" {
		name = fallback
	}
	name = filepath.Base(filepath.FromSlash(strings.ReplaceAll(name, "\\", "/")))
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if n == progressFolderMax {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// TurnReceipt says how the agent's turn over the map ended (PRD FR11):
// finished with an empty reason or step_limit, time_limit, token_limit; or
// skipped with no_model, provider_unavailable or interrupted.
type TurnReceipt struct {
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	Model      string    `json:"model,omitempty"`
	Tokens     int       `json:"tokens,omitempty"`
	ModelCalls int       `json:"model_calls,omitempty"`
	ToolCalls  int       `json:"tool_calls,omitempty"`
	Looks      int       `json:"looks,omitempty"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	// Cause is the host's one sentence for why a skipped turn could not look
	// ("No API key is set for Anthropic."), from folderlook.CauseSentence;
	// CauseFixable says the owner can fix it before Look further (PRD FR21,
	// FR25). Never the provider's raw words.
	Cause        string `json:"cause,omitempty"`
	CauseFixable bool   `json:"cause_fixable,omitempty"`
}

// Turn receipt statuses and skip reasons.
const (
	TurnFinished = "finished"
	TurnSkipped  = "skipped"

	SkipNoModel             = "no_model"
	SkipProviderUnavailable = "provider_unavailable"
	SkipInterrupted         = "interrupted"
)

// Setup is a Set it up run: the plan lines the user confirmed and how far
// the run got.
type Setup struct {
	Stage      foldermap.Stage                    `json:"stage"`
	Selected   []string                           `json:"selected,omitempty"`
	Lines      []personalassistant.FolderPlanLine `json:"lines"`
	Status     string                             `json:"status"`
	StopReason string                             `json:"stop_reason,omitempty"`
	StartedAt  time.Time                          `json:"started_at"`
	FinishedAt time.Time                          `json:"finished_at,omitzero"`
	// Receipt is what the run made, as the host reported it (PRD FR10, FR11);
	// a stopped run's receipt holds what finished before the stop.
	Receipt *SetupReceipt `json:"receipt,omitempty"`
}

// SetupReceipt is what a Set it up run made: names, versions, counts and the
// app's own workspace routes. Never a path. It lives on the card only.
type SetupReceipt struct {
	// Installed are the plugins installed, each "<name> <version>".
	Installed []string `json:"installed,omitempty"`
	// Home is the Home the folder joined; HomeCreated when this run made it.
	Home        *WorkspaceRef `json:"home,omitempty"`
	HomeCreated bool          `json:"home_created,omitempty"`
	// Group is set when the folder became a group; Members are its workspaces.
	Group   bool           `json:"group,omitempty"`
	Members []WorkspaceRef `json:"members,omitempty"`
	// Hired are the agents the run hired, by name.
	Hired []string `json:"hired,omitempty"`
	// Listed songs on the Home's shelf, the noun for them, and the folders
	// holding songs no listing reached.
	Listed     int    `json:"listed,omitempty"`
	ListedNoun string `json:"listed_noun,omitempty"`
	Unreached  int    `json:"unreached,omitempty"`
}

// Bounds of a receipt: a run installs a few plugins and hires a few agents;
// a group has at most GroupMaxMembers members.
const (
	maxReceiptItems = 16
	maxReceiptText  = 200
)

// Clean bounds the receipt a host reported: short single-line names, routes
// to the app's own workspace pages only, at most a handful of each.
func (r *SetupReceipt) Clean() *SetupReceipt {
	if r == nil {
		return nil
	}
	text := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) > maxReceiptText || strings.ContainsAny(s, "\x00\r\n") {
			return ""
		}
		return s
	}
	texts := func(in []string) []string {
		var out []string
		for _, s := range in {
			if s = text(s); s != "" && len(out) < maxReceiptItems {
				out = append(out, s)
			}
		}
		return out
	}
	ref := func(in WorkspaceRef) (WorkspaceRef, bool) {
		name := text(in.Name)
		if name == "" {
			return WorkspaceRef{}, false
		}
		route := in.Route
		if !strings.HasPrefix(route, "/workspaces/") || strings.Contains(route, "//") || len(route) > 512 {
			route = ""
		}
		return WorkspaceRef{ID: text(in.ID), Name: name, Route: route}, true
	}
	out := &SetupReceipt{Installed: texts(r.Installed), Hired: texts(r.Hired), Group: r.Group,
		Listed: max(r.Listed, 0), ListedNoun: text(r.ListedNoun), Unreached: max(r.Unreached, 0)}
	if r.Home != nil {
		if home, ok := ref(*r.Home); ok {
			out.Home, out.HomeCreated = &home, r.HomeCreated
		}
	}
	for _, m := range r.Members {
		if member, ok := ref(m); ok && len(out.Members) < GroupMaxMembers {
			out.Members = append(out.Members, member)
		}
	}
	return out
}

// Premise answers.
const (
	PremiseConfirmed     = "confirmed"
	PremiseSomethingElse = "something_else"
)

// PremiseAnswer is the owner's answer to the premise, for the Home provider
// whose kind it named. A later look that recognises a different provider's
// kind asks again (PRD FR9).
type PremiseAnswer struct {
	Answer   string    `json:"answer"`
	Provider string    `json:"provider"`
	At       time.Time `json:"at"`
	// Relooked is set once a look finished after a "something else" answer:
	// the report is then shown again, without the Home or installs ticked.
	Relooked bool `json:"relooked,omitempty"`
}

// Record is one folder workspace's state.
type Record struct {
	// Current trusted identity is projected at runtime, never persisted.
	RootPolicy    folderlook.RootPolicy `json:"-"`
	PolicyBinding string                `json:"-"`
	Version       int                   `json:"version"`
	WorkspaceID   string                `json:"workspace_id"`
	OwnerUserID   string                `json:"owner_user_id"`
	FolderName    string                `json:"folder_name"`
	WorkspaceName string                `json:"workspace_name"`
	AgentName     string                `json:"agent_name"`
	TicketID      string                `json:"ticket_id"`
	Brief         string                `json:"brief"`
	State         State                 `json:"state"`

	Map        *foldermap.Map         `json:"map,omitempty"`
	Facts      foldermap.Facts        `json:"facts"`
	Stage      foldermap.Stage        `json:"stage,omitempty"`
	Candidates []folderlook.Candidate `json:"candidates,omitempty"`
	Progress   Progress               `json:"progress"`
	Turn       *TurnReceipt           `json:"turn,omitempty"`
	// Sections are the agent's words that survived the host's parser, with
	// the turn's provenance; nil when no turn produced any.
	Sections *folderlook.Sections `json:"sections,omitempty"`
	// AskFolders are the pickable answers to the agent's one question.
	AskFolders []folderlook.AskFolder `json:"ask_folders,omitempty"`
	// Hint is the owner's typed answer, carried into the next turn once.
	Hint string `json:"hint,omitempty"`
	// NextTicketID is the agent's suggested first ticket, created when the
	// Set it up run completed; NextStarted once the owner started it.
	NextTicketID string `json:"next_ticket_id,omitempty"`
	// ExistingMembers are project folders (relative) that a workspace already
	// anchors; the Group stage includes them instead of creating them twice.
	ExistingMembers map[string]WorkspaceRef `json:"existing_members,omitempty"`
	// Declined are optional candidates the owner unticked at Set it up; a
	// later look does not propose them again (PRD FR22).
	Declined    []string `json:"declined,omitempty"`
	NextStarted bool     `json:"next_started,omitempty"`
	Setup       *Setup   `json:"setup,omitempty"`
	// GoalAnswer is the owner's answer to the suggested-goal offer made once
	// the run was done: GoalUsed or GoalDismissed; empty while it waits. The
	// suggested goal itself stays in Sections.Goal.
	GoalAnswer string `json:"goal_answer,omitempty"`
	// Failure is the plain sentence a failed look shows.
	Failure string `json:"failure,omitempty"`

	// OffersSongs is set when a Set it up run starts under a build that offers
	// to learn the songs afterwards. A folder set up before that never gets the
	// offer: its owner has already seen the card and the goal offer.
	OffersSongs bool `json:"offers_songs,omitempty"`
	// SongsAnswer is the owner's answer to the offer to learn the songs, made
	// once the run was done: SongsLearn or SongsDismissed; empty while it waits
	// (or was never made). It is stored here and only here, so the offer is
	// made once per folder whatever the Home does later.
	SongsAnswer string `json:"songs_answer,omitempty"`
	// Learning is the first reading of the songs, started by "Learn them": how
	// far it got and how it ended. Nil until the offer is accepted.
	Learning *Learning `json:"learning,omitempty"`

	// AsksPremise is set on every record created since the premise shipped. A
	// record without it predates the question and is never asked (PRD FR11):
	// its owner has already seen the card.
	AsksPremise bool `json:"asks_premise,omitempty"`
	// FedFromHome is the Home provider key of the Home the folder was fed from
	// inside; empty when it was fed from anywhere else (PRD FR8).
	FedFromHome string `json:"fed_from_home,omitempty"`
	// Premise is the owner's answer to "This looks like a <kind> folder";
	// nil while unanswered.
	Premise *PremiseAnswer `json:"premise,omitempty"`

	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *Record) validate() error {
	if r == nil || r.Version != RecordVersion || r.WorkspaceID == "" {
		return errors.New("folderagent: invalid record")
	}
	if r.Map != nil {
		if err := r.Map.Validate(); err != nil {
			return err
		}
	}
	if r.Turn != nil && !validCause(r.Turn.Cause) {
		return errors.New("folderagent: invalid turn cause")
	}
	return nil
}

// validCause keeps a stored cause sentence bounded and on one line. The
// sentence is composed only from the host's fixed words and the provider and
// model names (a model name may hold a "/", as "meta-llama/…" does).
func validCause(text string) bool {
	return len(text) <= folderlook.MaxCauseText && !strings.ContainsAny(text, "\x00\r\n")
}

// ErrNoRecord means the workspace was not started from a folder.
var ErrNoRecord = errors.New("folderagent: no folder record for this workspace")

// Store reads and writes records in each workspace's sidecar folder. Writes
// are atomic (temp file + rename) and serialized per process.
type Store struct {
	sidecarDir func(workspaceID string) (string, error)
	mu         sync.Mutex
	now        func() time.Time
}

// NewStore builds a store over a resolver from workspace id to its managed
// (sidecar) folder.
func NewStore(sidecarDir func(workspaceID string) (string, error)) *Store {
	return &Store{sidecarDir: sidecarDir, now: time.Now}
}

func (s *Store) path(workspaceID string) (string, error) {
	if s == nil || s.sidecarDir == nil || workspaceID == "" {
		return "", ErrNoRecord
	}
	dir, err := s.sidecarDir(workspaceID)
	if err != nil || dir == "" {
		return "", ErrNoRecord
	}
	return filepath.Join(dir, sidecarDirName, recordFile), nil
}

// Read returns the workspace's record, or ErrNoRecord.
func (s *Store) Read(workspaceID string) (*Record, error) {
	path, err := s.path(workspaceID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- the path is the workspace's own managed folder resolved by the workspace store
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoRecord
	}
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("folderagent: read record: %w", err)
	}
	if err := rec.validate(); err != nil {
		return nil, err
	}
	if rec.WorkspaceID != workspaceID {
		return nil, ErrNoRecord
	}
	return &rec, nil
}

// Create writes a new record; it refuses to overwrite one.
func (s *Store) Create(rec *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := s.Read(rec.WorkspaceID); err == nil && existing != nil {
		return errors.New("folderagent: record already exists")
	}
	now := s.now().UTC()
	rec.Version, rec.Revision, rec.CreatedAt, rec.UpdatedAt = RecordVersion, 1, now, now
	return s.write(rec)
}

// Update applies fn to the current record and writes it back.
func (s *Store) Update(workspaceID string, fn func(*Record) error) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.Read(workspaceID)
	if err != nil {
		return nil, err
	}
	if err := fn(rec); err != nil {
		return nil, err
	}
	rec.Revision++
	rec.UpdatedAt = s.now().UTC()
	if err := s.write(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *Store) write(rec *Record) error {
	if err := rec.validate(); err != nil {
		return err
	}
	path, err := s.path(rec.WorkspaceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".folder-agent-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
