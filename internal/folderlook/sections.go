// Package folderlook is the folder agent's bounded first look: the host's
// reference material, one capped model turn with the folder_look tool, the
// fixed report sections the host parses, and the gates that decide which of
// the model's words are shown. The model decides where to look and how to say
// it; everything with a consequence (projects, stage, tools, plan) is the
// host's, computed in internal/foldermap.
package folderlook

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Bounds of the report sections (PRD FR21, FR24, FR26).
const (
	WhatItIsMaxRunes  = 400
	GoalMaxRunes      = 300 // personalassistant.BriefMaxRunes
	TicketMaxRunes    = 200
	TicketWhyMaxRunes = 300
	ToolWhyMaxRunes   = 120
	QuestionMaxRunes  = 200
	MaxRankedTools    = 3
	PlanMaxRunes      = 600
)

// Section names, as the model writes them and the report records which were
// left out.
const (
	SectionWhatItIs = "what_it_is"
	SectionTools    = "tools"
	SectionGoal     = "suggested_goal"
	SectionTicket   = "suggested_first_ticket"
	SectionNext     = "next"
	SectionQuestion = "question"
	SectionPlan     = "plan"
)

// RankedTool is one candidate the model ranked, by the host's id.
type RankedTool struct {
	ID  string `json:"id"`
	Why string `json:"why"`
}

// Sections is what survived parsing. A field is empty when the model left
// it out or the host dropped it; Dropped names the dropped ones.
type Sections struct {
	WhatItIs  string       `json:"what_it_is,omitempty"`
	Tools     []RankedTool `json:"tools,omitempty"`
	Goal      string       `json:"suggested_goal,omitempty"`
	Ticket    string       `json:"suggested_first_ticket,omitempty"`
	TicketWhy string       `json:"suggested_first_ticket_why,omitempty"`
	Next      string       `json:"next,omitempty"`
	Question  string       `json:"question,omitempty"`
	// Plan is the agent's plan in its own words, handles and all (PlanWords
	// renders it); PlanSteps are the offered handles it named as steps to
	// run, in its order, each once. A handle it names only to leave out
	// ("leaving out [group]") is not one of them. Everything else in Plan is
	// shown to the owner as the agent's words and never run.
	Plan      string   `json:"plan,omitempty"`
	PlanSteps []string `json:"plan_steps,omitempty"`
	Dropped   []string `json:"dropped,omitempty"`
	// Provenance of everything above (PRD FR11), set by the host.
	Source   string `json:"source,omitempty"`
	Model    string `json:"model,omitempty"`
	TicketID string `json:"ticket_id,omitempty"`
}

var headingPattern = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?\**\s*(what it is|tools|suggested goal|suggested first ticket|question|next|plan)\s*\**\s*:?\s*\**\s*(.*)$`)

var headingKeys = map[string]string{
	"what it is": SectionWhatItIs, "tools": SectionTools, "suggested goal": SectionGoal,
	"suggested first ticket": SectionTicket, "question": SectionQuestion, "next": SectionNext,
	"plan": SectionPlan,
}

// ParseOptions carries the host facts the parser checks the model's words
// against.
type ParseOptions struct {
	// Candidates are the host-issued tool ids the model may rank.
	Candidates map[string]bool
	// Steps are the handles of the steps the host offered for the plan.
	Steps map[string]bool
	// FolderPaths are the map's relative paths that contain a slash; a section
	// quoting one names a path and is dropped.
	FolderPaths []string
}

// Parse reads the model's reply into the fixed sections. A section that
// names a path or exceeds its bound is dropped whole, never trimmed; a tool
// line naming an id the host did not issue is dropped alone.
func Parse(reply string, opts ParseOptions) Sections {
	raw := splitSections(reply)
	var out Sections
	drop := func(section string) { out.Dropped = append(out.Dropped, section) }
	keep := func(section, text string, limit int) string {
		text = oneLine(text)
		if text == "" {
			return ""
		}
		if utf8.RuneCountInString(text) > limit || namesPath(text, opts.FolderPaths) {
			drop(section)
			return ""
		}
		return text
	}
	out.WhatItIs = keep(SectionWhatItIs, raw[SectionWhatItIs], WhatItIsMaxRunes)
	out.Goal = keep(SectionGoal, raw[SectionGoal], GoalMaxRunes)
	out.Question = keep(SectionQuestion, raw[SectionQuestion], QuestionMaxRunes)
	out.Next = keep(SectionNext, raw[SectionNext], TicketMaxRunes)
	if plan := oneLine(raw[SectionPlan]); plan != "" {
		if utf8.RuneCountInString(plan) > PlanMaxRunes || namesPath(plan, opts.FolderPaths) {
			drop(SectionPlan)
		} else {
			out.Plan, out.PlanSteps = parsePlan(plan, opts.Steps)
		}
	}

	if ticket := strings.TrimSpace(raw[SectionTicket]); ticket != "" {
		lines := nonEmptyLines(ticket)
		title, why := lines[0], strings.Join(lines[1:], " ")
		if why == "" {
			title, why = splitFirstSentence(title)
		}
		title, why = oneLine(trimBullet(title)), oneLine(why)
		if utf8.RuneCountInString(title) > TicketMaxRunes || utf8.RuneCountInString(why) > TicketWhyMaxRunes ||
			namesPath(title+" "+why, opts.FolderPaths) {
			drop(SectionTicket)
		} else {
			out.Ticket, out.TicketWhy = title, why
		}
	}
	if out.Next == "" && out.Ticket != "" && !contains(out.Dropped, SectionNext) {
		out.Next = out.Ticket
	}

	if tools := strings.TrimSpace(raw[SectionTools]); tools != "" {
		seen := make(map[string]bool)
		for _, line := range nonEmptyLines(tools) {
			id, why := splitToolLine(trimBullet(line))
			if id == "" || !opts.Candidates[id] || seen[id] {
				continue
			}
			why = oneLine(why)
			if utf8.RuneCountInString(why) > ToolWhyMaxRunes || namesPath(why, opts.FolderPaths) {
				continue
			}
			seen[id] = true
			out.Tools = append(out.Tools, RankedTool{ID: id, Why: why})
			if len(out.Tools) == MaxRankedTools {
				break
			}
		}
	}
	return out
}

// splitSections maps each heading to the text under it. Text before the
// first heading is ignored.
func splitSections(reply string) map[string]string {
	out := make(map[string]string)
	current := ""
	var buf []string
	flush := func() {
		if current != "" {
			if _, seen := out[current]; !seen {
				out[current] = strings.TrimSpace(strings.Join(buf, "\n"))
			}
		}
		buf = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(reply, "\r\n", "\n"), "\n") {
		if m := headingPattern.FindStringSubmatch(line); m != nil && isHeadingLine(line) {
			flush()
			current = headingKeys[strings.ToLower(m[1])]
			if rest := strings.TrimSpace(strings.Trim(m[2], "*")); rest != "" {
				buf = append(buf, rest)
			}
			continue
		}
		if current != "" {
			buf = append(buf, line)
		}
	}
	flush()
	return out
}

// isHeadingLine keeps "Next: …" and "## Tools" as headings but not a
// sentence that merely starts with one of the words ("Tools like these…").
func isHeadingLine(line string) bool {
	trimmed := strings.TrimLeft(strings.TrimSpace(line), "#* ")
	lower := strings.ToLower(trimmed)
	for heading := range headingKeys {
		if !strings.HasPrefix(lower, heading) {
			continue
		}
		rest := strings.TrimLeft(trimmed[len(heading):], "* ")
		if rest == "" || strings.HasPrefix(rest, ":") {
			return true
		}
	}
	return false
}

var toolLinePattern = regexp.MustCompile(`^\[?([a-z0-9][a-z0-9._:/@-]*)\]?\s*(?:[—–:-]+\s*)?(.*)$`)

// splitToolLine reads "id — why" / "id: why" / "[id] why".
func splitToolLine(line string) (string, string) {
	line = strings.Trim(strings.TrimSpace(line), "`*")
	m := toolLinePattern.FindStringSubmatch(strings.ToLower(line[:min(len(line), 80)]))
	if m == nil {
		return "", ""
	}
	id := m[1]
	why := strings.TrimSpace(line[len(id):])
	why = strings.TrimSpace(strings.TrimLeft(why, "]`*—–:- "))
	return id, why
}

