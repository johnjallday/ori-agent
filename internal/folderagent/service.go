package folderagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	// AgentName is the agent the owner hired on the card: one of their own
	// agents (BindExisting), or a new one created with Provider, Model and
	// Mandate (empty model: the store's default).
	AgentName    string
	BindExisting bool
	Provider     string
	Model        string
	Mandate      string
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
	// SetGoal replaces the workspace goal (the one that drives Backlog
	// suggestions); Goal reads it ("" when none).
	SetGoal(workspaceID, goal string) error
	Goal(workspaceID string) string
	// AnchorOwners maps each absolute folder that another workspace (not
	// exclude) is anchored to onto that workspace.
	AnchorOwners(folders []string, exclude string) map[string]WorkspaceRef
}

// Tickets opens and closes the host-owned first ticket, and holds the
// agent's suggested first ticket until the owner starts it.
type Tickets interface {
	Open(workspaceID, title, agentName string) (string, error)
	Finish(workspaceID, ticketID, result string, failed bool) error
	// CreateBacklog adds a backlog ticket assigned to nobody's run yet.
	CreateBacklog(workspaceID, title, details, agentName string) (string, error)
	// Start hands a backlog ticket to its agent (the confirmed start).
	Start(workspaceID, ticketID string) error
}

// Setups is the server side of the tools a report proposes and the Set it up
// run that brings them into place.
type Setups interface {
	Readiness(ctx context.Context, userID string) folderlook.Readiness
	// Run carries out the confirmed lines for one record. It reports each
	// line through update and returns the final status, stop reason and what
	// the run made.
	Run(ctx context.Context, rec Record, plan Plan, update func([]personalassistant.FolderPlanLine)) RunResult
}

// HomeHireModeler is the optional part of Setups that says which model the
// Home step's hires would run on right now, worded as the Hire step words a
// model ("<model> (<provider>)"); "" when no model can run. Every hire gets
// the same one (staffing takes the system model), so there is one answer.
type HomeHireModeler interface {
	HomeHireModel() string
}

// RunResult is how a Set it up run ended and what it made (its receipt,
// partial when it stopped).
type RunResult struct {
	Status     string
	StopReason string
	Receipt    *SetupReceipt
}

// Pins narrows the folder agent's own Toolbox to the anchor's listing tools
// during the first look, and widens it to the content tools once the owner
// chose Set it up or Look further (PRD FR9).
type Pins interface {
	Pin(workspaceID, agentName string, readContents bool) error
	// Release removes the first-look pin once Set it up completed.
	Release(workspaceID, agentName string) error
}

// Turns resolves the folder agent's tool-capable model for its bounded turn.
type Turns interface {
	// Model returns the chat and the model by name with its provider's
	// display name. It returns a *folderlook.ModelError naming why there is no
	// usable model (no model chosen, provider not configured or signed in,
	// provider missing, model not tool-capable), or folderlook.ErrNoModel.
	Model(ctx context.Context, workspaceID, agentName string) (folderlook.Chat, folderlook.ModelRef, error)
	// TokenBudget is the configured first_look_token_budget (0 = default).
	TokenBudget() int
}

// Memory writes the workspace's one "What this folder is" entry, replacing
// an earlier one that carried a first-look provenance.
type Memory interface {
	Remember(workspaceID, text, provenance string) error
}

// Matches is source (d) and (c) of the tool candidates: the existing
// marketplace searches, called in process with the table's words, and what
// the user already has.
type Matches interface {
	Search(ctx context.Context, words []string) []folderlook.SearchHit
	Installed(ctx context.Context) folderlook.Installed
}

// Deps wires a Service.
type Deps struct {
	Hiring     Hiring
	Matches    Matches
	Pins       Pins
	Turns      Turns
	Memory     Memory
	Store      *Store
	Folders    Folders
	Workspaces Workspaces
	Tickets    Tickets
	Setups     Setups
	Homes      Homes
	// Songs reads the Home's song-details consent for the offer to learn the
	// songs. Nil makes no offer.
	Songs Songs
	// Reports posts a look's report into the Commander's conversation when the
	// look ends (workspace chat sync). Nil posts nothing.
	Reports Reports
	Now     func() time.Time
	// KnownFolderRoot shares the chooser's trusted configured-home resolver.
	KnownFolderRoot func(string) (string, error)
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
	// fedFromHome is the Home provider key of the Home the folder was chosen
	// from inside, resolved on the server; empty otherwise.
	fedFromHome string
	// policy supplies editable defaults only, never mutation authority.
	policy folderlook.RootPolicy
}

// Homes tells which reviewed Home provider a workspace is a Home of, for the
// feed origin (PRD FR8). "" when the workspace is not one of the user's
// Homes, or its provider is unknown.
type Homes interface {
	HomeProviderOf(ctx context.Context, userID, workspaceID string) string
}

