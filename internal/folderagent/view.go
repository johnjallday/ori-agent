package folderagent

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// MaxNextFolders is how many unopened folder names a report lists.
const MaxNextFolders = 5

// Report actions.
const (
	ActionSetUp       = "set_up"
	ActionKeep        = "keep"
	ActionLookFurther = "look_further"
)

// View is what the card, Needs you, the tile and the workspace page show.
// Every string names folders by name and never by path.
type View struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	Route         string `json:"route,omitempty"`
	FolderName    string `json:"folder_name"`
	AgentName     string `json:"agent_name"`
	TicketID      string `json:"ticket_id,omitempty"`
	Brief         string `json:"brief"`
	State         State  `json:"state"`

	// Receipt is the card's line right after Start.
	Receipt      string   `json:"receipt,omitempty"`
	Progress     Progress `json:"progress"`
	ProgressLine string   `json:"progress_line,omitempty"`

	PolicyNote    string                 `json:"policy_note,omitempty"`
	ExploreFolder bool                   `json:"explore_folder,omitempty"`
	FactsLine     string                 `json:"facts_line,omitempty"`
	StageLine     string                 `json:"stage_line,omitempty"`
	Stage         foldermap.Stage        `json:"stage,omitempty"`
	Candidates    []folderlook.Candidate `json:"candidates,omitempty"`
	NextFolders   []string               `json:"next_folders,omitempty"`
	ModelSentence string                 `json:"model_sentence,omitempty"`
	ToolsSentence string                 `json:"tools_sentence,omitempty"`
	Actions       []string               `json:"actions,omitempty"`
	Plan          *PlanView              `json:"plan,omitempty"`
	Setup         *Setup                 `json:"setup,omitempty"`
	Failure       string                 `json:"failure,omitempty"`
	Nested        *WorkspaceRef          `json:"nested,omitempty"`
	UpdatedAt     time.Time              `json:"updated_at"`

	// Premise is "This looks like a <kind> folder" and its answer
	// (confirm-and-visible-installs FR1–FR6); nil when the folder is not asked.
	// While its state is "asked" the report waits on it: the plan is prepared
	// but the card shows only the question, and Set it up is refused.
	Premise *PremiseView `json:"premise,omitempty"`

	// The agent's words (PRD FR24), shown as text, never executed.
	WhatItIs       string `json:"what_it_is,omitempty"`
	SuggestedGoal  string `json:"suggested_goal,omitempty"`
	FirstTicket    string `json:"first_ticket,omitempty"`
	FirstTicketWhy string `json:"first_ticket_why,omitempty"`
	Question       string `json:"question,omitempty"`
	// AskFolders are the question's pickable folder names.
	AskFolders []folderlook.AskFolder `json:"ask_folders,omitempty"`
	// LeftOut maps a section to the sentence shown in its place when the
	// agent left it out or the host dropped it.
	LeftOut map[string]string `json:"left_out,omitempty"`
	// NextTicket is the agent's first ticket, created when Set it up
	// finished; Start this starts it.
	NextTicket *NextTicketView `json:"next_ticket,omitempty"`
	// GoalOffer is the agent's suggested goal, offered once the run is done
	// and while the workspace has no goal and the owner has not answered
	// (Use this goal, Edit, Not now). Nil otherwise.
	GoalOffer *GoalOfferView `json:"goal_offer,omitempty"`
	// SongsOffer is the Commander's offer to learn the songs, made once after a
	// Set it up run that put the folder in a Home, and before the goal offer
	// (Learn them, Not now). Nil when none is waiting.
	SongsOffer *SongsOfferView `json:"songs_offer,omitempty"`
	// Learning is the reading of the songs once the owner said "Learn them":
	// the line it shows while it runs, and how it ended. Nil before that.
	Learning *LearningView `json:"learning,omitempty"`
	// Landing is where a watched run goes once it finished with every step
	// done and its plan included the Home step: the Home's Map, asking it to
	// open the projects sheet. Nil for every other run, which keeps Route.
	Landing *Landing `json:"landing,omitempty"`
}

// GoalOfferView is the sentence the offer shows; MaxRunes bounds an edit.
type GoalOfferView struct {
	Goal     string `json:"goal"`
	MaxRunes int    `json:"max_runes"`
}

// Landing names the Home a finished run lands in, for the card's "Opening
// <Name>…" line, and the route that opens its Map with the projects sheet.
type Landing struct {
	Route string `json:"route"`
	Name  string `json:"name"`
}

