package folderagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// DefaultBrief prefills the card's "What should it do?" field (PRD FR1).
const DefaultBrief = "Find out what's here and set it up"

// choiceTTL is how long a chosen folder waits for Start.
const choiceTTL = 30 * time.Minute

// progressEvery throttles progress writes during a walk.
const progressEvery = 250 * time.Millisecond

// Errors the HTTP layer maps to plain sentences.
var (
	ErrChoiceLost   = errors.New("folderagent: the folder choice expired")
	ErrUnavailable  = errors.New("folderagent: unavailable")
	ErrNotOwner     = errors.New("folderagent: not this user's workspace")
	ErrWrongState   = errors.New("folderagent: the report is not waiting for this")
	ErrStageNotYet  = errors.New("folderagent: this stage cannot be set up yet")
	ErrNothingToSet = errors.New("folderagent: nothing to set up")
)

// Refusal is a creation refusal shown on the card in its existing sentence
// (the anchor refusals, PRD FR3).
type Refusal struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Status    int           `json:"-"`
	Duplicate *WorkspaceRef `json:"duplicate,omitempty"`
}

func (r *Refusal) Error() string { return r.Message }

// WorkspaceRef names a workspace for an Open it link.
type WorkspaceRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Route string `json:"route,omitempty"`
}

// CreateRequest is the anchored workspace Start creates.
type CreateRequest struct {
	OwnerUserID string
	Name        string
	AnchorPath  string
	Mission     string
	// AgentName is "<folder name> Manager": a saved agent of that exact name
	// is bound, otherwise one is created (orchestrator, default model).
	AgentName string
}

// Created is what the creation made.
type Created struct {
	ID        string
	Name      string
	Slug      string
	AgentName string
	// Nested names the workspace whose anchor already contains this folder
	// (allowed, shown as a hint).
	Nested *WorkspaceRef
}

// Folders resolves a folder choice to the canonical folder the server holds.
type Folders interface {
	ChooseFolder(ctx context.Context, userID string, choice personalassistant.FolderChoice) (string, bool, error)
}

// Workspaces is the workspace seam: anchored creation through the ordinary
// pipeline, and reads of what already exists.
type Workspaces interface {
	CreateAnchored(ctx context.Context, req CreateRequest) (Created, error)
	NameTaken(ctx context.Context, name string) bool
	// AnchorRoot is the anchored folder's current absolute path, read from the
	// workspace's own directory reference.
	AnchorRoot(workspaceID string) (string, error)
	IDs() ([]string, error)
	Exists(workspaceID string) bool
	Route(workspaceID string) string
}

// Tickets opens and closes the host-owned first ticket.
type Tickets interface {
	Open(workspaceID, title, agentName string) (string, error)
	Finish(workspaceID, ticketID, result string, failed bool) error
}

// Setups is the server side of the tools a report proposes and the Set it up
// run that brings them into place.
type Setups interface {
	Readiness(ctx context.Context, userID string) folderlook.Readiness
	// Run carries out the confirmed lines for one record. It reports each
	// line through update and returns the final status and stop reason.
	Run(ctx context.Context, rec Record, plan Plan, update func([]personalassistant.FolderPlanLine)) (status, stopReason string)
}

// Deps wires a Service.
type Deps struct {
	Store      *Store
	Folders    Folders
	Workspaces Workspaces
	Tickets    Tickets
	Setups     Setups
	Now        func() time.Time
	// Walk overrides the walk options (tests).
	Walk foldermap.Options
}

// Service runs the folder agent flow.
type Service struct {
	deps Deps

	mu      sync.Mutex
	choices map[string]choice
	running map[string]bool
	wg      sync.WaitGroup
}

type choice struct {
	userID  string
	path    string
	expires time.Time
}

// NewService builds the service.
func NewService(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps, choices: make(map[string]choice), running: make(map[string]bool)}
}

// Chosen is the card for a chosen folder.
type Chosen struct {
	Token      string `json:"choice_token"`
	FolderName string `json:"folder_name"`
	Title      string `json:"title"`
	Field      string `json:"field"`
	Brief      string `json:"brief"`
	BriefMax   int    `json:"brief_max"`
	Action     string `json:"action"`
	Note       string `json:"note"`
}