// NewService builds the service.
func NewService(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.KnownFolderRoot == nil {
		if folders, ok := deps.Folders.(interface{ KnownFolderRoot(string) (string, error) }); ok {
			deps.KnownFolderRoot = folders.KnownFolderRoot
		}
	}
	return &Service{deps: deps, choices: make(map[string]choice), running: make(map[string]bool)}
}

// Chosen is the card for a chosen folder.
type Chosen struct {
	Token      string `json:"choice_token"`
	FolderName string `json:"folder_name"`
	Title      string `json:"title"`
	Purpose    string `json:"purpose,omitempty"`
	Field      string `json:"field"`
	Brief      string `json:"brief"`
	BriefMax   int    `json:"brief_max"`
	Action     string `json:"action"`
	Note       string `json:"note"`
	// Hire is who the owner hires before anything is created.
	Hire *HireOptions `json:"hire"`
}

// Choose resolves the folder and issues a token for Start. Nothing is
// created and nothing is read; a cancelled dialog returns nil.
func (s *Service) Choose(ctx context.Context, userID string, c personalassistant.FolderChoice) (*Chosen, error) {
	return s.ChooseFrom(ctx, userID, c, "")
}

// ChooseFrom is Choose from a workspace page: when that workspace is one of
// the owner's Homes, the folder is recorded as fed from inside it, and the
// premise is not asked for the kind that Home holds (PRD FR8). The page names
// the workspace; the server decides whether it is a Home.
func (s *Service) ChooseFrom(ctx context.Context, userID string, c personalassistant.FolderChoice, fromWorkspaceID string) (*Chosen, error) {
	if s == nil || s.deps.Folders == nil {
		return nil, ErrUnavailable
	}
	path, chosen, err := s.deps.Folders.ChooseFolder(ctx, userID, c)
	if err != nil || !chosen {
		return nil, err
	}
	fedFromHome := ""
	if from := strings.TrimSpace(fromWorkspaceID); from != "" && s.deps.Homes != nil {
		fedFromHome = s.deps.Homes.HomeProviderOf(ctx, userID, from)
	}
	return s.choosePath(ctx, userID, path, fedFromHome), nil
}

// choosePath issues a volatile hire card for an already host-validated path.
// Only chooser resolution and recorded project-holder resolution call it.
func (s *Service) choosePath(ctx context.Context, userID, path, fedFromHome string) *Chosen {
	identity := ResolveRootIdentity(path, s.deps.KnownFolderRoot)
	purpose, brief := knownFolderHire(identity.Policy)
	token := newToken()
	s.mu.Lock()
	now := s.deps.Now()
	for key, held := range s.choices {
		if now.After(held.expires) {
			delete(s.choices, key)
		}
	}
	s.choices[token] = choice{userID: userID, path: path, expires: now.Add(choiceTTL), fedFromHome: fedFromHome, policy: identity.Policy}
	s.mu.Unlock()
	name := filepath.Base(path)
	hire := s.hireOptions(ctx, userID, name)
	if identity.Policy.IsCatchAll() {
		hire.Mandate = brief
	}
	return &Chosen{
		Token: token, FolderName: name, Title: "Start an agent on " + name, Field: "What should it do?",
		Purpose: purpose, Brief: brief, BriefMax: personalassistant.BriefMaxRunes, Action: "Hire & start",
		Note: "Nothing is created until you hire. It looks before it touches anything, and you confirm every step after.",
		Hire: hire,
	}
}

func (s *Service) takeChoice(userID, token string) (choice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held, ok := s.choices[strings.TrimSpace(token)]
	if !ok || held.userID != userID || s.deps.Now().After(held.expires) {
		return choice{}, ErrChoiceLost
	}
	return held, nil
}

func (s *Service) dropChoice(token string) {
	s.mu.Lock()
	delete(s.choices, strings.TrimSpace(token))
	s.mu.Unlock()
}

// Start is the card's one action, Hire & start (PRD FR2): the agent the owner
// hired on the card, an anchored workspace on the chosen folder with that
// agent, the first ticket, and the walk, started now.
func (s *Service) Start(ctx context.Context, userID, token, brief string, hire Hire) (View, error) {
	if s == nil || s.deps.Store == nil || s.deps.Workspaces == nil || s.deps.Tickets == nil {
		return View{}, ErrUnavailable
	}
	held, err := s.takeChoice(userID, token)
	if err != nil {
		return View{}, err
	}
	path := held.path
	brief, err = personalassistant.NormalizeFolderBrief(brief)
	if err != nil {
		return View{}, err
	}
	if brief == "" {
		_, brief = knownFolderHire(held.policy)
	}
	folder := filepath.Base(path)
	req, err := s.resolveHire(ctx, userID, folder, brief, hire)
	if err != nil {
		return View{}, err
	}
	// The brief is the agent's instruction for its look and stays on the
	// record; it is not the workspace goal, which only the owner's answer to
	// the goal offer sets (folder-setup-ends-in-home FR15–FR18).
	req.OwnerUserID, req.Name, req.AnchorPath = userID, s.freeName(ctx, folder), path
	created, err := s.deps.Workspaces.CreateAnchored(ctx, req)
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
		AsksPremise: true, FedFromHome: held.fedFromHome,
	}
	if err := s.deps.Store.Create(rec); err != nil {
		return View{}, err
	}
	s.pin(created.ID, created.AgentName, false)
	s.startLook(created.ID, nil)
	view := s.viewOf(ctx, rec)
	view.Nested = created.Nested
	return view, nil
}

