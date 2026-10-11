package projectinspect

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

const (
	purposeMaxRunes    = 600
	conventionMaxRunes = 1500
	maxCommands        = 24
)

// describe turns the documents that were read into the few facts a review
// shows: what the project says it is, what it is built with, which commands it
// declares, and what was missing. It only reads text already in memory.
func describe(e *Evidence) {
	stack := map[string]bool{}
	var instructions []string
	hasReadme := false
	for _, doc := range e.Documents {
		lower := strings.ToLower(doc.Path)
		switch doc.Class {
		case ClassReadme:
			if !hasReadme {
				hasReadme = true
				if text := opening(doc.Text, purposeMaxRunes); text != "" {
					e.Purpose = &Excerpt{Source: doc.Path, Text: text}
				}
			}
		case ClassInstructions:
			instructions = append(instructions, doc.Path)
			if text := clip(strings.TrimSpace(doc.Text), conventionMaxRunes); text != "" {
				e.Conventions = append(e.Conventions, Excerpt{Source: doc.Path, Text: text})
			}
		case ClassWorkflow:
			e.Commands = append(e.Commands, workflowCommands(doc)...)
		}
		switch lower {
		case "go.mod":
			stack["Go"] = true
			e.Commands = append(e.Commands,
				Command{Kind: "build", Command: "go build ./...", Source: doc.Path},
				Command{Kind: "test", Command: "go test ./...", Source: doc.Path})
		case "package.json":
			stack["Node.js"] = true
			commands, ok := packageScripts(doc)
			if !ok {
				e.Gaps = append(e.Gaps, Gap{Code: GapUnsupportedFile, Source: doc.Path})
			}
			e.Commands = append(e.Commands, commands...)
		case "cargo.toml":
			stack["Rust"] = true
			e.Commands = append(e.Commands,
				Command{Kind: "build", Command: "cargo build", Source: doc.Path},
				Command{Kind: "test", Command: "cargo test", Source: doc.Path})
		case "pyproject.toml", "requirements.txt":
			stack["Python"] = true
			if lower == "pyproject.toml" && strings.Contains(doc.Text, "[tool.pytest") {
				e.Commands = append(e.Commands, Command{Kind: "test", Command: "pytest", Source: doc.Path})
			}
		case "gemfile":
			stack["Ruby"] = true
		case "pom.xml", "build.gradle", "build.gradle.kts":
			stack["JVM"] = true
		case "package.swift":
			stack["Swift"] = true
		case "composer.json":
			stack["PHP"] = true
		case "cmakelists.txt":
			stack["C or C++ (CMake)"] = true
		case "makefile":
			e.Commands = append(e.Commands, makeTargets(doc)...)
		case "justfile":
			e.Commands = append(e.Commands, justRecipes(doc)...)
		}
	}
	for name := range stack {
		e.Stack = append(e.Stack, name)
	}
	sort.Strings(e.Stack)
	// The plainly named commands come first (make test before make test-unit),
	// so a reader who sees only the head of a long list sees the main ones.
	sort.SliceStable(e.Commands, func(i, j int) bool {
		return commandRank(e.Commands[i]) < commandRank(e.Commands[j])
	})
	if len(e.Commands) > maxCommands {
		e.Commands = e.Commands[:maxCommands]
	}

	if !hasReadme {
		e.Gaps = append(e.Gaps, Gap{Code: GapNoReadme})
	}
	switch len(instructions) {
	case 0:
		e.Gaps = append(e.Gaps, Gap{Code: GapNoInstructions})
	case 1:
	default:
		// Two guides can disagree, and nothing here can tell which one wins.
		e.Gaps = append(e.Gaps, Gap{Code: GapSeveralGuides, Source: strings.Join(instructions, ", ")})
	}
	declared := false
	for _, command := range e.Commands {
		declared = declared || command.Declared
	}
	if !declared {
		e.Gaps = append(e.Gaps, Gap{Code: GapNoCommands})
	}
}

// opening returns a document's first paragraph of prose, skipping headings,
// badges and markup lines.
func opening(text string, limit int) string {
	var paragraph []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		skip := line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "![") ||
			strings.HasPrefix(line, "[!") || strings.HasPrefix(line, "<") ||
			strings.Trim(line, "=-~*") == "" || line == WithheldLine
		if skip {
			if len(paragraph) > 0 && line == "" {
				break
			}
			// A title underlined with = or - (reStructuredText) is a heading too.
			if strings.Trim(line, "=-~") == "" && line != "" {
				paragraph = nil
			}
			continue
		}
		paragraph = append(paragraph, plainProse(line))
	}
	return clip(strings.TrimSpace(strings.Join(paragraph, " ")), limit)
}