// SetupProjectsParam is the query parameter that asks a Home's Map to open
// its projects sheet once (the outer map's deep link uses the same one);
// SetupBesideParam names the folder workspace whose tile the sheet opens
// beside, when the map shows it.
const (
	SetupProjectsParam = "setup_projects"
	SetupBesideParam   = "setup_beside"
)

// landingOf is the Home landing for a done run whose plan ran the Home step.
func landingOf(rec *Record, plan Plan) *Landing {
	if rec.State != StateSetUp || rec.Setup == nil || rec.Setup.Status != personalassistant.FolderSetupDone ||
		rec.Setup.Receipt == nil || rec.Setup.Receipt.Home == nil {
		return nil
	}
	home := rec.Setup.Receipt.Home
	if !strings.HasPrefix(home.Route, "/workspaces/") || strings.ContainsAny(home.Route, "?#") {
		return nil
	}
	ranHome := false
	for _, s := range plan.Steps {
		if s.Chosen && s.Kind == folderlook.StepHome {
			ranHome = true
		}
	}
	if !ranHome {
		return nil
	}
	name := strings.TrimSpace(home.Name)
	if name == "" {
		name = "your Home"
	}
	query := url.Values{"mode": {"map"}, SetupProjectsParam: {"1"}}
	if rec.WorkspaceID != "" {
		query.Set(SetupBesideParam, rec.WorkspaceID)
	}
	return &Landing{Route: home.Route + "?" + query.Encode(), Name: name}
}

// NextTicketView is the receipt's line for the agent's first ticket.
type NextTicketView struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Started bool   `json:"started"`
}

// ActionStartNext starts the agent's first ticket from the receipt.
const ActionStartNext = "start_next"

// PlanView is the one-consent plan card's content.
type PlanView struct {
	Stage    foldermap.Stage                    `json:"stage"`
	Lines    []personalassistant.FolderPlanLine `json:"lines"`
	Digest   string                             `json:"digest"`
	Runnable bool                               `json:"runnable"`
	// Unavailable explains why a plan cannot run yet.
	Unavailable string `json:"unavailable,omitempty"`
	// Selected are the handles of the steps this plan includes.
	Selected []string `json:"selected,omitempty"`
	// Source says who chose the steps: "agent" or "default" (Ori's own plan).
	Source string `json:"source"`
	// Words is the agent's plan in its own words, each handle read as its
	// step's name; WordsRaw keeps the handles, for the card's chips.
	Words    string `json:"words,omitempty"`
	WordsRaw string `json:"words_raw,omitempty"`
	// Steps is every step Ori offered, each marked chosen when in the plan.
	Steps []folderlook.Step `json:"steps"`
	// Excluded explains a named step set aside because another named step
	// cannot run with it; DefaultReason says why Ori's own plan is shown.
	Excluded      string `json:"excluded,omitempty"`
	DefaultReason string `json:"default_reason,omitempty"`
	// ReorderNote is the muted line under the agent's words when Ori runs a
	// tools step ahead of where the agent named it.
	ReorderNote string `json:"reorder_note,omitempty"`
	// Installs are the plugins the ticked steps download and install, in run
	// order: the card's install block and the count on its button.
	Installs []InstallEntry `json:"installs"`
}

func planViewOf(plan Plan) *PlanView {
	steps := plan.Steps
	if steps == nil {
		steps = []folderlook.Step{}
	}
	installs := plan.Installs
	if installs == nil {
		installs = []InstallEntry{}
	}
	return &PlanView{Stage: plan.Stage, Lines: plan.Lines, Digest: plan.Digest, Runnable: plan.Runnable,
		Unavailable: plan.Unavailable, Selected: plan.Selected, Source: plan.Source, Words: plan.Words,
		WordsRaw: plan.WordsRaw, Steps: steps, Excluded: plan.Excluded, DefaultReason: plan.DefaultReason,
		ReorderNote: plan.ReorderNote, Installs: installs}
}