// pin applies the Toolbox pin; a failure is logged, never fatal: the binding
// itself is read-only either way.
func (s *Service) pin(workspaceID, agentName string, readContents bool) {
	if s.deps.Pins == nil || agentName == "" {
		return
	}
	if err := s.deps.Pins.Pin(workspaceID, agentName, readContents); err != nil {
		logger.Warn("Folder agent could not pin its Toolbox", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
	}
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
		failed, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
			r.State, r.Failure = StateFailed, sentence
			return nil
		})
		if rec.TicketID != "" {
			_ = s.deps.Tickets.Finish(workspaceID, rec.TicketID, sentence, true)
		}
		if err == nil {
			s.announce(ctx, failed)
		}
	}
	root, err := s.deps.Workspaces.AnchorRoot(workspaceID)
	if err != nil || root == "" {
		fail("The folder is no longer where it was.", err)
		return
	}
	opts := s.deps.Walk
	var last time.Time
	opts.Progress = func(p foldermap.WalkProgress) {
		if now := time.Now(); now.Sub(last) >= progressEvery {
			last = now
			folder := ProgressFolder(p.Folder, rec.FolderName)
			_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
				r.Progress = Progress{Entries: p.Entries, FoldersLeft: p.FoldersLeft, Opened: p.Opened, Folder: folder}
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
	// The walk is done; the search and the turn come next. Until the turn
	// begins, the line says where the look stands as a whole.
	opened := 0
	for _, f := range m.Folders {
		if f.State != foldermap.StateUnopened {
			opened++
		}
	}
	whole := ProgressFolder("", rec.FolderName)
	_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
		r.Progress = Progress{Entries: m.Entries, FoldersLeft: len(m.Unopened()), Opened: opened, Folder: whole}
		return nil
	})
	var ready folderlook.Readiness
	if s.deps.Setups != nil {
		ready = s.deps.Setups.Readiness(ctx, rec.OwnerUserID)
	}
	identity := s.rootIdentity(workspaceID)
	rec.RootPolicy, rec.PolicyBinding = identity.Policy, identity.Binding
	facts := foldermap.FactsFrom(m)
	candidates := folderlook.CandidatesForPolicy(rec.RootPolicy, facts, ready)
	model := s.resolveModel(ctx, rec)
	// Source (d), the marketplace search, runs only for a turn that can rank
	// it (PRD FR23), with the table's own words.
	var found []folderlook.Candidate
	if rec.RootPolicy == folderlook.RootOrdinary && model.chat != nil && s.deps.Matches != nil {
		if words := folderlook.SearchWords(facts); len(words) > 0 {
			declined := make(map[string]bool, len(rec.Declined))
			for _, id := range rec.Declined {
				declined[id] = true
			}
			found = folderlook.SearchCandidates(s.deps.Matches.Search(ctx, words), candidates,
				s.deps.Matches.Installed(ctx), func(id string) bool { return declined[id] })
		}
	}
	turn, sections := s.runTurn(ctx, rec, root, m, facts, append(append([]folderlook.Candidate(nil), candidates...), found...), model)
	// The agent's looks may have found more: the facts are the host's again.
	facts = foldermap.FactsFrom(m)
	stage := foldermap.StageFor(facts)
	if rec.RootPolicy != folderlook.RootOrdinary {
		stage = foldermap.StageFolder
		if sections != nil {
			sections.Plan, sections.PlanSteps = "", nil
			sections.Goal, sections.Ticket, sections.TicketWhy, sections.Next = "", "", "", ""
		}
	}
	required := folderlook.CandidatesForPolicy(rec.RootPolicy, facts, ready)
	candidates = applyRanking(append(required, found...), sections)
	candidates = keepShown(candidates)
	existing := s.existingMembers(rec.WorkspaceID, root, stage, facts)
	var askFolders []folderlook.AskFolder
	if sections != nil && sections.Question != "" {
		askFolders = folderlook.AskFolders(facts)
	}
	updated, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		r.Map, r.Facts, r.Stage, r.Candidates, r.Turn = m, facts, stage, candidates, turn
		r.Sections, r.AskFolders, r.Hint = sections, askFolders, ""
		r.ExistingMembers = existing
		r.Progress = Progress{Entries: m.Entries, FoldersLeft: len(facts.Unopened)}
		r.State, r.Failure = StateReported, ""
		// A look after "something else" brings the report back, without the
		// Home or installs ticked (PRD FR7).
		if r.Premise != nil && r.Premise.Answer == PremiseSomethingElse {
			r.Premise.Relooked = true
		}
		return nil
	})
	if err != nil {
		logger.Warn("Folder agent could not save its look", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		return
	}
	updated = s.effectiveRecord(updated)
	s.remember(updated)
	s.announce(ctx, updated)
	if updated.TicketID != "" {
		result := foldermap.FactsLine(facts)
		if policy := folderlook.PolicyLine(updated.RootPolicy); policy != "" {
			result += "\n" + policy
		}
		result += "\n" + folderlook.DefaultLine(folderlook.StepsForPolicy(updated.RootPolicy, updated.FolderName, facts, candidates))
		if sections != nil && sections.Plan != "" {
			result += "\nPlan: " + folderlook.PlanWords(sections.Plan, folderlook.StepsForPolicy(updated.RootPolicy, updated.FolderName, facts, candidates))
		}
		if sections != nil && sections.WhatItIs != "" {
			result = sections.WhatItIs + "\n" + result
		}
		if err := s.deps.Tickets.Finish(workspaceID, updated.TicketID, result, false); err != nil {
			logger.Warn("Folder agent could not close its first ticket", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		}
	}
}

// existingMembers finds, for the Group stage, the project folders another
// workspace is already anchored to (PRD FR36): they are included as members,
// never created twice.
func (s *Service) existingMembers(workspaceID, root string, stage foldermap.Stage, facts foldermap.Facts) map[string]WorkspaceRef {
	if stage != foldermap.StageGroup || s.deps.Workspaces == nil {
		return nil
	}
	byFolder := make(map[string]string)
	var folders []string
	for i, p := range facts.Members {
		if i == GroupMaxMembers {
			break
		}
		abs := filepath.Join(root, filepath.FromSlash(p.Path))
		byFolder[abs] = p.Path
		folders = append(folders, abs)
	}
	owners := s.deps.Workspaces.AnchorOwners(folders, workspaceID)
	if len(owners) == 0 {
		return nil
	}
	out := make(map[string]WorkspaceRef, len(owners))
	for abs, ref := range owners {
		if rel, ok := byFolder[abs]; ok {
			out[rel] = ref
		}
	}
	return out
}

// runTurn runs the agent's one bounded turn over the map (PRD FR11). Its
// looks write into m; its words come back parsed and gated by the host.
// turnModel is the folder agent's resolved model, or why there is none.
type turnModel struct {
	chat     folderlook.Chat
	name     string
	provider string
	reason   string
	// cause is why there is no usable model, when the host could say.
	cause *folderlook.Cause
}

// resolveModel asks the host for the agent's model. A resolve failure is
// reported before any call (PRD FR24): no model chosen is no_model; a provider
// that is not configured, not signed in, missing or not tool-capable is
// provider_unavailable, each with its own cause. The underlying error is
// logged once, never stored.
func (s *Service) resolveModel(ctx context.Context, rec *Record) turnModel {
	if s.deps.Turns == nil {
		return turnModel{reason: SkipNoModel, cause: &folderlook.Cause{Kind: folderlook.CauseNoModel}}
	}
	chat, ref, err := s.deps.Turns.Model(ctx, rec.WorkspaceID, rec.AgentName)
	if err == nil && chat != nil && strings.TrimSpace(ref.Model) != "" {
		return turnModel{chat: chat, name: ref.Model, provider: ref.Provider}
	}
	var failed *folderlook.ModelError
	switch {
	case errors.As(err, &failed):
		resolved := turnModel{name: failed.Cause.Model, provider: failed.Cause.Provider, reason: SkipProviderUnavailable, cause: &failed.Cause}
		if failed.Cause.Kind == folderlook.CauseNoModel {
			resolved.reason = SkipNoModel
		}
		s.logSkippedLook(rec, resolved, err)
		return resolved
	case err != nil && !errors.Is(err, folderlook.ErrNoModel):
		resolved := turnModel{name: ref.Model, provider: ref.Provider, reason: SkipProviderUnavailable}
		s.logSkippedLook(rec, resolved, err)
		return resolved
	}
	resolved := turnModel{reason: SkipNoModel, cause: &folderlook.Cause{Kind: folderlook.CauseNoModel}}
	if err != nil {
		s.logSkippedLook(rec, resolved, err)
	}
	return resolved
}

// logSkippedLook records a look that could not run, once, with the underlying
// error the card never shows (PRD FR23).
func (s *Service) logSkippedLook(rec *Record, model turnModel, err error) {
	fields := logger.Fields{"workspace_id": rec.WorkspaceID, "provider": model.provider, "model": model.name, "reason": model.reason}
	if err != nil {
		fields["error"] = err.Error()
	}
	logger.Warn("Folder agent look skipped", fields)
}

// causeOf words a cause for the turn receipt; an empty sentence when none.
func causeOf(cause *folderlook.Cause, agent string) (string, bool) {
	if cause == nil {
		return "", false
	}
	return folderlook.CauseSentence(*cause, agent)
}

// keepShown keeps what the report shows: required candidates, and the ones
// the agent ranked. Unranked search hits are left out.
func keepShown(candidates []folderlook.Candidate) []folderlook.Candidate {
	var out []folderlook.Candidate
	for _, c := range candidates {
		if c.Required || c.Ranked {
			out = append(out, c)
		}
	}
	return out
}

// narrate marks the agent's turn in the live progress and names the folder its
// look tool opens ("" between looks), for the Commander's line on the map.
func (s *Service) narrate(workspaceID, folder string) {
	_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
		r.Progress.Turn, r.Progress.Folder = true, folder
		return nil
	})
}

