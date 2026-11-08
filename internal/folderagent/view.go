package folderagent

import (
	"context"
	"fmt"
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
	// Selected are the optional candidates this plan includes.
	Selected []string `json:"selected,omitempty"`
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
	v.StageLine = StageLine(rec.Facts, rec.Stage, rec.Candidates)
	v.Candidates = rec.Candidates
	v.NextFolders = nextFolders(rec.Facts)
	v.ModelSentence = modelSentence(rec)
	sectionsInto(&v, rec)
	if rec.Turn == nil || rec.Turn.Status != TurnFinished {
		v.ToolsSentence = "Search for more tools needs a model"
	}
	plan := BuildPlan(*rec, nil)
	v.Plan = &PlanView{Stage: plan.Stage, Lines: plan.Lines, Digest: plan.Digest, Runnable: plan.Runnable, Unavailable: plan.Unavailable, Selected: plan.Selected}
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

// StageLine says what Set it up would do, in the host's words (PRD FR24).
func StageLine(f foldermap.Facts, stage foldermap.Stage, candidates []folderlook.Candidate) string {
	var parts []string
	for _, c := range candidates {
		if !c.Required {
			continue
		}
		switch c.Kind {
		case folderlook.KindHomeProvider:
			parts = append(parts, c.Detail)
		case folderlook.KindIntegration:
			if c.Action == folderlook.ActionAlreadyHere {
				parts = append(parts, "connects "+c.Name)
			} else {
				parts = append(parts, "installs "+c.Name)
			}
		case folderlook.KindFileCurator:
			parts = append(parts, "adds File Curator to keep the loose files tidy")
		}
	}
	if stage == foldermap.StageGroup {
		parts = append([]string{groupStageLine(f)}, parts...)
	}
	if len(parts) == 0 {
		if stage == foldermap.StageFolder {
			return "Nothing to set up yet: Ori found no project it knows here."
		}
		for _, c := range candidates {
			if !c.Required && c.Action != folderlook.ActionAlreadyHere {
				return "Set it up: adds the tools you leave selected below"
			}
		}
		return "Nothing to set up for this project yet."
	}
	return "Set it up: " + strings.Join(parts, ", ")
}

// Plan is a Set it up plan: the host's lines for the stage and the selected
// candidates.
type Plan struct {
	Stage      foldermap.Stage
	Lines      []personalassistant.FolderPlanLine
	Candidates []folderlook.Candidate
	Selected   []string
	Digest     string
	Runnable   bool
	// Unavailable is why the plan cannot run yet.
	Unavailable string
}

// BuildPlan lists every consequence of Set it up. Required candidates are
// always in it; optional ones only when selected.
//
// selected nil means the default: every optional candidate the agent ranked,
// except what is already here. An explicit list (even empty) replaces it;
// ids that are not optional candidates are ignored.
func BuildPlan(rec Record, selected []string) Plan {
	plan := Plan{Stage: rec.Stage}
	chosen := make(map[string]bool, len(selected))
	if selected == nil {
		for _, c := range rec.Candidates {
			if !c.Required && c.Ranked && c.Action != folderlook.ActionAlreadyHere {
				chosen[c.ID] = true
			}
		}
	}
	for _, id := range selected {
		chosen[id] = true
	}
	runnable := rec.Stage == foldermap.StageTools || rec.Stage == foldermap.StageHome || rec.Stage == foldermap.StageGroup
	for _, c := range rec.Candidates {
		if !c.Required && (!chosen[c.ID] || c.Action == folderlook.ActionAlreadyHere) {
			continue
		}
		plan.Candidates = append(plan.Candidates, c)
		if !c.Required {
			plan.Selected = append(plan.Selected, c.ID)
		}
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
			plan.Lines = append(plan.Lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanAgents, Name: "Adds File Curator", Detail: c.Detail})
			runnable = false
			plan.Unavailable = "Adding File Curator from a folder is not available yet."
		case folderlook.KindSkill, folderlook.KindMCP, folderlook.KindPlugin:
			plan.Lines = append(plan.Lines, addOnLine(c, rec.WorkspaceName))
		default:
			runnable = false
		}
	}
	if rec.Stage == foldermap.StageGroup {
		plan.Lines = append(groupLines(rec), plan.Lines...)
	}
	if len(plan.Lines) == 0 {
		runnable = false
		if plan.Unavailable == "" && rec.Stage == foldermap.StageFolder {
			plan.Unavailable = "Ori found nothing it knows how to set up here."
		}
	}
	plan.Lines = append(plan.Lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanFolder, Name: "Nothing is moved, copied or read"})
	plan.Runnable = runnable && rec.Stage != foldermap.StageFolder
	sort.Strings(plan.Selected)
	plan.Digest = planDigest(plan)
	return plan
}

// FolderPlanAddOn is the plan line kind of an add-on found by search.
const FolderPlanAddOn = "add_on"

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
	lines := append([]personalassistant.FolderPlanLine{{Kind: "stage", Name: string(plan.Stage)}}, plan.Lines...)
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
