package foldersetup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

const testRootKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func albumsFacts() StructureFacts {
	facts := StructureFacts{
		GroupName: "Albums", GroupType: personalassistant.FolderGroupPlain,
		Manager: "Albums Manager", RosterRevision: "roster-1", RootKey: testRootKey, RootIdentity: "1:2",
		Unavailable: []string{"Music Production Home is not installed; a plain group is proposed instead."},
	}
	for n := 1; n <= 5; n++ {
		name := fmt.Sprintf("Album-%d", n)
		facts.Members = append(facts.Members, StructureMember{
			CandidateID: fmt.Sprintf("candidate-%d", n), Name: name, Folder: name, TypeLabel: "Session folder", Identity: fmt.Sprintf("1:%d", 10+n),
		})
	}
	facts.Members = append(facts.Members, StructureMember{CandidateID: "candidate-6", Name: "Logic Sketch", Folder: "Logic Sketch", TypeLabel: "Plain workspace"})
	return facts
}

func lineNames(lines []personalassistant.FolderPlanLine) []string {
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		names = append(names, line.Kind+": "+line.Name)
	}
	return names
}

func TestBuildStructurePlan_APlainGroupListsEveryConsequenceInOrder(t *testing.T) {
	plan := BuildStructurePlan(albumsFacts())
	want := []string{
		"group: Creates group Albums",
		"workspaces: Creates 6 workspaces, each linked to its own folder",
		"member: Album-1", "member: Album-2", "member: Album-3", "member: Album-4", "member: Album-5", "member: Logic Sketch",
		"folder: Nothing is moved, copied or read",
		"unavailable: Music Production Home is not installed; a plain group is proposed instead.",
		"task: No task is created or started",
	}
	if got := lineNames(plan.Lines); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plan lines:\n%s", strings.Join(got, "\n"))
	}
	if plan.Lines[0].Detail != "Adds its Group Manager, Albums Manager." || plan.Lines[2].Detail != "Session folder" {
		t.Fatalf("details: %q / %q", plan.Lines[0].Detail, plan.Lines[2].Detail)
	}
	intent := plan.Intent
	if intent.Recipe != personalassistant.FolderRecipeStructure || intent.RecipeVersion != 1 || intent.Structure == nil ||
		intent.Structure.Validate() != nil || intent.Structure.ManagerAction != personalassistant.FolderSoftwareCreate ||
		len(intent.Structure.Members) != 6 || intent.Structure.Members[0].Blueprint != "" || plan.Digest == "" {
		t.Fatalf("intent: %+v / %+v", intent, intent.Structure)
	}
	for _, line := range plan.Lines {
		if strings.ContainsAny(line.Name+line.Detail, "/\\") {
			t.Fatalf("a plan line looks like a path: %+v", line)
		}
	}
}

func TestBuildStructurePlan_TheDigestCoversNamesMembersAndTheManager(t *testing.T) {
	base := BuildStructurePlan(albumsFacts())
	for name, change := range map[string]func(*StructureFacts){
		"a renamed group":           func(f *StructureFacts) { f.GroupName = "Albums 2026" },
		"an unchecked member":       func(f *StructureFacts) { f.Members = f.Members[:5] },
		"a renamed workspace":       func(f *StructureFacts) { f.Members[0].Name = "First Record" },
		"another folder, same name": func(f *StructureFacts) { f.Members[0].Identity = "9:9" },
		"another root":              func(f *StructureFacts) { f.RootIdentity = "9:9" },
		"another roster":            func(f *StructureFacts) { f.RosterRevision = "roster-2" },
		"a reused manager":          func(f *StructureFacts) { f.ManagerReused = true },
	} {
		facts := albumsFacts()
		change(&facts)
		if changed := BuildStructurePlan(facts); changed.Digest == base.Digest || changed.Digest == "" {
			t.Fatalf("%s did not change the plan the user confirms", name)
		}
	}
	if again := BuildStructurePlan(albumsFacts()); again.Digest != base.Digest {
		t.Fatal("the same facts stamped two different plans")
	}
}