// Choose resolves the folder and issues a token for Start. Nothing is
// created and nothing is read; a cancelled dialog returns nil.
func (s *Service) Choose(ctx context.Context, userID string, c personalassistant.FolderChoice) (*Chosen, error) {
	if s == nil || s.deps.Folders == nil {
		return nil, ErrUnavailable
	}
	path, chosen, err := s.deps.Folders.ChooseFolder(ctx, userID, c)
	if err != nil || !chosen {
		return nil, err
	}
	token := newToken()
	s.mu.Lock()
	now := s.deps.Now()
	for key, held := range s.choices {
		if now.After(held.expires) {
			delete(s.choices, key)
		}
	}
	s.choices[token] = choice{userID: userID, path: path, expires: now.Add(choiceTTL)}
	s.mu.Unlock()
	name := filepath.Base(path)
	return &Chosen{
		Token: token, FolderName: name, Title: "Start an agent on " + name, Field: "What should it do?",
		Brief: DefaultBrief, BriefMax: personalassistant.BriefMaxRunes, Action: "Start",
		Note: "It looks before it touches anything. You confirm every step after.",
	}, nil
}

func (s *Service) takeChoice(userID, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held, ok := s.choices[strings.TrimSpace(token)]
	if !ok || held.userID != userID || s.deps.Now().After(held.expires) {
		return "", ErrChoiceLost
	}
	return held.path, nil
}

func (s *Service) dropChoice(token string) {
	s.mu.Lock()
	delete(s.choices, strings.TrimSpace(token))
	s.mu.Unlock()
}

// Start is the card's one action (PRD FR2): an anchored workspace on the
// chosen folder, its agent, the first ticket, and the walk, started now.
func (s *Service) Start(ctx context.Context, userID, token, brief string) (View, error) {
	if s == nil || s.deps.Store == nil || s.deps.Workspaces == nil || s.deps.Tickets == nil {
		return View{}, ErrUnavailable
	}
	path, err := s.takeChoice(userID, token)
	if err != nil {
		return View{}, err
	}
	brief, err = personalassistant.NormalizeFolderBrief(brief)
	if err != nil {
		return View{}, err
	}
	if brief == "" {
		brief = DefaultBrief
	}
	folder := filepath.Base(path)
	name := s.freeName(ctx, folder)
	created, err := s.deps.Workspaces.CreateAnchored(ctx, CreateRequest{
		OwnerUserID: userID, Name: name, AnchorPath: path, Mission: brief, AgentName: folder + " Manager",
	})
	if err != nil {
		return View{}, err
	}
	s.dropChoice(token)
	ticketID, err := s.deps.Tickets.Open(created.ID, "Look around "+folder, created.AgentName)
	if err != nil {
		logger.Warn("Folder agent could not open its first ticket", logger.Fields{"workspace_id": created.ID, "error": err.Error()})
	}
	rec := &Record{
		WorkspaceID: created.ID, OwnerUserID: userID, FolderName: folder, WorkspaceName: created.Name,
		AgentName: created.AgentName, TicketID: ticketID, Brief: brief, State: StateLooking,
	}
	if err := s.deps.Store.Create(rec); err != nil {
		return View{}, err
	}
	s.startLook(created.ID, nil)
	view := s.viewOf(ctx, rec)
	view.Nested = created.Nested
	return view, nil
}

// freeName is the folder's name, with a numeric suffix when a workspace
// already uses it.
func (s *Service) freeName(ctx context.Context, folder string) string {
	name := folder
	for i := 2; i < 100 && s.deps.Workspaces.NameTaken(ctx, name); i++ {
		name = fmt.Sprintf("%s %d", folder, i)
	}
	return name
}

// startLook runs the walk (and, from group 2, the agent's turn) in the
// background. first names folders the user asked to open first.
func (s *Service) startLook(workspaceID string, first []string) {
	s.mu.Lock()
	if s.running[workspaceID] {
		s.mu.Unlock()
		return
	}
	s.running[workspaceID] = true
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, workspaceID)
			s.mu.Unlock()
		}()
		s.look(context.Background(), workspaceID, first)
	}()
}

// Wait blocks until background looks finish (tests, shutdown).
func (s *Service) Wait() { s.wg.Wait() }

// Running reports whether a look is in progress for the workspace.
func (s *Service) Running(workspaceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[workspaceID]
}

