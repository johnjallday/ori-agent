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
}

func planViewOf(plan Plan) *PlanView {
	steps := plan.Steps
	if steps == nil {
		steps = []folderlook.Step{}
	}
	return &PlanView{Stage: plan.Stage, Lines: plan.Lines, Digest: plan.Digest, Runnable: plan.Runnable,
		Unavailable: plan.Unavailable, Selected: plan.Selected, Source: plan.Source, Words: plan.Words,
		WordsRaw: plan.WordsRaw, Steps: steps, Excluded: plan.Excluded, DefaultReason: plan.DefaultReason}
}

func (s *Service) viewOf(_ context.Context, rec *Record) View {
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
	v.StageLine = StageLine(rec.FolderName, rec.Facts, rec.Candidates)
	v.Candidates = rec.Candidates
	v.NextFolders = nextFolders(rec.Facts)
	v.ModelSentence = modelSentence(rec)
	sectionsInto(&v, rec)
	if rec.Turn == nil || rec.Turn.Status != TurnFinished {
		v.ToolsSentence = "Search for more tools needs a model"
	}
	// Once a run exists the plan shown is the one it was confirmed on, not
	// the agent's recommendation (a stopped run carries on with it).
	var selected []string
	if rec.Setup != nil && len(rec.Setup.Selected) > 0 {
		selected = rec.Setup.Selected
	}
	plan := BuildPlan(*rec, selected)
	v.Plan = planViewOf(plan)
	v.Landing = landingOf(rec, plan)
	if s.goalOffered(rec) {
		v.GoalOffer = &GoalOfferView{Goal: rec.Sections.Goal, MaxRunes: personalassistant.BriefMaxRunes}
	}
	switch rec.State {
	case StateReported:
		v.Actions = []string{ActionSetUp, ActionKeep, ActionLookFurther}
	case StateKept:
		v.Actions = []string{ActionSetUp, ActionLookFurther}
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
	Stage      foldermap.Stage
	Lines      []personalassistant.FolderPlanLine
	Candidates []folderlook.Candidate
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
}

// BuildPlan lists every consequence of Set it up. The steps come from the
// agent's plan (the handles it named, in its order), else from Ori's default
// (the stage's required steps and the add-ons the agent ranked). selected,
// when not nil, is the owner's own choice among the offered steps, by handle
// (a candidate id is accepted too), and replaces both.
func BuildPlan(rec Record, selected []string) Plan {
	plan := Plan{Stage: rec.Stage, Source: PlanByOri}
	steps := folderlook.Steps(rec.FolderName, rec.Facts, rec.Candidates)
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
	if rec.Sections != nil {
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
	order = toolsBeforeHome(order, steps, byHandle)
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
		switch c.Kind {
		case folderlook.KindIntegration:
			if c.Action != folderlook.ActionAlreadyHere {
				detail := ""
				if c.Action == folderlook.ActionInstall && c.App != "" {
					detail = c.App + " itself is not on this Mac yet."
				} else if c.App != "" {
					detail = c.App + " is installed on this Mac."
				}
				plan.Lines = append(plan.Lines, personalassistant.FolderPlanLine{
					Kind: personalassistant.FolderPlanIntegration, Name: "Installs " + c.Name, Detail: detail,
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
	plan.Lines = append(plan.Lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanFolder, Name: assuranceLine(rec)})
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
// the folder (its base name, never a path) so the promise is concrete.
func assuranceLine(rec Record) string {
	folder := strings.TrimSpace(rec.FolderName)
	if folder == "" || strings.ContainsAny(folder, "/\\\x00\r\n") {
		return "Nothing is moved, copied or read"
	}
	return "Nothing in " + folder + " is moved, copied or read"
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
	for _, id := range plan.Selected {
		lines = append(lines, personalassistant.FolderPlanLine{Kind: "selected", Name: id})
	}
	return personalassistant.FolderPlanDigest(lines)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