// plainProse removes the markup a README's opening line is often wrapped in (a
// quote mark, emphasis), so the sentence reads as a sentence.
func plainProse(line string) string {
	line = strings.TrimSpace(strings.TrimLeft(line, "> "))
	for _, mark := range []string{"**", "__"} {
		line = strings.ReplaceAll(line, mark, "")
	}
	return strings.TrimSpace(strings.Trim(line, "*_"))
}

func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimRight(string(runes[:limit-1]), " ") + "…"
}

// commandRank orders commands for display: declared before inferred, a kind's
// plain name ("make test") before its variants ("make test-unit"), and build,
// test, lint, run in that order.
func commandRank(command Command) int {
	rank := 4
	switch command.Kind {
	case "build":
		rank = 0
	case "test":
		rank = 1
	case "lint":
		rank = 2
	case "run":
		rank = 3
	}
	name := command.Command[strings.LastIndex(command.Command, " ")+1:]
	if strings.ContainsAny(name, ":-") {
		rank += 10
	}
	if !command.Declared {
		rank += 100
	}
	return rank
}

// commandKind sorts a script or target name into the kinds a review lists.
func commandKind(name string) (string, bool) {
	lower := strings.ToLower(name)
	switch {
	case lower == "build" || strings.HasPrefix(lower, "build:") || strings.HasPrefix(lower, "build-") || lower == "compile":
		return "build", true
	case lower == "test" || strings.HasPrefix(lower, "test:") || strings.HasPrefix(lower, "test-") || lower == "check" || lower == "ci" || strings.HasPrefix(lower, "ci-"):
		return "test", true
	case lower == "lint" || strings.HasPrefix(lower, "lint:") || strings.HasPrefix(lower, "lint-") || lower == "vet" || lower == "fmt" || lower == "format" || lower == "typecheck":
		return "lint", true
	case lower == "start" || lower == "dev" || lower == "run" || lower == "serve" || strings.HasPrefix(lower, "run-"):
		return "run", true
	}
	return "", false
}

// packageScripts lists a package.json's build, test, lint and run scripts as
// the commands that invoke them. ok is false when the file is not the JSON
// object a package manifest is.
func packageScripts(doc Document) (commands []Command, ok bool) {
	var manifest struct {
		Scripts map[string]json.RawMessage `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(doc.Text), &manifest); err != nil {
		return nil, false
	}
	names := make([]string, 0, len(manifest.Scripts))
	for name := range manifest.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if kind, known := commandKind(name); known && plainName(name) {
			commands = append(commands, Command{Kind: kind, Command: "npm run " + name, Source: doc.Path, Declared: true})
		}
	}
	return commands, true
}

var (
	makeTarget = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*)\s*:([^=]|$)`)
	justRecipe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_-]*)(\s+[^:]*)?:\s*($|[^=])`)
	plain      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]*$`)
)

func plainName(name string) bool { return len(name) <= 60 && plain.MatchString(name) }

func targetsFrom(doc Document, pattern *regexp.Regexp, runner string) []Command {
	var commands []Command
	seen := map[string]bool{}
	for _, line := range strings.Split(doc.Text, "\n") {
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ".") {
			continue
		}
		match := pattern.FindStringSubmatch(line)
		if match == nil || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		if kind, known := commandKind(match[1]); known && plainName(match[1]) {
			commands = append(commands, Command{Kind: kind, Command: runner + " " + match[1], Source: doc.Path, Declared: true})
		}
	}
	return commands
}

func makeTargets(doc Document) []Command { return targetsFrom(doc, makeTarget, "make") }

func justRecipes(doc Document) []Command { return targetsFrom(doc, justRecipe, "just") }

// workflowCommands lists the single-line run steps of a workflow, so a review
// can say what the project's own automation runs. Being listed in a workflow
// says nothing about whether that workflow passes.
func workflowCommands(doc Document) []Command {
	var commands []Command
	for _, raw := range strings.Split(doc.Text, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "- "))
		step, found := strings.CutPrefix(line, "run:")
		step = strings.TrimSpace(step)
		if !found || step == "" || step == "|" || step == ">" || len(step) > 120 || step == WithheldLine {
			continue
		}
		commands = append(commands, Command{Kind: "other", Command: step, Source: doc.Path, Declared: true})
		if len(commands) >= 6 {
			break
		}
	}
	return commands
}