func (s *Service) viewOf(ctx context.Context, rec *Record) View {
	rec = s.effectiveRecord(rec)
	v := View{
		WorkspaceID: rec.WorkspaceID, WorkspaceName: rec.WorkspaceName, FolderName: rec.FolderName,
		AgentName: rec.AgentName, TicketID: rec.TicketID, Brief: rec.Brief, State: rec.State,
		Progress: rec.Progress, Setup: rec.Setup, Failure: rec.Failure, UpdatedAt: rec.UpdatedAt,
	}
	if s.deps.Workspaces != nil {
		v.Route = s.deps.Workspaces.Route(rec.WorkspaceID)
	}
	switch rec.State {
	case StateLooking:
		v.Receipt = "Looking around…"
		v.ProgressLine = ProgressLine(rec.Progress)
		return v
	case StateFailed:
		v.Actions = []string{ActionLookFurther}
		return v
	}
	if rec.Map == nil {
		return v
	}
	v.FactsLine = foldermap.FactsLine(rec.Facts)
	v.Stage = rec.Stage
	v.StageLine = folderlook.DefaultLine(folderlook.StepsForPolicy(rec.RootPolicy, rec.FolderName, rec.Facts, rec.Candidates))
	v.PolicyNote = folderlook.PolicyLine(rec.RootPolicy)
	v.ExploreFolder = rec.RootPolicy == folderlook.RootDownloads
	v.Candidates = rec.Candidates
	v.NextFolders = nextFolders(rec.Facts)
	v.ModelSentence = modelSentence(rec)
	sectionsInto(&v, rec)
	if rec.RootPolicy == folderlook.RootOrdinary && (rec.Turn == nil || rec.Turn.Status != TurnFinished) {
		v.ToolsSentence = "Search for more tools needs a model"
	}
	// Once a run exists the plan shown is the one it was confirmed on, not
	// the agent's recommendation (a stopped run carries on with it).
	var selected []string
	if rec.Setup != nil && len(rec.Setup.Selected) > 0 {
		selected = rec.Setup.Selected
	}
	plan := BuildPlan(*rec, selected)
	if completed(rec) {
		// A completed card is a receipt, not a newly generated offer catalog.
		// Retain the confirmed lines and omit alternatives it never ran.
		plan.Lines = rec.Setup.Lines
		plan.Stage, v.Stage = rec.Setup.Stage, rec.Setup.Stage
		plan.Runnable = false
		v.StageLine = "Set up as confirmed. Nothing in the source folder was moved."
		var ran []folderlook.Step
		kept := map[string]bool{}
		for _, step := range plan.Steps {
			if step.Chosen {
				ran = append(ran, step)
				kept[step.Handle] = true
			}
		}
		// The agent's words keep naming the steps that did not run.
		plan.WordsRaw = folderlook.NameHandles(plan.WordsRaw, plan.Steps, kept)
		plan.Steps = ran
	}
	v.Plan = planViewOf(plan)
	v.Landing = landingOf(rec, plan)
	v.SongsOffer = s.songsOffer(ctx, rec)
	v.Learning = learningView(rec)
	if s.goalOffered(ctx, rec) {
		v.GoalOffer = &GoalOfferView{Goal: rec.Sections.Goal, MaxRunes: personalassistant.BriefMaxRunes}
	}
	v.Premise = premiseView(rec)
	withdrawn := premiseWithdrawn(rec)
	if withdrawn {
		// "Something else": nothing is recommended until a look with the
		// owner's own words (FR6); the report stays on the record.
		v.Plan = nil
	}
	switch rec.State {
	case StateReported:
		v.Actions = []string{ActionSetUp, ActionKeep, ActionLookFurther}
		if withdrawn {
			v.Actions = []string{ActionKeep, ActionLookFurther}
		}
	case StateKept:
		v.Actions = []string{ActionSetUp, ActionLookFurther}
		if withdrawn {
			v.Actions = []string{ActionLookFurther}
		}
	case StateSetUp:
		v.Actions = []string{ActionLookFurther}
		if rec.Setup != nil && rec.Setup.Status == personalassistant.FolderSetupStopped {
			v.Actions = []string{ActionSetUp, ActionLookFurther}
		}
		if rec.NextTicketID != "" && rec.Sections != nil {
			v.NextTicket = &NextTicketView{ID: rec.NextTicketID, Title: rec.Sections.Next, Started: rec.NextStarted}
			if !rec.NextStarted {
				v.Actions = append([]string{ActionStartNext}, v.Actions...)
			}
		}
	}
	return v
}

// leftOutSections are the agent's parts the report shows, in order.
var leftOutSections = []string{folderlook.SectionWhatItIs, folderlook.SectionGoal, folderlook.SectionTicket}