func (s *Service) look(ctx context.Context, workspaceID string, first []string) {
	rec, err := s.deps.Store.Read(workspaceID)
	if err != nil {
		return
	}
	fail := func(sentence string, cause error) {
		fields := logger.Fields{"workspace_id": workspaceID}
		if cause != nil {
			fields["error"] = cause.Error()
		}
		logger.Warn("Folder agent look failed", fields)
		_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
			r.State, r.Failure = StateFailed, sentence
			return nil
		})
		if rec.TicketID != "" {
			_ = s.deps.Tickets.Finish(workspaceID, rec.TicketID, sentence, true)
		}
	}
	root, err := s.deps.Workspaces.AnchorRoot(workspaceID)
	if err != nil || root == "" {
		fail("The folder is no longer where it was.", err)
		return
	}
	opts := s.deps.Walk
	var last time.Time
	opts.Progress = func(entries, left int) {
		if now := time.Now(); now.Sub(last) >= progressEvery {
			last = now
			_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
				r.Progress = Progress{Entries: entries, FoldersLeft: left}
				return nil
			})
		}
	}
	m := rec.Map
	if m == nil {
		m, err = foldermap.Walk(root, opts)
	} else {
		err = foldermap.Continue(root, m, first, opts)
	}
	if err != nil {
		fail("Ori could not list this folder.", err)
		return
	}
	facts := foldermap.FactsFrom(m)
	stage := foldermap.StageFor(facts)
	var ready folderlook.Readiness
	if s.deps.Setups != nil {
		ready = s.deps.Setups.Readiness(ctx, rec.OwnerUserID)
	}
	candidates := folderlook.RequiredCandidates(facts, ready)
	turn := &TurnReceipt{Status: TurnSkipped, Reason: SkipNoModel, FinishedAt: s.deps.Now().UTC()}
	updated, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		r.Map, r.Facts, r.Stage, r.Candidates, r.Turn = m, facts, stage, candidates, turn
		r.Progress = Progress{Entries: m.Entries, FoldersLeft: len(facts.Unopened)}
		r.State, r.Failure = StateReported, ""
		return nil
	})
	if err != nil {
		logger.Warn("Folder agent could not save its look", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		return
	}
	if updated.TicketID != "" {
		result := foldermap.FactsLine(facts) + "\n" + StageLine(facts, stage, candidates)
		if err := s.deps.Tickets.Finish(workspaceID, updated.TicketID, result, false); err != nil {
			logger.Warn("Folder agent could not close its first ticket", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		}
	}
}

// Record reads a user's record.
func (s *Service) Record(userID, workspaceID string) (*Record, error) {
	if s == nil || s.deps.Store == nil {
		return nil, ErrUnavailable
	}
	rec, err := s.deps.Store.Read(workspaceID)
	if err != nil {
		return nil, err
	}
	if rec.OwnerUserID != userID {
		return nil, ErrNotOwner
	}
	return rec, nil
}

// View reads the report for one workspace.
func (s *Service) View(ctx context.Context, userID, workspaceID string) (View, error) {
	rec, err := s.Record(userID, workspaceID)
	if err != nil {
		return View{}, err
	}
	return s.viewOf(ctx, rec), nil
}

// List returns every folder record the user owns whose workspace still
// exists, for Needs you and the map.
func (s *Service) List(ctx context.Context, userID string) []View {
	if s == nil || s.deps.Workspaces == nil {
		return nil
	}
	ids, err := s.deps.Workspaces.IDs()
	if err != nil {
		return nil
	}
	var out []View
	for _, id := range ids {
		rec, err := s.deps.Store.Read(id)
		if err != nil || rec.OwnerUserID != userID {
			continue
		}
		out = append(out, s.viewOf(ctx, rec))
	}
	return out
}

// Keep puts the report aside (PRD FR27): the workspace stays at the Folder
// stage with its agent; no further look is scheduled.
func (s *Service) Keep(ctx context.Context, userID, workspaceID string) (View, error) {
	if _, err := s.Record(userID, workspaceID); err != nil {
		return View{}, err
	}
	rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		if r.State != StateReported {
			return ErrWrongState
		}
		r.State = StateKept
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return s.viewOf(ctx, rec), nil
}

// LookFurther continues the walk from the unopened folders under a fresh
// budget, the named folders first (PRD FR18).
func (s *Service) LookFurther(ctx context.Context, userID, workspaceID string, first []string) (View, error) {
	if _, err := s.Record(userID, workspaceID); err != nil {
		return View{}, err
	}
	if len(first) > 3 {
		first = first[:3]
	}
	for i, rel := range first {
		cleaned, err := foldermap.CleanRel(rel)
		if err != nil {
			return View{}, fmt.Errorf("%w: %s", personalassistant.ErrValidation, "choose folders from the report")
		}
		first[i] = cleaned
	}
	rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		if r.State != StateReported && r.State != StateKept && r.State != StateSetUp && r.State != StateFailed {
			return ErrWrongState
		}
		r.State, r.Failure = StateLooking, ""
		return nil
	})
	if err != nil {
		return View{}, err
	}
	s.startLook(workspaceID, first)
	return s.viewOf(ctx, rec), nil
}

