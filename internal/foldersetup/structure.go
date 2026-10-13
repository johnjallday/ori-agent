package foldersetup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// A structure setup makes one group and a linked workspace for each member
// folder of a collection. It installs nothing and runs no task: the plan below
// is everything it does, and the runner does exactly that, one line at a time.

// StructureMember is one member folder and the workspace the plan promises it.
type StructureMember struct {
	CandidateID string
	// Name is the workspace's name; Folder the folder's own label, shown when a
	// rename made them differ.
	Name   string
	Folder string
	// TypeLabel says what the folder was recognized as. BlueprintLabel names the
	// blueprint the workspace starts from; empty is a plain workspace.
	TypeLabel      string
	Blueprint      string
	BlueprintLabel string
	Identity       string
}

// StructureFacts are what a structure's plan is built from, read by the host
// from canonical state. The builder only words them.
type StructureFacts struct {
	GroupName string
	// GroupType is personalassistant.FolderGroupPlain or FolderGroupHome.
	// GroupExists says the group is the owner's existing Home.
	GroupType   string
	GroupExists bool
	// Manager is the Group Manager a new plain group is created with;
	// ManagerReused says it is the owner's saved agent of that name, used as it
	// is. RosterRevision witnesses the roster plan those two were read from.
	Manager        string
	ManagerReused  bool
	RosterRevision string
	// RootKey and RootIdentity witness the folder; Destination the Home.
	RootKey      string
	RootIdentity string
	Destination  *personalassistant.FolderSetupDestination
	Members      []StructureMember
	// Unavailable are the sentences for what the proposal goes without.
	Unavailable []string
}

// BuildStructurePlan lists every consequence of Set up on a proposed group, in
// the order the card shows them (PRD FR12), and the intent a run is held to.
// Every string is plain text and names no path.
func BuildStructurePlan(facts StructureFacts) personalassistant.FolderSetupPlan {
	group := personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanGroup, Name: "Creates group " + facts.GroupName}
	intent := personalassistant.FolderStructureIntent{
		GroupName: facts.GroupName, GroupType: facts.GroupType, GroupExists: facts.GroupExists,
		RootKey: facts.RootKey, RootIdentity: facts.RootIdentity,
	}
	switch {
	case facts.GroupExists:
		group.Name, group.Detail = "Adds to your "+facts.GroupName, "Nothing about the group itself changes."
	case facts.GroupType == personalassistant.FolderGroupHome:
		group.Name = "Creates your " + facts.GroupName
	case strings.TrimSpace(facts.Manager) != "":
		intent.Manager, intent.RosterRevision = facts.Manager, facts.RosterRevision
		intent.ManagerAction = personalassistant.FolderSoftwareCreate
		group.Detail = "Adds its Group Manager, " + facts.Manager + "."
		if facts.ManagerReused {
			intent.ManagerAction = personalassistant.FolderSoftwareReuse
			group.Detail = "Your saved agent " + facts.Manager + " is its Group Manager."
		}
	}
	count := len(facts.Members)
	workspaces := fmt.Sprintf("Creates %d workspaces, each linked to its own folder", count)
	if count == 1 {
		workspaces = "Creates 1 workspace, linked to its own folder"
	}
	lines := []personalassistant.FolderPlanLine{group, {Kind: personalassistant.FolderPlanMembers, Name: workspaces}}
	for _, member := range facts.Members {
		detail := strings.TrimSpace(member.TypeLabel)
		if label := strings.TrimSpace(member.BlueprintLabel); label != "" && member.Blueprint != "" {
			detail = strings.TrimSpace(detail + " · " + label + " workspace")
		}
		if member.Folder != "" && member.Folder != member.Name {
			detail = strings.TrimSpace(detail + " · folder " + member.Folder)
		}
		lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanMember, Name: member.Name, Detail: strings.TrimPrefix(detail, "· ")})
		intent.Members = append(intent.Members, personalassistant.FolderStructureMemberIntent{
			CandidateID: member.CandidateID, Name: member.Name, Blueprint: member.Blueprint, Identity: member.Identity,
		})
	}
	lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanFolder, Name: "Nothing is moved, copied or read"})
	for _, sentence := range facts.Unavailable {
		if sentence = strings.TrimSpace(sentence); sentence != "" {
			lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanUnavailable, Name: sentence})
		}
	}
	lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanTask, Name: "No task is created or started"})
	plan := personalassistant.NewFolderSetupPlan(lines)
	plan.Intent = personalassistant.FolderSetupIntent{
		Recipe: personalassistant.FolderRecipeStructure, RecipeVersion: 1, Structure: &intent, Placement: "grouped",
	}
	plan.Destination = facts.Destination
	return plan.Stamped()
}

// StructureGroup is the group a run created, or found already there.
type StructureGroup struct {
	ID    string
	Name  string
	Route string
	// Created is false for the owner's existing Home and for a group an earlier
	// pass of this same setup made.
	Created bool
}

// StructureWorkspace is one member's workspace, linked to its folder.
type StructureWorkspace struct {
	ID      string
	Name    string
	Route   string
	Created bool
}

// StructureHost is the host seam behind a structure run. Both calls are
// idempotent for one confirmed setup: a second pass finds what the first made
// and creates nothing again.
type StructureHost interface {
	// EnsureGroup creates the group the intent promised, or returns the one this
	// setup already has (an existing Home, or a group an earlier pass created).
	EnsureGroup(ctx context.Context, intent personalassistant.FolderStructureIntent) (StructureGroup, error)
	// EnsureWorkspace creates one member's workspace under the group and links
	// the member's folder as its primary directory. It reads no file and starts
	// no task.
	EnsureWorkspace(ctx context.Context, group StructureGroup, member personalassistant.FolderStructureMemberIntent) (StructureWorkspace, error)
	// Receipt reads back, from canonical state, the group and the workspaces
	// this setup made.
	Receipt(ctx context.Context, group StructureGroup, workspaces []StructureWorkspace) ([]personalassistant.FolderReceiptRow, error)
}

