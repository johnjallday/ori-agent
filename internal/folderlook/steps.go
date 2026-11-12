package folderlook

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// Step kinds: the host's runnable actions.
const (
	StepHome        = "home"
	StepIntegration = "integration"
	StepGroup       = "group"
	StepFileCurator = "file_curator"
	StepAddOn       = "add_on"
)

// HandleGroup is the group step's handle; the only step that is not a
// candidate.
const HandleGroup = "group"

const handleMaxLen = 32

// Step is one thing Ori can run for this folder, offered to the agent by a
// short handle it names inside its plan ("[project-tools]"). The agent writes
// the plan in its own words; Ori runs only the steps it recognises by handle.
type Step struct {
	Handle string `json:"handle"`
	Kind   string `json:"kind"`
	// Name is what the handle reads as inside the agent's words (the
	// integration's "<app> tools", the Home's name, "a group").
	Name string `json:"name"`
	// CandidateID is the candidate this step installs, connects or adds;
	// empty for the group step.
	CandidateID string `json:"candidate_id,omitempty"`
	// Title says what the step does, in the host's words.
	Title string `json:"title"`
	// Why says why the step is available here, in the host's words (or the
	// agent's ranking line for an add-on it ranked).
	Why string `json:"why,omitempty"`
	// Label marks a search hit ("Found by search · Skills").
	Label string `json:"label,omitempty"`
	// Default marks the steps of Ori's own plan, used when the agent names
	// no step or has no model.
	Default bool `json:"default,omitempty"`
	// Excludes is the handle this step cannot run together with.
	Excludes string `json:"excludes,omitempty"`
	// Tradeoff says in one line what the owner gives up by taking this step
	// instead of the one it excludes, in the host's words with the tables'
	// nouns. Only a step with an Excludes has one.
	Tradeoff string `json:"tradeoff,omitempty"`
	// Chosen is set on a plan's catalog when the step is in the plan.
	Chosen bool `json:"chosen,omitempty"`
}

// Steps is the catalog of steps Ori can run for this folder, from the facts
// and the candidates: the candidates in their order, then the group step.
// Only the host decides what is on it; the agent chooses among it.
func Steps(folderName string, facts foldermap.Facts, candidates []Candidate) []Step {
	stage := foldermap.StageFor(facts)
	handles := newHandles()
	var out []Step
	homeHandle := ""
	homeLabel := ""
	homeDefault := false
	for _, c := range candidates {
		if c.Action == ActionAlreadyHere && !c.Required {
			continue
		}
		step := Step{CandidateID: c.ID, Name: c.Name, Why: c.Why, Default: c.Required}
		switch c.Kind {
		case KindHomeProvider:
			step.Kind = StepHome
			songs := 0
			noun := "projects"
			if facts.Portfolio != nil {
				songs = facts.Portfolio.Count
				noun = foldermap.ShapeNoun(facts.Portfolio.Shape, songs)
			}
			// A Home the owner already has is joined by default; a new Home is
			// offered, for the agent to recommend, never built from a first
			// folder on its own.
			step.Default = c.Action == ActionAlreadyHere
			if c.Action == ActionAlreadyHere {
				step.Title = fmt.Sprintf("Add %s to your %s and list its %d %s on the shelf", folderName, c.Name, songs, noun)
			} else {
				step.Title = fmt.Sprintf("Create your %s and put %s on its shelf (%d %s)", c.Name, folderName, songs, noun)
				step.Why = strings.TrimSpace(step.Why + " One shelf for every " + foldermap.ShapeNoun(facts.Portfolio.Shape, 1) + " across your folders; worth it once there are several.")
			}
			homeDefault = step.Default
		case KindIntegration:
			step.Kind = StepIntegration
			// The title follows the plugin, not the application: the tools are
			// downloaded and installed unless they are already in place, even
			// when the application itself is on this Mac (PRD FR23).
			if IntegrationInstalls(c) {
				step.Title = fmt.Sprintf("Install %s and connect them to %s", c.Name, folderName)
			} else {
				step.Title = fmt.Sprintf("Connect %s to %s", c.Name, folderName)
			}
			if c.App != "" && c.Action == ActionInstall {
				step.Why = strings.TrimSpace(step.Why + " " + c.App + " is not on this Mac yet.")
			}
		case KindFileCurator:
			step.Kind = StepFileCurator
			step.Title = fmt.Sprintf("Add File Janitor to %s, with File Curator", folderName)
		case KindSkill, KindMCP, KindPlugin:
			step.Kind = StepAddOn
			step.Default = c.Ranked
			switch c.Kind {
			case KindPlugin:
				step.Title = "Install " + c.Name
			case KindSkill:
				step.Title = "Add the skill " + c.Name
			default:
				step.Title = "Add " + c.Name
			}
			if c.Source == SourceSearch && c.Marketplace != "" {
				step.Label = "Found by search · " + c.Marketplace
			}
		default:
			continue
		}
		step.Handle = handles.take(c.Name, step.Kind)
		if step.Kind == StepHome {
			homeHandle, homeLabel = step.Handle, c.Name
		}
		out = append(out, step)
	}
	if n := len(facts.Members); n >= foldermap.GroupMinMembers {
		// The folder becomes a place with its projects inside: the default
		// whenever no Home takes it (the Group stage, or a portfolio without a
		// Home yet).
		noun := memberNoun(facts)
		step := Step{
			Handle: HandleGroup, Kind: StepGroup, Name: "a group", Default: stage == foldermap.StageGroup || !homeDefault,
			Title: fmt.Sprintf("Turn %s into a group with one workspace per %s (%d)", folderName, noun, n),
			Why:   fmt.Sprintf("%d %s sit in their own folders here.", n, foldermap.ShapeNoun(memberShape(facts), n)),
		}
		if homeHandle != "" {
			step.Excludes = homeHandle
			// What each gives up, for whichever of the two is offered as the
			// alternative to the other (setup-card-clarity FR17).
			step.Tradeoff = fmt.Sprintf("One workspace per %s inside %s, and no %s: nothing lists %s across your other folders.",
				noun, folderName, homeLabel, foldermap.ShapeNoun(memberShape(facts), n))
			shelfShape := folderdigest.Shape("")
			if facts.Portfolio != nil {
				shelfShape = facts.Portfolio.Shape
			}
			shelfTradeoff := fmt.Sprintf("One shelf for every %s across your folders; %s get workspaces when you open them.",
				foldermap.ShapeNoun(shelfShape, 1), foldermap.ShapeNoun(shelfShape, 2))
			for i := range out {
				if out[i].Handle == homeHandle {
					out[i].Excludes = HandleGroup
					out[i].Tradeoff = shelfTradeoff
				}
			}
		}
		// The place comes first in the plan, then what goes into it; as an
		// alternative to the Home it is listed last.
		if step.Default {
			out = append([]Step{step}, out...)
		} else {
			out = append(out, step)
		}
	}
	return out
}

