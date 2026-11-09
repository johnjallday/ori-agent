package server

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// hireRun keeps plan lines by name and moves them the way the runner's
// setLine does, recording every move.
type hireRun struct {
	lines map[string]*personalassistant.FolderPlanLine
	order []string
	moves []string
}

func newHireRun(names ...string) *hireRun {
	r := &hireRun{lines: map[string]*personalassistant.FolderPlanLine{}}
	for _, name := range names {
		r.lines[name] = &personalassistant.FolderPlanLine{Kind: folderagent.FolderPlanHire, Name: name}
		r.order = append(r.order, name)
	}
	return r
}

func (r *hireRun) setLine(name, state, progress string) {
	line, ok := r.lines[name]
	if !ok {
		return
	}
	line.Mark(state, progress, time.Now().UTC())
	r.moves = append(r.moves, name+" → "+state+" "+progress)
}

func (r *hireRun) final() []string {
	var out []string
	for _, name := range r.order {
		l := r.lines[name]
		out = append(out, name+": "+l.State+" "+l.Progress)
	}
	return out
}

func roles(names ...string) []foldersetup.HomeRole {
	var out []foldersetup.HomeRole
	for _, name := range names {
		out = append(out, foldersetup.HomeRole{RoleID: strings.ToLower(name), Name: name})
	}
	return out
}

func TestHireFolderAgentRoles_EachNamedRoleIsItsOwnLine(t *testing.T) {
	run := newHireRun("Hires Portfolio Manager", "Hires Library Manager")
	var staffed []string
	_, err := hireFolderAgentRoles(roles("Portfolio Manager", "Library Manager"), run.order, "Adds Albums", run.setLine,
		func(role foldersetup.HomeRole) error { staffed = append(staffed, role.Name); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Hires Portfolio Manager → working Hiring Portfolio Manager",
		"Hires Portfolio Manager → done Hired Portfolio Manager",
		"Hires Library Manager → working Hiring Library Manager",
		"Hires Library Manager → done Hired Library Manager",
	}
	if strings.Join(run.moves, "\n") != strings.Join(want, "\n") {
		t.Fatalf("moves:\n%s", strings.Join(run.moves, "\n"))
	}
	if strings.Join(staffed, ",") != "Portfolio Manager,Library Manager" {
		t.Fatalf("staffed = %v", staffed)
	}
}

func TestHireFolderAgentRoles_TheGenericLineNamesEachRoleAsItHires(t *testing.T) {
	run := newHireRun(folderagent.HomeRolesLine)
	if _, err := hireFolderAgentRoles(roles("Portfolio Manager", "Library Manager"), run.order, "Adds Albums", run.setLine,
		func(foldersetup.HomeRole) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(run.final(), "\n"); got != "Fills the Home's roles: done Hired Portfolio Manager · Library Manager" {
		t.Fatalf("final = %s", got)
	}
	if run.moves[0] != "Fills the Home's roles → working Hiring Portfolio Manager" ||
		run.moves[1] != "Fills the Home's roles → working Hiring Library Manager" {
		t.Fatalf("moves = %v", run.moves)
	}
}

func TestHireFolderAgentRoles_ARenamedOrFilledRoleStillEndsEveryLine(t *testing.T) {
	// "Library Manager" was taken meanwhile, so the role is hired as
	// "Library Manager 2"; "Portfolio Manager" was filled meanwhile.
	run := newHireRun("Hires Portfolio Manager", "Hires Library Manager")
	if _, err := hireFolderAgentRoles(roles("Library Manager 2"), run.order, "Adds Albums", run.setLine,
		func(foldersetup.HomeRole) error { return nil }); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Hires Portfolio Manager: done Hired Library Manager 2",
		"Hires Library Manager: done ",
	}
	if got := run.final(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("final:\n%s", strings.Join(got, "\n"))
	}
}

func TestHireFolderAgentRoles_AFailedHireMarksOnlyItsLine(t *testing.T) {
	run := newHireRun("Hires Portfolio Manager", "Hires Library Manager")
	_, err := hireFolderAgentRoles(roles("Portfolio Manager", "Library Manager"), run.order, "Adds Albums", run.setLine,
		func(role foldersetup.HomeRole) error {
			if role.Name == "Library Manager" {
				return errors.New("no model")
			}
			return nil
		})
	if err == nil {
		t.Fatal("a failed hire was not reported")
	}
	want := []string{
		"Hires Portfolio Manager: done Hired Portfolio Manager",
		"Hires Library Manager: failed Hiring Library Manager",
	}
	if got := run.final(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("final:\n%s", strings.Join(got, "\n"))
	}
}
