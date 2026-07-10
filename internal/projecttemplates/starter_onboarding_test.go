package projecttemplates_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// TestReaperStarterSetupTask pins the converted reaper-song manifest: the
// intake-era onboarding block is gone, replaced by an agent roster and a
// single setup starter task ("defaults first, adjust conversationally").
func TestReaperStarterSetupTask(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "reaper-song")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}

	if tpl.HasOnboarding() {
		t.Fatal("reaper-song must not carry a legacy onboarding block")
	}
	if len(tpl.Warnings) != 0 {
		t.Fatalf("reaper-song should load without warnings, got %v", tpl.Warnings)
	}

	// Roster: exactly one producer entry agent with the REAPER skill bound.
	if len(tpl.Agents) != 1 || tpl.Agents[0].Name != "Reaper Producer" {
		t.Fatalf("unexpected roster: %+v", tpl.Agents)
	}
	if skills := tpl.Agents[0].Tools.Skills; len(skills) != 1 || skills[0] != "reaper-session-setup" {
		t.Fatalf("entry agent skills = %#v, want reaper-session-setup", tpl.Agents[0].Tools.Skills)
	}

	// Starter tasks: exactly one setup task, first in the list, framed as an
	// adjust task over the scaffolded defaults.
	if len(tpl.StarterTasks) != 2 {
		t.Fatalf("expected 2 starter tasks, got %+v", tpl.StarterTasks)
	}
	setup := tpl.StarterTasks[0]
	if !setup.Setup || tpl.StarterTasks[1].Setup {
		t.Fatalf("first task must be the only setup task: %+v", tpl.StarterTasks)
	}
	for _, heading := range []string{"## Created defaults", "## Questions to ask", "## Validation", "## How to apply changes"} {
		if !strings.Contains(setup.Details, heading) {
			t.Errorf("setup details missing %q heading", heading)
		}
	}
	for _, want := range []string{"120 BPM", "40 and 240", "reaper-session-setup"} {
		if !strings.Contains(setup.Details, want) {
			t.Errorf("setup details missing %q", want)
		}
	}

	// The skeleton still ships the .rpp at the documented defaults.
	if !tpl.HasSkeleton {
		t.Fatal("reaper-song must keep its skeleton")
	}
}