// sectionsInto copies the agent's surviving words into the view, and puts
// one muted sentence in place of a part it left out or the host dropped.
func sectionsInto(v *View, rec *Record) {
	finished := rec.Turn != nil && rec.Turn.Status == TurnFinished
	agent := rec.AgentName
	if agent == "" {
		agent = "The agent"
	}
	if s := rec.Sections; s != nil {
		v.WhatItIs, v.SuggestedGoal, v.Question = s.WhatItIs, s.Goal, s.Question
		v.FirstTicket, v.FirstTicketWhy = s.Ticket, s.TicketWhy
		if s.Question != "" {
			v.AskFolders = rec.AskFolders
		}
	}
	if !finished {
		return
	}
	present := map[string]bool{
		folderlook.SectionWhatItIs: v.WhatItIs != "",
		folderlook.SectionGoal:     v.SuggestedGoal != "",
		folderlook.SectionTicket:   v.FirstTicket != "",
	}
	sentence := agent + " left this part out."
	switch rec.Turn.Reason {
	case folderlook.ReasonTimeLimit:
		sentence = agent + " ran out of time before this part."
	case folderlook.ReasonStepLimit, folderlook.ReasonTokenLimit:
		sentence = agent + " reached its limit for one look before this part."
	}
	for _, section := range leftOutSections {
		if present[section] {
			continue
		}
		if v.LeftOut == nil {
			v.LeftOut = map[string]string{}
		}
		v.LeftOut[section] = sentence
	}
}

// ProgressLine is the tile's and Needs you's line while the walk runs
// (PRD FR17): "Looking around · 240 entries · 3 folders left".
func ProgressLine(p Progress) string {
	if p.Entries == 0 {
		return "Looking around…"
	}
	line := fmt.Sprintf("Looking around · %d %s", p.Entries, plural(p.Entries, "entry", "entries"))
	if p.FoldersLeft > 0 {
		line += fmt.Sprintf(" · %d %s left", p.FoldersLeft, plural(p.FoldersLeft, "folder", "folders"))
	}
	return line
}

func modelSentence(rec *Record) string {
	if rec.Turn == nil || rec.Turn.Status == TurnFinished {
		return ""
	}
	agent := rec.AgentName
	if agent == "" {
		agent = "The agent"
	}
	// The host's cause sentence names the provider and model (PRD FR22).
	if cause := rec.Turn.Cause; cause != "" && rec.Turn.Reason != SkipInterrupted {
		sentence := agent + " could not look itself: " + cause
		if rec.Turn.CauseFixable {
			sentence += " Fix it and press Look further."
		}
		return sentence + " This is what Ori found."
	}
	switch rec.Turn.Reason {
	case SkipNoModel:
		return "No model is set up, so " + agent + " could not look itself. This is what Ori found."
	case SkipProviderUnavailable:
		return agent + "'s model was unavailable, so it could not look itself. This is what Ori found."
	case SkipInterrupted:
		return agent + " was interrupted before it could look itself. This is what Ori found."
	}
	return agent + " could not look itself. This is what Ori found."
}

// nextFolders are the unopened and large folders' names, at most five.
func nextFolders(f foldermap.Facts) []string {
	var out []string
	seen := make(map[string]bool)
	for _, rel := range append(append([]string(nil), f.Unopened...), f.Large...) {
		name := path.Base(rel)
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) == MaxNextFolders {
			break
		}
	}
	return out
}

// StageLine says what Set it up would do by default, in the host's words
// (PRD FR24): the catalog's default steps, or why there is nothing.
func StageLine(folderName string, f foldermap.Facts, candidates []folderlook.Candidate) string {
	steps := folderlook.Steps(folderName, f, candidates)
	if len(steps) == 0 && f.Recognized() {
		return "Nothing to set up for this project yet."
	}
	return folderlook.DefaultLine(steps)
}

// Who chose a plan's steps.
const (
	PlanByAgent = "agent"
	PlanByOri   = "default"
)

// Plan is a Set it up plan: the steps in effect, in order, and the host's
// lines for them.
type Plan struct {
	Stage         foldermap.Stage
	PolicyBinding string
	Lines         []personalassistant.FolderPlanLine
	Candidates    []folderlook.Candidate
	// Selected are the handles of the steps in the plan, sorted.
	Selected []string
	Digest   string
	Runnable bool
	// Unavailable is why the plan cannot run yet.
	Unavailable string
	// Source is PlanByAgent when the agent's plan named the steps, else
	// PlanByOri (its default: the stage's required steps and ranked add-ons).
	Source string
	// Words is the agent's plan in its own words, each handle read as its
	// step's name; WordsRaw keeps the handles.
	Words    string
	WordsRaw string
	// Steps is the catalog Ori offered, each marked Chosen when in the plan.
	Steps []folderlook.Step
	// Excluded explains a named step set aside because another named step
	// cannot run with it; DefaultReason says why Ori's own plan is shown.
	Excluded      string
	DefaultReason string
	// ReorderNote is set when Ori moved a tools step ahead of where the
	// agent's plan named it.
	ReorderNote string
	// Installs are the plugins the plan downloads and installs, in run order.
	Installs []InstallEntry
}