func (s *Service) runTurn(ctx context.Context, rec *Record, root string, m *foldermap.Map, facts foldermap.Facts,
	candidates []folderlook.Candidate, resolved turnModel) (*TurnReceipt, *folderlook.Sections) {
	now := s.deps.Now().UTC()
	if resolved.chat == nil {
		receipt := &TurnReceipt{Status: TurnSkipped, Reason: resolved.reason, Model: resolved.name, StartedAt: now, FinishedAt: s.deps.Now().UTC()}
		receipt.Cause, receipt.CauseFixable = causeOf(resolved.cause, rec.AgentName)
		return receipt, nil
	}
	chat, model := resolved.chat, resolved.name
	s.narrate(rec.WorkspaceID, "")
	ask := folderlook.AskAllowed(facts)
	steps := folderlook.StepsForPolicy(rec.RootPolicy, rec.FolderName, facts, candidates)
	reference := folderlook.BuildReference(folderlook.ReferenceInput{
		FolderName: rec.FolderName, Brief: rec.Brief, Map: m, Facts: facts,
		StageLine: folderlook.DefaultLine(steps), Steps: steps, Candidates: candidates,
		AskAllowed: ask, Hint: rec.Hint, Policy: rec.RootPolicy,
	})
	var mu sync.Mutex
	look := func(_ context.Context, folder string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		s.narrate(rec.WorkspaceID, ProgressFolder(folder, rec.FolderName))
		result, err := foldermap.Look(root, m, folder)
		if err != nil {
			if errors.Is(err, foldermap.ErrNotInMap) {
				return "", errors.New("that folder is not in the map; open one the map lists")
			}
			return "", errors.New("that folder could not be listed")
		}
		var payload any = result
		if policy := folderlook.PolicyLine(rec.RootPolicy); policy != "" {
			// Put policy first so the turn's bounded tool output cannot truncate
			// it away when a listing fills the result budget.
			payload = struct {
				Policy  string `json:"root_policy"`
				Listing any    `json:"listing"`
			}{policy, result}
		}
		raw, err := json.Marshal(payload)
		return string(raw), err
	}
	result := folderlook.RunTurn(ctx, folderlook.TurnInput{
		Chat: chat, Model: model, Provider: resolved.provider, AgentName: rec.AgentName, Reference: reference,
		AskAllowed: ask, Look: look, TokenBudget: s.deps.Turns.TokenBudget(),
	})
	receipt := &TurnReceipt{Status: result.Status, Reason: result.Reason, Model: model, Tokens: result.Tokens,
		ModelCalls: result.ModelCalls, ToolCalls: result.ToolCalls, Looks: result.Looks, StartedAt: now, FinishedAt: s.deps.Now().UTC()}
	if result.Status == TurnSkipped {
		receipt.Cause, receipt.CauseFixable = causeOf(result.Cause, rec.AgentName)
		if result.Err != nil {
			s.logSkippedLook(rec, turnModel{name: model, provider: resolved.provider, reason: result.Reason}, result.Err)
		}
	}
	if result.Reply == "" {
		return receipt, nil
	}
	ids := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		ids[c.ID] = true
	}
	var nested []string
	for _, f := range m.Folders {
		if strings.Contains(f.Path, "/") {
			nested = append(nested, f.Path)
		}
	}
	parsed := folderlook.Parse(result.Reply, folderlook.ParseOptions{Candidates: ids, Steps: folderlook.StepHandles(steps), FolderPaths: nested})
	if !ask {
		// A question written when the facts are clear is not shown.
		parsed.Question = ""
	}
	parsed.Source, parsed.Model, parsed.TicketID = folderlook.SourceFirstLookModel, model, rec.TicketID
	return receipt, &parsed
}

