package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

func TestExecutionPromptsRequireFullPlanAndDependencyPreflight(t *testing.T) {
	t.Parallel()
	for _, assessment := range []struct {
		name string
		text string
	}{
		{name: "host only", text: "Assessment: not required — host-only change."},
		{name: "companion not prepared", text: "Assessment: required — plugin manifest change.\nOwner: unassigned. Worktree: not prepared."},
		{name: "unresolved source", text: "Assessment: unresolved — canonical plugin source unavailable."},
		{name: "prepared companion", text: "Assessment: required.\nCompanion owner: separate builder. Worktree: /private/companion-evidence-only."},
		{name: "legacy plan without assessment"},
	} {
		t.Run(assessment.name, func(t *testing.T) {
			path := t.TempDir()
			skillPath := filepath.Join(path, ".agents", "skills", "task-planning", "SKILL.md")
			taskPath := filepath.Join(path, "tasks", "tasks-bridge.md")
			for _, dir := range []string{filepath.Dir(skillPath), filepath.Dir(taskPath)} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(skillPath, []byte("# Task Planning\n\n## Implementation dependency preflight\n"), 0600); err != nil {
				t.Fatal(err)
			}
			contents := "## Execution Topology\n\nprivate topology evidence\n\n"
			if assessment.text != "" {
				contents += "## Companion Dependencies\n\n" + assessment.text + "\n\n"
			}
			// A dependency beyond the preview's limit must still be read from the
			// full plan, not silently lost when the next item is truncated.
			contents += "## Tasks\n\n- [ ] 1.1 Revalidate " + strings.Repeat("source facts ", 30) + "private task tail\n"
			if err := os.WriteFile(taskPath, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			feature := model.Feature{Name: "bridge", Path: path}
			for name, render := range map[string]func(model.Feature, string) string{
				"bootstrap": BootstrapPrompt, "continuation": ContinuationPrompt,
			} {
				t.Run(name, func(t *testing.T) {
					prompt := render(feature, "builder")
					for _, want := range []string{
						"Work only in this Git worktree: " + path,
						"Task checklist: " + taskPath,
						"Execution workflow: " + skillPath + " (implementation dependency preflight)",
						"Before editing, read the full planning artifacts, including Execution Topology and Companion Dependencies when present",
						"the next-item preview may be truncated",
						"After the dependency preflight",
						"next safe in-scope task",
						"Do not create or remove Git worktrees.",
						"wt done bridge",
						"…",
					} {
						if !strings.Contains(prompt, want) {
							t.Errorf("prompt missing %q:\n%s", want, prompt)
						}
					}
					// The renderer references evidence, not a guessed readiness state
					// or a companion path promoted into authorization to write there.
					for _, private := range []string{"private topology evidence", "private task tail", "/private/companion-evidence-only", "Assessment:"} {
						if strings.Contains(prompt, private) {
							t.Errorf("prompt embedded plan evidence %q:\n%s", private, prompt)
						}
					}
					if strings.Index(prompt, "After the dependency preflight") < strings.Index(prompt, "Execution workflow:") {
						t.Errorf("prompt starts work before naming the execution workflow:\n%s", prompt)
					}
				})
			}
			got, err := os.ReadFile(taskPath)
			if err != nil || string(got) != contents {
				t.Fatalf("prompt rendering changed the plan: contents=%q err=%v", got, err)
			}
		})
	}
}

func TestExecutionPromptsReportUnavailableWorkflow(t *testing.T) {
	t.Parallel()
	for _, skillState := range []string{"missing", "directory"} {
		t.Run(skillState, func(t *testing.T) {
			feature := model.Feature{Name: "bridge", Path: t.TempDir()}
			skillPath := filepath.Join(feature.Path, ".agents", "skills", "task-planning", "SKILL.md")
			if skillState == "directory" {
				if err := os.MkdirAll(skillPath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for name, render := range map[string]func(model.Feature, string) string{
				"bootstrap": BootstrapPrompt, "continuation": ContinuationPrompt,
			} {
				t.Run(name, func(t *testing.T) {
					prompt := render(feature, "builder")
					want := "Execution workflow unavailable: " + skillPath + "; resolve repository guidance before editing."
					if !strings.Contains(prompt, want) {
						t.Errorf("prompt must report unavailable workflow without inventing readiness:\n%s", prompt)
					}
					if !strings.Contains(prompt, "No detailed task list was found") {
						t.Errorf("prompt must still report the missing task list:\n%s", prompt)
					}
				})
			}
		})
	}
}