// InstallEntry is one plugin a plan downloads and installs, for the card's
// install block (PRD FR13–FR15). The host builds it; the card never parses
// line text for it.
type InstallEntry struct {
	// Step is the handle of the step that installs it; Line is the kind of
	// that step's plan line the run marks while it installs, so the block
	// can follow the run (the step has one line of that kind).
	Step string `json:"step"`
	Line string `json:"line"`
	// Name is the plugin's display name; Version the release it would put
	// in place, empty when the host does not know it before the download.
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// Publisher, SourceLabel and SourceURL name a reviewed release.
	Publisher   string `json:"publisher,omitempty"`
	SourceLabel string `json:"source_label,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
	// Search marks an add-on found by search; Marketplace is where.
	Search      bool   `json:"search,omitempty"`
	Marketplace string `json:"marketplace,omitempty"`
	// Why is what it gives the owner; AppSentence where the application
	// and its tools stand, for an integration.
	Why         string `json:"why,omitempty"`
	AppSentence string `json:"app_sentence,omitempty"`
}

// installEntryOf is the install block's row for a candidate that installs a
// plugin, under the step that runs it.
func installEntryOf(step string, c folderlook.Candidate) InstallEntry {
	e := InstallEntry{Step: step, Name: c.Name, Why: c.Why}
	if c.Release != nil {
		e.Version, e.Publisher, e.SourceLabel, e.SourceURL = c.Release.Version, c.Release.Publisher, c.Release.SourceLabel, c.Release.SourceURL
	}
	switch c.Kind {
	case folderlook.KindIntegration:
		e.Line = personalassistant.FolderPlanIntegration
		e.AppSentence = folderlook.AppToolsSentence(c)
	case folderlook.KindHomeProvider:
		e.Line = personalassistant.FolderPlanProvider
		if c.Home != nil && c.Home.ProviderName != "" {
			e.Name = c.Home.ProviderName
		}
		if e.Version == "" && c.Home != nil {
			e.Version = c.Home.ProviderVersion
		}
		e.Why = "Provides your " + homeName(c) + "."
	default:
		e.Line = FolderPlanAddOn
		e.Search = c.Source == folderlook.SourceSearch
		e.Marketplace = c.Marketplace
	}
	return e
}

// ReorderNote is the muted line under the agent's words when Ori runs the
// tools ahead of the order the agent named them in.
const ReorderNote = "Ori runs the tools first, so the Home can list what they can open."

// BuildPlan lists every consequence of Set it up. The steps come from the
// agent's plan (the handles it named, in its order), else from Ori's default
// (the stage's required steps and the add-ons the agent ranked). selected,
// when not nil, is the owner's own choice among the offered steps, by handle
// (a candidate id is accepted too), and replaces both.
func BuildPlan(rec Record, selected []string) Plan {
	plan := Plan{Stage: rec.Stage, Source: PlanByOri, PolicyBinding: rec.PolicyBinding}
	steps := folderlook.StepsForPolicy(rec.RootPolicy, rec.FolderName, rec.Facts, rec.Candidates)
	byHandle := make(map[string]int, len(steps))
	handleOf := make(map[string]string, len(steps))
	for i, s := range steps {
		byHandle[s.Handle] = i
		handleOf[s.Handle] = s.Handle
		if s.CandidateID != "" {
			handleOf[s.CandidateID] = s.Handle
		}
	}
	var agentOrder []string
	if rec.Sections != nil && rec.RootPolicy == folderlook.RootOrdinary {
		for _, h := range rec.Sections.PlanSteps {
			if _, ok := byHandle[h]; ok {
				agentOrder = append(agentOrder, h)
			}
		}
		plan.WordsRaw = rec.Sections.Plan
		plan.Words = folderlook.PlanWords(rec.Sections.Plan, steps)
	}
	var defaultOrder []string
	for _, s := range steps {
		if s.Default {
			defaultOrder = append(defaultOrder, s.Handle)
		}
	}
	base := defaultOrder
	if len(agentOrder) > 0 {
		base, plan.Source = agentOrder, PlanByAgent
	} else {
		plan.DefaultReason = defaultReason(rec)
	}
	order := base
	if selected == nil && premiseDeclined(&rec) {
		// The owner said the folder is something else: their answer outranks
		// the agent's and Ori's recommendation. The Home step and every step
		// that installs a plugin stay offered, unticked, until they tick one
		// (confirm-and-visible-installs FR7).
		candidates := make(map[string]folderlook.Candidate, len(rec.Candidates))
		for _, c := range rec.Candidates {
			candidates[c.ID] = c
		}
		var kept []string
		for _, h := range order {
			step := steps[byHandle[h]]
			if step.Kind == folderlook.StepHome || installsPlugin(candidates[step.CandidateID]) {
				continue
			}
			kept = append(kept, h)
		}
		order = kept
	}
	if selected != nil {
		ticked := make(map[string]bool, len(selected))
		for _, id := range selected {
			if h, ok := handleOf[strings.TrimSpace(id)]; ok {
				ticked[h] = true
			}
		}
		order = nil
		for _, h := range base {
			if ticked[h] {
				order = append(order, h)
				delete(ticked, h)
			}
		}
		for _, s := range steps {
			if ticked[s.Handle] {
				order = append(order, s.Handle)
			}
		}
	}
	order, plan.Excluded = applyExcludes(order, steps, byHandle)
	asNamed := order
	order = toolsBeforeHome(order, steps, byHandle)
	// The agent's words are never rewritten; when the host moved a tools step
	// ahead of where the agent named it, the card says why (PRD FR26).
	if plan.Source == PlanByAgent && strings.Join(asNamed, ",") != strings.Join(order, ",") {
		plan.ReorderNote = ReorderNote
	}
	chosen := make(map[string]bool, len(order))
	for _, h := range order {
		chosen[h] = true
	}
	plan.Steps = make([]folderlook.Step, len(steps))
	for i, s := range steps {
		s.Chosen = chosen[s.Handle]
		plan.Steps[i] = s
	}
	candidates := make(map[string]folderlook.Candidate, len(rec.Candidates))
	for _, c := range rec.Candidates {
		candidates[c.ID] = c
	}
	stage := foldermap.Stage("")
	type bound struct {
		handle string
		start  int
	}
	var bounds []bound
	for _, h := range order {
		step := steps[byHandle[h]]
		plan.Selected = append(plan.Selected, h)
		bounds = append(bounds, bound{handle: h, start: len(plan.Lines)})
		if step.Kind == folderlook.StepGroup {
			stage = foldermap.StageGroup
			plan.Lines = append(plan.Lines, groupLines(rec)...)
			continue
		}
		c, ok := candidates[step.CandidateID]
		if !ok {
			continue
		}
		if step.Kind == folderlook.StepHome && stage != foldermap.StageGroup {
			stage = foldermap.StageHome
		} else if stage == "" {
			stage = foldermap.StageTools
		}
		plan.Candidates = append(plan.Candidates, c)
		if installsPlugin(c) {
			plan.Installs = append(plan.Installs, installEntryOf(h, c))
		}
		switch c.Kind {
		case folderlook.KindIntegration:
			if folderlook.IntegrationInstalls(c) {
				plan.Lines = append(plan.Lines, personalassistant.FolderPlanLine{
					Kind: personalassistant.FolderPlanIntegration, Name: "Installs " + c.Name, Detail: folderlook.AppToolsSentence(c),
				})
			}
			plan.Lines = append(plan.Lines, personalassistant.FolderPlanLine{
				Kind: personalassistant.FolderPlanWorkspace, Name: "Connects " + c.Name + " to " + rec.WorkspaceName,
				Detail: rec.AgentName + " can use them in this workspace.",
			})
		case folderlook.KindHomeProvider:
			plan.Lines = append(plan.Lines, homeLines(rec, c)...)
		case folderlook.KindFileCurator:
			// File Janitor goes into this workspace in place (the folder is
			// already its anchor), and File Curator joins as its companion.
			plan.Lines = append(plan.Lines,
				personalassistant.FolderPlanLine{
					Kind: FolderPlanCapability, Name: "Adds File Janitor to " + rec.WorkspaceName,
					Detail: "Watching and the daily scan stay off until you turn them on in its setup.",
				},
				personalassistant.FolderPlanLine{
					Kind: personalassistant.FolderPlanAgents, Name: "Adds File Curator",
					Detail: "It explains proposed filing; you approve every move.",
				})
		case folderlook.KindSkill, folderlook.KindMCP, folderlook.KindPlugin:
			plan.Lines = append(plan.Lines, addOnLine(c, rec.WorkspaceName))
		}
	}
	// Each line carries the step it came from, so the card can show the steps
	// with their actions under them.
	for i, b := range bounds {
		end := len(plan.Lines)
		if i+1 < len(bounds) {
			end = bounds[i+1].start
		}
		for j := b.start; j < end; j++ {
			plan.Lines[j].Step = b.handle
		}
	}
	if stage != "" {
		plan.Stage = stage
	}
	plan.Runnable = len(plan.Lines) > 0
	if !plan.Runnable {
		switch {
		case len(steps) == 0:
			plan.Unavailable = "Ori found nothing it knows how to set up here."
		case selected != nil:
			plan.Unavailable = "Nothing is selected to set up."
		}
	}
	if rec.RootPolicy != folderlook.RootOrdinary {
		plan.DefaultReason = "Ori refreshed the setup choices for this folder. Review and confirm the current plan."
		if rec.RootPolicy == folderlook.RootUnresolved {
			plan.Unavailable = folderlook.PolicyLine(rec.RootPolicy)
		}
		for _, id := range selected {
			if _, ok := handleOf[strings.TrimSpace(id)]; !ok {
				plan.Runnable = false
				plan.Unavailable = "The plan changed. Reopen the report and confirm the current choices."
			}
		}
	}
	plan.Lines = append(plan.Lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanFolder, Name: assuranceLine(rec, plan.Candidates)})
	sort.Strings(plan.Selected)
	plan.Digest = planDigest(plan)
	return plan
}

// applyExcludes keeps the first of two steps that cannot run together (the
// card unticks the other before asking, so this is the safety net) and says
// which one was set aside.
func applyExcludes(order []string, steps []folderlook.Step, byHandle map[string]int) ([]string, string) {
	var kept []string
	taken := make(map[string]bool, len(order))
	excluded := ""
	for _, h := range order {
		step := steps[byHandle[h]]
		if step.Excludes != "" && taken[step.Excludes] {
			if excluded == "" {
				other := steps[byHandle[step.Excludes]]
				excluded = fmt.Sprintf("Ori can run one of these, not both. It keeps: %s. Set aside: %s.", other.Title, step.Title)
			}
			continue
		}
		taken[h] = true
		kept = append(kept, h)
	}
	return kept, excluded
}

// toolsBeforeHome moves the tools steps ahead of the Home step, keeping every
// other step where it was. The Home step's listing records which projects can
// be set up from the tools installed at that moment, and nothing refreshes it
// when the run ends; with the tools after it, a run that installs them still
// leaves the Home saying none can be set up.
func toolsBeforeHome(order []string, steps []folderlook.Step, byHandle map[string]int) []string {
	home := -1
	for i, h := range order {
		if steps[byHandle[h]].Kind == folderlook.StepHome {
			home = i
			break
		}
	}
	if home < 0 {
		return order
	}
	out := make([]string, 0, len(order))
	out = append(out, order[:home]...)
	var later []string
	for _, h := range order[home:] {
		if steps[byHandle[h]].Kind == folderlook.StepIntegration {
			out = append(out, h)
		} else {
			later = append(later, h)
		}
	}
	return append(out, later...)
}

// assuranceLine is the note under every phase of the plan (PRD FR13). It names
// the folder (its base name, never a path) so the promise is concrete, then
// what the plan adds (PRD FR25): the plugins it installs, the Home it creates
// and that Home's agents, naming only the parts that apply.
func assuranceLine(rec Record, candidates []folderlook.Candidate) string {
	folder := strings.TrimSpace(rec.FolderName)
	line := "Nothing in " + folder + " is moved, copied or read"
	if folder == "" || strings.ContainsAny(folder, "/\\\x00\r\n") {
		line = "Nothing is moved, copied or read"
	}
	if adds := addsSentence(candidates); adds != "" {
		line += ". " + adds
	}
	return line
}

// addsSentence is the assurance's second half: "Ori adds 2 plugins, <Home>,
// and its agents." Empty when the plan adds none of them.
func addsSentence(candidates []folderlook.Candidate) string {
	var parts []string
	if n := len(pluginInstalls(candidates)); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "plugin", "plugins")))
	}
	for _, c := range candidates {
		if c.Kind != folderlook.KindHomeProvider || !createsHome(c) {
			continue
		}
		parts = append(parts, homeName(c))
		if hiresAgents(c) {
			parts = append(parts, "its agents")
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return "Ori adds " + parts[0] + "."
	case 2:
		return "Ori adds " + parts[0] + " and " + parts[1] + "."
	}
	return "Ori adds " + strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1] + "."
}

// pluginInstalls are the plan's candidates whose step downloads and installs
// a plugin, in run order: an integration whose tools are not in place, a Home
// provider that is not ready, and a plugin add-on.
func pluginInstalls(candidates []folderlook.Candidate) []folderlook.Candidate {
	var out []folderlook.Candidate
	for _, c := range candidates {
		if installsPlugin(c) {
			out = append(out, c)
		}
	}
	return out
}

// installsPlugin reports whether a candidate's step downloads and installs a
// plugin. A plugin already installed, enabled and at the reviewed release is
// never one (PRD FR17).
func installsPlugin(c folderlook.Candidate) bool {
	switch c.Kind {
	case folderlook.KindIntegration:
		return folderlook.IntegrationInstalls(c)
	case folderlook.KindHomeProvider:
		return c.Home != nil && !c.Home.ProviderReady
	case folderlook.KindPlugin:
		return c.Action != folderlook.ActionAlreadyHere
	}
	return false
}

// createsHome reports whether the Home step makes a new Home rather than
// joining the owner's.
func createsHome(home folderlook.Candidate) bool {
	if home.Home != nil {
		return !home.Home.HomeExists
	}
	return home.Action != folderlook.ActionAlreadyHere
}

// hiresAgents reports whether the Home step hires any agent.
func hiresAgents(home folderlook.Candidate) bool {
	if home.Home == nil {
		return createsHome(home)
	}
	return !home.Home.RolesKnown || len(home.Home.MissingRoles) > 0
}

// defaultReason says why Ori's own plan is shown rather than the agent's.
func defaultReason(rec Record) string {
	if rec.Sections == nil {
		return ""
	}
	for _, dropped := range rec.Sections.Dropped {
		if dropped == folderlook.SectionPlan {
			return "The agent's plan could not be shown (it named a path or ran too long), so this is Ori's default plan."
		}
	}
	if rec.Sections.Plan != "" {
		return "The agent named no step Ori can run, so this is Ori's default plan."
	}
	return "The agent did not write a plan, so this is Ori's default plan."
}

// FolderPlanAddOn is the plan line kind of an add-on found by search.
const FolderPlanAddOn = "add_on"

// FolderPlanCapability is the plan line kind of a built-in capability
// installed in place (File Janitor for a folder of loose files).
const FolderPlanCapability = "capability"

// addOnLine words one selected add-on: "Installs <plugin>", "Adds skill
// <name>", "Adds <server>", each labelled "found by search" (PRD FR33).
func addOnLine(c folderlook.Candidate, workspaceName string) personalassistant.FolderPlanLine {
	name := c.Name
	switch c.Kind {
	case folderlook.KindPlugin:
		name = "Installs " + name
	case folderlook.KindSkill:
		name = "Adds skill " + name
	default:
		name = "Adds " + name
	}
	if c.Source == folderlook.SourceSearch {
		name += " · found by search"
	}
	detail := "For " + workspaceName + "."
	if c.Marketplace != "" {
		detail = "From " + c.Marketplace + ", for " + workspaceName + "."
	}
	return personalassistant.FolderPlanLine{Kind: FolderPlanAddOn, Name: name, Detail: detail}
}

func planDigest(plan Plan) string {
	lines := append([]personalassistant.FolderPlanLine{{Kind: "stage", Name: string(plan.Stage)}, {Kind: "source", Name: plan.Source}}, plan.Lines...)
	if plan.PolicyBinding != "" {
		lines = append(lines, personalassistant.FolderPlanLine{Kind: "root_policy", Name: plan.PolicyBinding})
	}
	if !plan.Runnable {
		lines = append(lines, personalassistant.FolderPlanLine{Kind: "unavailable", Name: plan.Unavailable})
	}
	for _, id := range plan.Selected {
		lines = append(lines, personalassistant.FolderPlanLine{Kind: "selected", Name: id})
	}
	// The install block is what the button confirms (PRD FR21): a plan whose
	// installs changed since the card was drawn is refused and redrawn.
	for _, e := range plan.Installs {
		lines = append(lines, personalassistant.FolderPlanLine{Kind: "install", Name: e.Step + "\x00" + e.Name + "\x00" + e.Version,
			Detail: e.Publisher + "\x00" + e.SourceURL + "\x00" + e.Marketplace})
	}
	return personalassistant.FolderPlanDigest(lines)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