// applyRanking marks the candidates the agent ranked and gives them its why
// line; ids it named that are not candidates were dropped by the parser.
func applyRanking(candidates []folderlook.Candidate, sections *folderlook.Sections) []folderlook.Candidate {
	if sections == nil {
		return candidates
	}
	why := make(map[string]string, len(sections.Tools))
	for _, tool := range sections.Tools {
		why[tool.ID] = tool.Why
	}
	for i := range candidates {
		if line, ok := why[candidates[i].ID]; ok {
			candidates[i].Ranked = true
			if line != "" {
				candidates[i].Why = line
			}
		}
	}
	return candidates
}

// MemoryTitle opens the one memory entry a folder agent keeps.
const MemoryTitle = "What this folder is:"

// maxMemorySentences bounds the memory note (PRD FR30: at most three lines).
const maxMemorySentences = 3

// remember writes, or replaces, the workspace memory entry "What this
// folder is" from the agent's sentence and the host's facts (PRD FR30). No
// model words, no entry; one with a path is refused.
func (s *Service) remember(rec *Record) {
	if s.deps.Memory == nil || rec == nil || rec.Sections == nil || rec.Sections.WhatItIs == "" {
		return
	}
	text := MemoryTitle + " " + rec.Sections.WhatItIs
	if facts := foldermap.FactsLine(rec.Facts); facts != "" {
		text += " Ori found: " + facts + "."
	}
	if sentences(text) > maxMemorySentences+1 || folderlook.NamesPath(text, nil) || utf8.RuneCountInString(text) > MemoryMaxRunes {
		logger.Warn("Folder agent memory note refused", logger.Fields{"workspace_id": rec.WorkspaceID})
		return
	}
	provenance := folderlook.SourceFirstLookModel + "/" + provenanceWord(rec.Sections.Model) + "/" + provenanceWord(rec.TicketID)
	if err := s.deps.Memory.Remember(rec.WorkspaceID, text, provenance); err != nil {
		logger.Warn("Folder agent could not write its memory note", logger.Fields{"workspace_id": rec.WorkspaceID, "error": err.Error()})
	}
}