func TestBuildStructurePlan_AHomeAndMixedAvailability(t *testing.T) {
	facts := albumsFacts()
	facts.GroupName, facts.GroupType, facts.Manager, facts.RosterRevision = "Music Production Home", personalassistant.FolderGroupHome, "", ""
	facts.Unavailable = nil
	for i := range facts.Members[:5] {
		facts.Members[i].Blueprint, facts.Members[i].BlueprintLabel = "session-song", "Session song"
	}
	facts.Members[1].Name = "Second Record"
	created := BuildStructurePlan(facts)
	if created.Lines[0].Name != "Creates your Music Production Home" || created.Lines[0].Detail != "" ||
		created.Lines[2].Detail != "Session folder · Session song workspace" ||
		created.Lines[3].Detail != "Session folder · Session song workspace · folder Album-2" ||
		created.Lines[7].Detail != "Plain workspace" || created.Intent.Structure.Members[0].Blueprint != "session-song" {
		t.Fatalf("home plan: %+v", created.Lines)
	}
	if len(created.Lines) != 10 || created.Intent.Structure.ManagerAction != "" || created.Intent.Structure.Validate() != nil {
		t.Fatalf("home plan has %d lines, intent %+v", len(created.Lines), created.Intent.Structure)
	}
	facts.GroupExists = true
	facts.Destination = &personalassistant.FolderSetupDestination{Status: "existing"}
	joined := BuildStructurePlan(facts)
	if joined.Lines[0].Name != "Adds to your Music Production Home" || !joined.Intent.Structure.GroupExists || joined.Digest == created.Digest {
		t.Fatalf("existing home plan: %+v", joined.Lines[0])
	}
	one := albumsFacts()
	one.Members = one.Members[:1]
	if got := BuildStructurePlan(one).Lines[1].Name; got != "Creates 1 workspace, linked to its own folder" {
		t.Fatalf("one member: %q", got)
	}
}

// structureHost is a host that records what it was asked to make.
type structureHost struct {
	groups, workspaces int
	made               map[string]StructureWorkspace
	failAt             string
	failGroup          error
	receiptErr         error
	order              []string
}

func (h *structureHost) EnsureGroup(_ context.Context, intent personalassistant.FolderStructureIntent) (StructureGroup, error) {
	h.order = append(h.order, "group")
	if h.failGroup != nil {
		return StructureGroup{}, h.failGroup
	}
	created := h.groups == 0
	h.groups++
	return StructureGroup{ID: "group-1", Name: intent.GroupName, Route: "/workspaces/albums", Created: created}, nil
}

func (h *structureHost) EnsureWorkspace(_ context.Context, group StructureGroup, member personalassistant.FolderStructureMemberIntent) (StructureWorkspace, error) {
	h.order = append(h.order, member.CandidateID)
	if member.CandidateID == h.failAt {
		return StructureWorkspace{}, errors.New("a workspace with that name already exists")
	}
	if h.made == nil {
		h.made = map[string]StructureWorkspace{}
	}
	if existing, ok := h.made[member.CandidateID]; ok {
		existing.Created = false
		return existing, nil
	}
	h.workspaces++
	workspace := StructureWorkspace{ID: "ws-" + member.CandidateID, Name: member.Name, Route: "/workspaces/albums/" + member.CandidateID, Created: true}
	h.made[member.CandidateID] = workspace
	return workspace, nil
}

func (h *structureHost) Receipt(_ context.Context, group StructureGroup, workspaces []StructureWorkspace) ([]personalassistant.FolderReceiptRow, error) {
	if h.receiptErr != nil {
		return nil, h.receiptErr
	}
	rows := []personalassistant.FolderReceiptRow{{Kind: "workspace", Name: group.Name, Detail: "group", Route: group.Route}}
	for _, workspace := range workspaces {
		rows = append(rows, personalassistant.FolderReceiptRow{Kind: "workspace", Name: workspace.Name, Route: workspace.Route})
	}
	return rows, nil
}

// updates records every snapshot a run reported.
type updates struct{ all []Update }

func (u *updates) Update(_ context.Context, update Update) error {
	u.all = append(u.all, update)
	return nil
}

func (u *updates) last() Update { return u.all[len(u.all)-1] }

func states(lines []personalassistant.FolderPlanLine, kind string) []string {
	var out []string
	for _, line := range lines {
		if line.Kind == kind {
			out = append(out, line.State)
		}
	}
	return out
}