// memberShape is the one shape every member shares, or "" when they differ.
func memberShape(facts foldermap.Facts) folderdigest.Shape {
	shape := folderdigest.Shape("")
	for i, p := range facts.Members {
		if i == 0 {
			shape = p.Shape
		} else if p.Shape != shape {
			return ""
		}
	}
	return shape
}

// memberNoun names one member: "song", "draft", or "project" for a mix.
func memberNoun(facts foldermap.Facts) string {
	if shape := memberShape(facts); shape != "" {
		return foldermap.ShapeNoun(shape, 1)
	}
	return "project"
}

// DefaultLine is Ori's default plan in one line, from the catalog's default
// steps: what Set it up would do if the agent named nothing.
func DefaultLine(steps []Step) string {
	var titles []string
	optional := false
	for _, s := range steps {
		if s.Default {
			titles = append(titles, s.Title)
		} else if s.Kind == StepAddOn {
			optional = true
		}
	}
	switch {
	case len(titles) > 0:
		return "Set it up: " + strings.Join(titles, " · ")
	case len(steps) == 0:
		return "Nothing to set up yet: Ori found no project it knows here."
	case optional:
		return "Set it up: adds the tools you leave selected below"
	}
	return "Nothing is set up by default here; the steps below are offered."
}

// StepHandles is the set of handles the parser accepts.
func StepHandles(steps []Step) map[string]bool {
	out := make(map[string]bool, len(steps))
	for _, s := range steps {
		out[s.Handle] = true
	}
	return out
}

// PlanWords is the agent's plan as plain text: each offered handle reads as
// its step's name, any other handle-shaped token is removed, and the spaces
// they leave are tidied. The card draws the same text with the names as chips.
func PlanWords(text string, steps []Step) string {
	names := make(map[string]string, len(steps))
	for _, s := range steps {
		names[s.Handle] = s.Name
	}
	out := handlePattern.ReplaceAllStringFunc(text, func(token string) string {
		return names[handlePattern.FindStringSubmatch(token)[1]]
	})
	out = strings.ReplaceAll(out, "()", "")
	out = spaceRun.ReplaceAllString(out, " ")
	out = spaceBeforePunct.ReplaceAllString(out, "$1")
	return strings.TrimSpace(out)
}

// NameHandles writes each offered step's handle that keep does not hold as
// the step's name, leaving the kept handles (the card's chips) and any other
// token as they are. A finished card lists only the steps that ran; the
// agent's words still name the others.
func NameHandles(text string, steps []Step, keep map[string]bool) string {
	names := make(map[string]string, len(steps))
	for _, s := range steps {
		names[s.Handle] = s.Name
	}
	return handlePattern.ReplaceAllStringFunc(text, func(token string) string {
		handle := handlePattern.FindStringSubmatch(token)[1]
		if name, ok := names[handle]; ok && !keep[handle] {
			return name
		}
		return token
	})
}

var handleNoise = regexp.MustCompile(`[^a-z0-9]+`)

// handles issues unique, readable handles from candidate names.
type handles struct{ used map[string]bool }

func newHandles() *handles { return &handles{used: map[string]bool{HandleGroup: true}} }

func (h *handles) take(name, kind string) string {
	base := strings.Trim(handleNoise.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(base) > handleMaxLen {
		base = strings.Trim(base[:handleMaxLen], "-")
	}
	if base == "" {
		base = strings.ReplaceAll(kind, "_", "-")
	}
	handle := base
	for i := 2; h.used[handle]; i++ {
		handle = fmt.Sprintf("%s-%d", base, i)
	}
	h.used[handle] = true
	return handle
}