// MemoryMaxRunes is workspace memory's own line cap.
const MemoryMaxRunes = 500

func sentences(text string) int {
	n := 0
	for i, r := range text {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(text) || text[i+1] == ' ') {
			n++
		}
	}
	return n
}

// provenanceWord keeps a provenance part inside memory's provenance slot.
func provenanceWord(text string) string {
	text = strings.Map(func(r rune) rune {
		if r == ',' || r == ']' || r == '[' || r == '/' || r == ' ' {
			return '-'
		}
		return r
	}, strings.TrimSpace(text))
	if text == "" {
		return "unknown"
	}
	return text
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
	return s.effectiveRecord(rec), nil
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
func (s *Service) LookFurther(ctx context.Context, userID, workspaceID string, first []string, hint string) (View, error) {
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
	// The owner's typed answer to the question: one line, no path, carried
	// into the next turn once.
	hint = strings.Join(strings.Fields(hint), " ")
	if utf8.RuneCountInString(hint) > folderlook.HintMaxRunes || folderlook.NamesPath(hint, nil) {
		return View{}, fmt.Errorf("%w: %s", personalassistant.ErrValidation, "answer in one line, without paths")
	}
	rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		if r.State != StateReported && r.State != StateKept && r.State != StateSetUp && r.State != StateFailed {
			return ErrWrongState
		}
		r.State, r.Failure, r.Hint = StateLooking, "", hint
		r.Progress.Turn, r.Progress.Folder = false, ""
		return nil
	})
	if err != nil {
		return View{}, err
	}
	s.pin(workspaceID, rec.AgentName, true)
	s.startLook(workspaceID, first)
	return s.viewOf(ctx, rec), nil
}

// SetUpRequest is what the owner confirmed on the plan card.
type SetUpRequest struct {
	PlanDigest string
	// Selected are the optional candidates left selected; required ones
	// always run. Set it up writes no workspace goal: the suggested goal is
	// offered once the run is done (AnswerGoal).
	Selected []string
}