// StructureRunner is the resumable structure run.
type StructureRunner struct {
	Host     StructureHost
	Progress Progress
}

// ErrStructureNotWired means the host left out a seam the run needs, or the
// plan it was handed is not a structure plan.
var ErrStructureNotWired = errors.New("foldersetup: the structure run is not wired")

// Run creates the group, then each member's workspace in the plan's order,
// reporting every line as it goes. A member that cannot be created stops the
// run with the earlier lines done; running the same confirmed plan again
// carries on from that member, because the host finds what already exists.
func (r *StructureRunner) Run(ctx context.Context, plan personalassistant.FolderSetupPlan) (Result, error) {
	intent := plan.Intent.Structure
	if r == nil || r.Host == nil || r.Progress == nil || intent == nil || plan.Intent.Recipe != personalassistant.FolderRecipeStructure {
		return Result{}, ErrStructureNotWired
	}
	run := &structureRun{runner: r, intent: *intent}
	run.lines = append([]personalassistant.FolderPlanLine(nil), plan.Lines...)
	for i := range run.lines {
		run.lines[i].State = personalassistant.FolderLineWaiting
	}
	reason, cause := run.drive(ctx)
	status := personalassistant.FolderSetupDone
	if reason != "" {
		status = personalassistant.FolderSetupStopped
		for i := range run.lines {
			if run.lines[i].State == personalassistant.FolderLineWorking {
				run.lines[i].State = personalassistant.FolderLineFailed
			}
		}
	}
	err := run.record(context.WithoutCancel(ctx), status, reason)
	return Result{Status: status, StopReason: reason, Cause: cause}, err
}

type structureRun struct {
	runner  *StructureRunner
	intent  personalassistant.FolderStructureIntent
	lines   []personalassistant.FolderPlanLine
	group   StructureGroup
	receipt []personalassistant.FolderReceiptRow
}

func (s *structureRun) record(ctx context.Context, status, reason string) error {
	return s.runner.Progress.Update(ctx, Update{
		Lines: append([]personalassistant.FolderPlanLine(nil), s.lines...), Status: status, StopReason: reason,
		HomeID: s.group.ID, Receipt: append([]personalassistant.FolderReceiptRow(nil), s.receipt...),
	})
}

// set marks the index-th line of a kind (members are in plan order).
func (s *structureRun) set(kind string, index int, state string) {
	seen := 0
	for i := range s.lines {
		if s.lines[i].Kind != kind {
			continue
		}
		if seen == index {
			s.lines[i].State = state
			return
		}
		seen++
	}
}

// drive returns the stop reason ("" when the run finished) and its cause.
func (s *structureRun) drive(ctx context.Context) (string, error) {
	stopped := func(reason string, cause error) (string, error) {
		if ctx.Err() != nil {
			return personalassistant.FolderStopInterrupted, ctx.Err()
		}
		if errors.Is(cause, personalassistant.ErrFolderPlanChanged) {
			return personalassistant.FolderStopPlanChanged, cause
		}
		if errors.Is(cause, ErrNeedsPick) || errors.Is(cause, personalassistant.ErrFolderPathLost) {
			return personalassistant.FolderStopNeedsPick, cause
		}
		return reason, cause
	}
	s.set(personalassistant.FolderPlanGroup, 0, personalassistant.FolderLineWorking)
	if err := s.record(ctx, personalassistant.FolderSetupRunning, ""); err != nil {
		return stopped(personalassistant.FolderStopFailed, err)
	}
	group, err := s.runner.Host.EnsureGroup(ctx, s.intent)
	if err != nil || group.ID == "" {
		if err == nil {
			err = errors.New("the group was not created")
		}
		return stopped(personalassistant.FolderStopFailed, err)
	}
	s.group = group
	s.set(personalassistant.FolderPlanGroup, 0, personalassistant.FolderLineDone)
	s.set(personalassistant.FolderPlanMembers, 0, personalassistant.FolderLineWorking)
	workspaces := make([]StructureWorkspace, 0, len(s.intent.Members))
	for index, member := range s.intent.Members {
		s.set(personalassistant.FolderPlanMember, index, personalassistant.FolderLineWorking)
		if err := s.record(ctx, personalassistant.FolderSetupRunning, ""); err != nil {
			return stopped(personalassistant.FolderStopFailed, err)
		}
		workspace, err := s.runner.Host.EnsureWorkspace(ctx, group, member)
		if err != nil || workspace.ID == "" {
			if err == nil {
				err = errors.New("the workspace was not created")
			}
			return stopped(personalassistant.FolderStopMemberFailed, fmt.Errorf("member %d: %w", index+1, err))
		}
		workspaces = append(workspaces, workspace)
		s.set(personalassistant.FolderPlanMember, index, personalassistant.FolderLineDone)
	}
	rows, err := s.runner.Host.Receipt(ctx, group, workspaces)
	if err != nil || len(rows) == 0 {
		if err == nil {
			err = errors.New("nothing could be read back")
		}
		return stopped(personalassistant.FolderStopFailed, fmt.Errorf("could not read back what was set up: %w", err))
	}
	s.receipt = rows
	for i := range s.lines {
		s.lines[i].State = personalassistant.FolderLineDone
	}
	return "", nil
}