func TestStructureRunner_CreatesTheGroupThenEachWorkspaceInOrder(t *testing.T) {
	host, progress := &structureHost{}, &updates{}
	plan := BuildStructurePlan(albumsFacts())
	result, err := (&StructureRunner{Host: host, Progress: progress}).Run(context.Background(), plan)
	if err != nil || result.Status != personalassistant.FolderSetupDone || result.StopReason != "" {
		t.Fatalf("run: %+v, %v", result, err)
	}
	want := "group,candidate-1,candidate-2,candidate-3,candidate-4,candidate-5,candidate-6"
	if got := strings.Join(host.order, ","); got != want || host.groups != 1 || host.workspaces != 6 {
		t.Fatalf("order %s; %d groups, %d workspaces", got, host.groups, host.workspaces)
	}
	final := progress.last()
	if final.Status != personalassistant.FolderSetupDone || final.HomeID != "group-1" || len(final.Receipt) != 7 || final.Receipt[0].Detail != "group" {
		t.Fatalf("final update: %+v", final)
	}
	for _, line := range final.Lines {
		if line.State != personalassistant.FolderLineDone {
			t.Fatalf("a line was left %s: %+v", line.State, line)
		}
	}
	// Progress was reported per line: the group working first, then one member
	// at a time with the earlier ones done.
	first := progress.all[0]
	if states(first.Lines, personalassistant.FolderPlanGroup)[0] != personalassistant.FolderLineWorking || first.HomeID != "" {
		t.Fatalf("first update: %+v", first)
	}
	third := progress.all[3]
	if got := strings.Join(states(third.Lines, personalassistant.FolderPlanMember), ","); got != "done,done,working,waiting,waiting,waiting" || third.HomeID != "group-1" {
		t.Fatalf("mid-run members: %s", got)
	}
}

func TestStructureRunner_AMemberThatFailsStopsTheRunAndContinueCarriesOn(t *testing.T) {
	host, progress := &structureHost{failAt: "candidate-3"}, &updates{}
	plan := BuildStructurePlan(albumsFacts())
	runner := &StructureRunner{Host: host, Progress: progress}
	result, err := runner.Run(context.Background(), plan)
	if err != nil || result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopMemberFailed ||
		result.Cause == nil || !strings.Contains(result.Cause.Error(), "member 3") {
		t.Fatalf("stopped run: %+v, %v", result, err)
	}
	stopped := progress.last()
	if got := strings.Join(states(stopped.Lines, personalassistant.FolderPlanMember), ","); got != "done,done,failed,waiting,waiting,waiting" ||
		stopped.HomeID != "group-1" || len(stopped.Receipt) != 0 || states(stopped.Lines, personalassistant.FolderPlanGroup)[0] != personalassistant.FolderLineDone {
		t.Fatalf("stopped lines: %s, %+v", got, stopped)
	}
	if host.workspaces != 2 {
		t.Fatalf("%d workspaces were made before the stop", host.workspaces)
	}
	// Continue: the same confirmed plan again. What exists is found, not made twice.
	host.failAt = ""
	result, err = runner.Run(context.Background(), plan)
	if err != nil || result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("continued run: %+v, %v", result, err)
	}
	if host.groups != 2 || host.workspaces != 6 || len(progress.last().Receipt) != 7 {
		t.Fatalf("after Continue: %d group calls, %d workspaces, %d receipt rows", host.groups, host.workspaces, len(progress.last().Receipt))
	}
	// A third run of a finished setup creates nothing.
	if _, err := runner.Run(context.Background(), plan); err != nil || host.workspaces != 6 {
		t.Fatalf("a repeated run made %d workspaces: %v", host.workspaces, err)
	}
}

func TestStructureRunner_StopsSayWhy(t *testing.T) {
	plan := BuildStructurePlan(albumsFacts())
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, tc := range map[string]struct {
		host   *structureHost
		ctx    context.Context
		reason string
	}{
		"the group cannot be created": {&structureHost{failGroup: errors.New("refused")}, context.Background(), personalassistant.FolderStopFailed},
		"the plan changed":            {&structureHost{failGroup: personalassistant.ErrFolderPlanChanged}, context.Background(), personalassistant.FolderStopPlanChanged},
		"the folder is gone":          {&structureHost{failGroup: personalassistant.ErrFolderPathLost}, context.Background(), personalassistant.FolderStopNeedsPick},
		"nothing can be read back":    {&structureHost{receiptErr: errors.New("unreadable")}, context.Background(), personalassistant.FolderStopFailed},
		"the run was cancelled":       {&structureHost{failGroup: errors.New("cancelled")}, cancelled, personalassistant.FolderStopInterrupted},
	} {
		progress := &updates{}
		result, err := (&StructureRunner{Host: tc.host, Progress: progress}).Run(tc.ctx, plan)
		if err != nil || result.Status != personalassistant.FolderSetupStopped || result.StopReason != tc.reason || progress.last().StopReason != tc.reason {
			t.Fatalf("%s: %+v, %v", name, result, err)
		}
	}
	// A plan that is not a structure plan is never run as one.
	other := personalassistant.NewFolderSetupPlan([]personalassistant.FolderPlanLine{{Kind: "workspace", Name: "Creates a workspace"}})
	if _, err := (&StructureRunner{Host: &structureHost{}, Progress: &updates{}}).Run(context.Background(), other); !errors.Is(err, ErrStructureNotWired) {
		t.Fatalf("a foreign plan was run: %v", err)
	}
}