// SetUp confirms the plan and runs it in the background (PRD FR32).
func (s *Service) SetUp(ctx context.Context, userID, workspaceID string, req SetUpRequest) (View, error) {
	current, err := s.Record(userID, workspaceID)
	if err != nil {
		return View{}, err
	}
	if !setUpAllowed(current) {
		return View{}, ErrWrongState
	}
	selected, planDigest := req.Selected, req.PlanDigest
	plan := BuildPlan(*current, selected)
	if plan.Digest != planDigest {
		return View{}, personalassistant.ErrFolderPlanChanged
	}
	// Nothing runs until the owner confirmed what the folder is (PRD FR10),
	// and nothing is recommended after "something else" until a look with
	// their words followed it (PRD FR6).
	if premiseOwed(current) || premiseWithdrawn(current) {
		return View{}, ErrPremiseOwed
	}
	if !plan.Runnable {
		if current.RootPolicy != folderlook.RootOrdinary {
			return View{}, personalassistant.ErrFolderPlanChanged
		}
		return View{}, ErrNothingToSet
	}
	if s.deps.Setups == nil {
		return View{}, ErrUnavailable
	}
	rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		if !setUpAllowed(r) {
			return ErrWrongState
		}
		if e := s.effectiveRecord(r); premiseOwed(e) || premiseWithdrawn(e) {
			return ErrPremiseOwed
		}
		if current.RootPolicy != folderlook.RootOrdinary {
			// Persist the choices actually confirmed, only on this mutation.
			// Otherwise a legacy model goal could resurface after the safe run
			// completed and its report became a historical receipt.
			r.Candidates, r.Sections, r.Stage = current.Candidates, current.Sections, current.Stage
		}
		r.State = StateSettingUp
		// Once the run is done the Commander offers to learn the songs; a
		// folder set up before this build never is.
		r.OffersSongs = true
		// The first step is under way from this moment; the run marks each
		// line as it reaches it.
		lines := append([]personalassistant.FolderPlanLine(nil), plan.Lines...)
		for i := range lines {
			if lines[i].Kind != personalassistant.FolderPlanFolder {
				lines[i].State = personalassistant.FolderLineWorking
				break
			}
		}
		r.Setup = &Setup{Stage: plan.Stage, Selected: plan.Selected, Lines: lines,
			Status: personalassistant.FolderSetupRunning, StartedAt: s.deps.Now().UTC()}
		if selected != nil {
			kept := make([]string, 0, len(plan.Candidates))
			for _, c := range plan.Candidates {
				kept = append(kept, c.ID)
			}
			r.Declined = appendDeclined(r.Declined, r.Candidates, kept)
		}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	s.pin(workspaceID, rec.AgentName, true)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// The run outlives the request that confirmed it: the request's values
		// travel with it, its cancellation does not.
		result := RunResult{Status: personalassistant.FolderSetupStopped, StopReason: personalassistant.FolderStopPlanChanged}
		if s.CheckPlan(workspaceID, plan) == nil {
			result = s.deps.Setups.Run(context.WithoutCancel(ctx), *rec, plan, func(lines []personalassistant.FolderPlanLine) {
				_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
					if r.Setup != nil {
						r.Setup.Lines = lines
					}
					return nil
				})
			})
		}
		status, reason := result.Status, result.StopReason
		nextID := ""
		if status == personalassistant.FolderSetupDone {
			nextID = s.createNextTicket(s.effectiveRecord(rec))
			// The first look is over: the agent works with the workspace's
			// tools, including any the run just added.
			if s.deps.Pins != nil {
				if err := s.deps.Pins.Release(workspaceID, rec.AgentName); err != nil {
					logger.Warn("Folder agent could not release its first-look pin", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
				}
			}
		}
		_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
			if r.Setup != nil {
				r.Setup.Status, r.Setup.StopReason, r.Setup.FinishedAt = status, reason, s.deps.Now().UTC()
				r.Setup.Receipt = result.Receipt.Clean()
			}
			if nextID != "" {
				r.NextTicketID = nextID
			}
			r.State = StateSetUp
			return nil
		})
	}()
	return s.viewOf(ctx, rec), nil
}

// appendDeclined records the ranked optional candidates the owner unticked.
func appendDeclined(declined []string, candidates []folderlook.Candidate, selected []string) []string {
	kept := make(map[string]bool, len(selected))
	for _, id := range selected {
		kept[id] = true
	}
	seen := make(map[string]bool, len(declined))
	for _, id := range declined {
		seen[id] = true
	}
	for _, c := range candidates {
		if c.Required || !c.Ranked || c.Action == folderlook.ActionAlreadyHere || kept[c.ID] || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		declined = append(declined, c.ID)
	}
	return declined
}

// Plan returns the plan card for the owner's own choice among the offered
// steps, so the card can show the lines and digest the owner will confirm.
func (s *Service) Plan(userID, workspaceID string, selected []string) (PlanView, error) {
	rec, err := s.Record(userID, workspaceID)
	if err != nil {
		return PlanView{}, err
	}
	// Plan is a new preview, not the historical receipt returned by View.
	copy := *rec
	copy.State = StateReported
	return *planViewOf(BuildPlan(*s.effectiveRecord(&copy), selected)), nil
}