// SetUp confirms the plan and runs it in the background (PRD FR32). selected
// are the optional candidates the user left selected; required ones always
// run.
func (s *Service) SetUp(ctx context.Context, userID, workspaceID, planDigest string, selected []string) (View, error) {
	current, err := s.Record(userID, workspaceID)
	if err != nil {
		return View{}, err
	}
	plan := BuildPlan(*current, selected)
	if plan.Digest != planDigest {
		return View{}, personalassistant.ErrFolderPlanChanged
	}
	if !plan.Runnable {
		if plan.Stage == foldermap.StageFolder {
			return View{}, ErrNothingToSet
		}
		return View{}, ErrStageNotYet
	}
	if s.deps.Setups == nil {
		return View{}, ErrUnavailable
	}
	rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		if !setUpAllowed(r) {
			return ErrWrongState
		}
		r.State = StateSettingUp
		r.Setup = &Setup{Stage: plan.Stage, Selected: plan.Selected, Lines: plan.Lines,
			Status: personalassistant.FolderSetupRunning, StartedAt: s.deps.Now().UTC()}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		status, reason := s.deps.Setups.Run(context.Background(), *rec, plan, func(lines []personalassistant.FolderPlanLine) {
			_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
				if r.Setup != nil {
					r.Setup.Lines = lines
				}
				return nil
			})
		})
		_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
			if r.Setup != nil {
				r.Setup.Status, r.Setup.StopReason, r.Setup.FinishedAt = status, reason, s.deps.Now().UTC()
			}
			r.State = StateSetUp
			return nil
		})
	}()
	return s.viewOf(ctx, rec), nil
}

// setUpAllowed: a report waiting for an answer, one put aside, or a setup
// that stopped (Set it up again carries on from there).
func setUpAllowed(r *Record) bool {
	switch r.State {
	case StateReported, StateKept:
		return true
	case StateSetUp:
		return r.Setup != nil && r.Setup.Status == personalassistant.FolderSetupStopped
	}
	return false
}

// SweepInterrupted closes looks and runs a previous process left running.
// It runs once at startup and never starts a look.
func (s *Service) SweepInterrupted() int {
	if s == nil || s.deps.Workspaces == nil {
		return 0
	}
	ids, err := s.deps.Workspaces.IDs()
	if err != nil {
		return 0
	}
	closed := 0
	for _, id := range ids {
		rec, err := s.deps.Store.Read(id)
		if err != nil || (rec.State != StateLooking && rec.State != StateSettingUp) {
			continue
		}
		_, err = s.deps.Store.Update(id, func(r *Record) error {
			switch r.State {
			case StateLooking:
				if r.Map == nil {
					r.State, r.Failure = StateFailed, "Ori stopped before it finished looking. Look again to carry on."
				} else {
					r.State = StateReported
					r.Turn = &TurnReceipt{Status: TurnSkipped, Reason: SkipInterrupted, FinishedAt: s.deps.Now().UTC()}
				}
			case StateSettingUp:
				r.State = StateSetUp
				if r.Setup != nil {
					r.Setup.Status, r.Setup.StopReason = personalassistant.FolderSetupStopped, personalassistant.FolderStopInterrupted
				}
			}
			return nil
		})
		if err == nil {
			closed++
		}
	}
	return closed
}

func newToken() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return "fa-" + hex.EncodeToString(buf)
}