// namesPath reports text that names a place on disk: the hire card's rule
// (leading slash or tilde, a backslash, a slash-led word) or one of the map's
// own nested folder paths.
// NamesPath is the parser's path rule, for other host writes of model text
// (the memory note).
func NamesPath(text string, folderPaths []string) bool { return namesPath(text, folderPaths) }

func namesPath(text string, folderPaths []string) bool {
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, "~") || strings.Contains(text, "\\") ||
		strings.Contains(text, " /") || strings.Contains(text, " ~/") || strings.Contains(text, "(/") {
		return true
	}
	for _, p := range folderPaths {
		if strings.Contains(p, "/") && strings.Contains(text, p) {
			return true
		}
	}
	return false
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func trimBullet(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* ", "• ", "1. ", "2. ", "3. "} {
		line = strings.TrimPrefix(line, prefix)
	}
	return strings.TrimSpace(line)
}

func nonEmptyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

var (
	handlePattern    = regexp.MustCompile(`\[([a-z0-9][a-z0-9-]*)\]`)
	spaceRun         = regexp.MustCompile(`\s+`)
	spaceBeforePunct = regexp.MustCompile(`\s+([.,;:!?)])`)
	// leftOutBefore matches the words right before a handle that name it
	// only to leave it out.
	leftOutBefore = regexp.MustCompile(`(?i)(?:leav(?:e|es|ing)\s+out|left\s+out|skip(?:s|ping)?|not|no|without|rather\s+than|instead\s+of|drop(?:s|ping)?)\s+(?:the\s+|a\s+|an\s+)?$`)
)

// parsePlan reads the agent's plan: its words as written, and the offered
// handles it named as steps to run, in order and once each. The agent may
// name a step inline or in a trailing list; a handle right after "leaving
// out", "not", "skip" or the like is a mention, not a choice.
func parsePlan(text string, steps map[string]bool) (words string, handles []string) {
	seen := make(map[string]bool)
	for _, loc := range handlePattern.FindAllStringSubmatchIndex(text, -1) {
		handle := text[loc[2]:loc[3]]
		if !steps[handle] || seen[handle] || leftOutBefore.MatchString(text[:loc[0]]) {
			continue
		}
		seen[handle] = true
		handles = append(handles, handle)
	}
	return text, handles
}

// splitFirstSentence splits "Do X. Because Y." into ("Do X.", "Because Y.").
func splitFirstSentence(text string) (string, string) {
	for i, r := range text {
		if (r == '.' || r == '!' || r == '?') && i+1 < len(text) && text[i+1] == ' ' {
			return strings.TrimSpace(text[:i+1]), strings.TrimSpace(text[i+1:])
		}
	}
	return text, ""
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}
