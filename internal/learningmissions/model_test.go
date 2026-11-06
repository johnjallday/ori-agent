package learningmissions

import (
	"errors"
	"strings"
	"testing"
)

func TestStructuresAreClosedVersionedAndDetached(t *testing.T) {
	for _, id := range []StructureID{DelegateInspect, ImproveResult, RepeatSuccess} {
		s, err := Lookup(id, 1)
		if err != nil || s.Criteria == "" || len(s.Steps) < 3 {
			t.Fatalf("structure: %+v %v", s, err)
		}
		s.Steps[0] = "grant_permissions"
		fresh, _ := Lookup(id, 1)
		if fresh.Steps[0] == s.Steps[0] {
			t.Fatal("caller changed the registry")
		}
		if _, err := Lookup(id, 2); !errors.Is(err, ErrUnsupported) {
			t.Fatal("unrecognized version was accepted")
		}
	}
	if _, err := Lookup("arbitrary_composition", 1); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unknown structure was accepted")
	}
}

func TestPreferencesRequireSeparateExplicitScopesAndConsent(t *testing.T) {
	for name, change := range map[string]func(*Preferences){
		"unknown mode":     func(p *Preferences) { p.Mode = "always" },
		"no scope":         func(p *Preferences) { p.Observe = true },
		"no timestamp":     func(p *Preferences) { p.Observe = true; p.WorkspaceIDs = []string{"ws"}; p.ConsentVersion = 1 },
		"no model consent": func(p *Preferences) { p.Mode = "automatic"; p.WorkspaceIDs = []string{"ws"}; p.ConsentVersion = 1 },
		"duplicates":       func(p *Preferences) { p.WorkspaceIDs = []string{"ws", "ws"} },
		"too many scopes":  func(p *Preferences) { p.WorkspaceIDs = []string{"a", "b", "c", "d", "e", "f"} },
		"long focus":       func(p *Preferences) { p.Focus = strings.Repeat("界", 301) },
		"unknown support":  func(p *Preferences) { p.Guidance = GuidanceUnknown },
	} {
		t.Run(name, func(t *testing.T) {
			p := DefaultPreferences()
			change(&p)
			if p.Validate() == nil {
				t.Fatal("invalid preference accepted")
			}
		})
	}
	if DefaultPreferences().Validate() != nil {
		t.Fatal("default on-request with no scopes must be valid and inert")
	}
}

func TestTerminalInstancesCannotResume(t *testing.T) {
	for _, from := range []State{Completed, Deferred, Declined, Stopped, Invalidated} {
		for _, to := range []State{Offered, Active, Blocked, Completed} {
			if CanTransition(from, to) {
				t.Fatalf("terminal resurrection: %s -> %s", from, to)
			}
		}
	}
	if CanTransition(Offered, Completed) || CanTransition(Blocked, Completed) {
		t.Fatal("completion bypassed active evidence verification")
	}
}
