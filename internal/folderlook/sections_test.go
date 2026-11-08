package folderlook

import (
	"reflect"
	"strings"
	"testing"
)

var parseOpts = ParseOptions{
	Candidates:  map[string]bool{"integration:demo": true, "home:music": true, "skill:mixing": true, "skill:two": true},
	FolderPaths: []string{"Albums/Album-1", "Picker Songs"},
}

func TestParse_AllSections(t *testing.T) {
	got := Parse(report+"\nQuestion: Which folder first?", parseOpts)
	want := Sections{
		WhatItIs:  "Seven songs and a sketch, across two album folders.",
		Tools:     []RankedTool{{ID: "integration:demo", Why: "Lets the agent read the sessions."}},
		Goal:      "Keep these songs moving toward release.",
		Ticket:    "List the five songs saved most recently.",
		TicketWhy: "Seeing what moved last shows where to start.",
		Next:      "List the five songs saved most recently and what each still needs.",
		Question:  "Which folder first?",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed:\n%+v\nwant:\n%+v", got, want)
	}
}

func TestParse_MarkdownHeadingsAndSameLineTicketWhy(t *testing.T) {
	reply := "Here you go.\n## What it is\nA repository.\n**Tools:**\n1. `skill:mixing`: helps mix\n" +
		"### Suggested first ticket\nRun the tests. They show what works.\n"
	got := Parse(reply, parseOpts)
	if got.WhatItIs != "A repository." || got.Ticket != "Run the tests." || got.TicketWhy != "They show what works." {
		t.Fatalf("parsed = %+v", got)
	}
	if len(got.Tools) != 1 || got.Tools[0].ID != "skill:mixing" || got.Tools[0].Why != "helps mix" {
		t.Fatalf("tools = %+v", got.Tools)
	}
	// Next falls back to the ticket.
	if got.Next != "Run the tests." {
		t.Fatalf("next = %q", got.Next)
	}
}

func TestParse_DropsPathsAndOverlongSections(t *testing.T) {
	reply := "What it is: Songs under Albums/Album-1 and more.\n" +
		"Suggested goal: " + strings.Repeat("g", GoalMaxRunes+1) + "\n" +
		"Suggested first ticket: Open /Users/me/Music to check.\nWhy.\n" +
		"Question: Look in ~/Downloads?\n" +
		"Next: Tidy up."
	got := Parse(reply, parseOpts)
	if got.WhatItIs != "" || got.Goal != "" || got.Ticket != "" || got.Question != "" {
		t.Fatalf("kept a dropped section: %+v", got)
	}
	want := []string{SectionWhatItIs, SectionGoal, SectionQuestion, SectionTicket}
	if !reflect.DeepEqual(got.Dropped, want) {
		t.Fatalf("dropped = %v, want %v", got.Dropped, want)
	}
	if got.Next != "Tidy up." {
		t.Fatalf("next = %q", got.Next)
	}
}

func TestParse_ToolsOnlyFromCandidatesAtMostThree(t *testing.T) {
	reply := "Tools:\n- photoshop — not issued\n- skill:mixing — one\n- skill:mixing — duplicate\n" +
		"- home:music — two\n- integration:demo — " + strings.Repeat("w", ToolWhyMaxRunes+1) + "\n" +
		"- skill:two — three\n- integration:demo — four\n"
	got := Parse(reply, parseOpts)
	var ids []string
	for _, tool := range got.Tools {
		ids = append(ids, tool.ID)
	}
	if strings.Join(ids, ",") != "skill:mixing,home:music,skill:two" {
		t.Fatalf("ranked = %v", ids)
	}
}

func TestParse_SentenceStartingWithAHeadingWordIsNotAHeading(t *testing.T) {
	got := Parse("What it is: A mix folder.\nTools like these help, but none apply.\n", parseOpts)
	if got.WhatItIs != "A mix folder. Tools like these help, but none apply." || len(got.Tools) != 0 {
		t.Fatalf("parsed = %+v", got)
	}
}