// createNextTicket adds the agent's suggested first ticket to the backlog,
// assigned to the folder's agent, once (PRD FR29). It never starts.
func (s *Service) createNextTicket(rec *Record) string {
	if rec.NextTicketID != "" || rec.Sections == nil || rec.Sections.Next == "" {
		return rec.NextTicketID
	}
	id, err := s.deps.Tickets.CreateBacklog(rec.WorkspaceID, rec.Sections.Next, rec.Sections.TicketWhy, rec.AgentName)
	if err != nil {
		logger.Warn("Folder agent could not add its first ticket", logger.Fields{"workspace_id": rec.WorkspaceID, "error": err.Error()})
		return ""
	}
	return id
}

// StartNext is the receipt's Start this: the owner's confirmed start of the
// agent's suggested first ticket.
func (s *Service) StartNext(ctx context.Context, userID, workspaceID string) (View, error) {
	current, err := s.Record(userID, workspaceID)
	if err != nil {
		return View{}, err
	}
	if current.NextTicketID == "" || current.NextStarted {
		return View{}, ErrWrongState
	}
	if err := s.deps.Tickets.Start(workspaceID, current.NextTicketID); err != nil {
		return View{}, err
	}
	rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		r.NextStarted = true
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return s.viewOf(ctx, rec), nil
}

// Answers to the suggested-goal offer.
const (
	GoalUsed      = "used"
	GoalDismissed = "dismissed"
)

// goalOffered says whether the suggested goal is offered now: the run is done,
// the agent suggested a goal, the owner has not answered, and the workspace has
// no goal of its own (PRD FR17, FR19). The Commander asks one thing at a time:
// while the offer to learn the songs waits for an answer, the goal waits too.
func (s *Service) goalOffered(ctx context.Context, rec *Record) bool {
	if rec == nil || rec.State != StateSetUp || rec.Setup == nil || rec.Setup.Status != personalassistant.FolderSetupDone ||
		rec.GoalAnswer != "" {
		return false
	}
	// The goal the agent wrote after learning the songs, else the first look's.
	if goal, _ := offeredGoal(rec); goal == "" {
		return false
	}
	if s.deps.Workspaces == nil {
		return false
	}
	if learningRunning(rec) || s.songsOffer(ctx, rec) != nil {
		return false
	}
	// A workspace started before this build carries its brief as its goal;
	// that is not a goal the owner chose.
	goal := strings.TrimSpace(s.deps.Workspaces.Goal(rec.WorkspaceID))
	return goal == "" || goal == strings.TrimSpace(rec.Brief)
}

// AnswerGoal is the owner's answer to the goal offer: use sets the workspace
// goal to the suggested sentence, or to edited when given (one line, at most
// 300 characters, no path); otherwise the offer is dismissed and the goal
// stays empty. The offer is answered once (PRD FR18).
func (s *Service) AnswerGoal(ctx context.Context, userID, workspaceID string, use bool, edited *string) (View, error) {
	current, err := s.Record(userID, workspaceID)
	if err != nil {
		return View{}, err
	}
	if !s.goalOffered(ctx, current) {
		return View{}, ErrWrongState
	}
	goal, _ := offeredGoal(current)
	if use && edited != nil {
		goal = *edited
	}
	answer := GoalDismissed
	if use {
		goal, err = personalassistant.NormalizeFolderBrief(goal)
		if err != nil || goal == "" || folderlook.NamesPath(goal, nil) {
			return View{}, fmt.Errorf("%w: %s", personalassistant.ErrValidation, "goal")
		}
		answer = GoalUsed
	}
	rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		if r.GoalAnswer != "" {
			return ErrWrongState
		}
		r.GoalAnswer = answer
		return nil
	})
	if err != nil {
		return View{}, err
	}
	if use {
		if err := s.deps.Workspaces.SetGoal(workspaceID, goal); err != nil {
			// The answer is not kept when the goal could not be written: the
			// owner can try again.
			_, _ = s.deps.Store.Update(workspaceID, func(r *Record) error {
				r.GoalAnswer = ""
				return nil
			})
			return View{}, err
		}
	}
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
		if err != nil || (rec.State != StateLooking && rec.State != StateSettingUp && !learningRunning(rec)) {
			continue
		}
		swept, err := s.deps.Store.Update(id, func(r *Record) error {
			// A reading of the songs a previous process left running is over;
			// whatever it read is kept, and the goal offer is no longer held.
			if learningRunning(r) {
				r.Learning.Status, r.Learning.Song, r.Learning.FinishedAt = LearningStopped, "", s.deps.Now().UTC()
				r.Learning.Reason = LearningReasonRestarted
			}
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
			// A look this process never finished ends here: its report (or its
			// failure) reaches the conversation like any other look's.
			if rec.State == StateLooking {
				s.announce(context.Background(), s.effectiveRecord(swept))
			}
		}
	}
	return closed
}

func newToken() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return "fa-" + hex.EncodeToString(buf)
}
