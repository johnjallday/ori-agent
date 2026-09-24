package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

func janitorKnowledgeTestStatus(at time.Time) filejanitor.Status {
	settings := filejanitor.NewSettings("janitor-ws")
	settings.RootPath = "/tmp/disposable-fixture-folder"
	settings.DirectoryReferenceID = "approved-reference"
	settings.RootID = "root-generation-1"
	settings.SetupCompletedAt = at.Add(-24 * time.Hour)
	return filejanitor.Status{
		Settings: settings, Applies: true,
		Readiness: filejanitor.Readiness{Checks: []filejanitor.ComponentCheck{
			{Component: filejanitor.ComponentDirectoryAccess, Status: filejanitor.ComponentOK},
			{Component: filejanitor.ComponentMCPBinding, Status: filejanitor.ComponentOK},
			{Component: filejanitor.ComponentDestination, Status: filejanitor.ComponentFailed},
		}},
	}
}

func janitorKnowledgeTestAction(id string, at time.Time) filejanitor.FileAction {
	name := "private-filename-" + id + ".pdf"
	before := filejanitor.Fingerprint{Name: name, Size: 12, ModTime: at.Add(-time.Minute)}
	return filejanitor.FileAction{
		ID: id, WorkspaceID: "janitor-ws", RootID: "root-generation-1", CandidateID: "candidate-" + id,
		Operation: filejanitor.OperationMove, SourceName: name, DestinationCategory: filejanitor.CategoryDocuments,
		DestinationRelative: "Filed/Documents/" + name,
		BeforeFingerprint:   before, AfterFingerprint: before,
		ApprovedBy: "local", ApprovedAt: at.Add(-time.Minute), IdempotencyKey: "action-" + id,
		CompletedAt: at, Result: filejanitor.ResultApplied, Undo: filejanitor.UndoAvailable,
	}
}

func TestJanitorKnowledgeRequiresCurrentFolderReadPermissionsAndRootGeneration(t *testing.T) {
	at := time.Now().UTC()
	status := janitorKnowledgeTestStatus(at)
	if !janitorKnowledgeReadAllowed(status, "janitor-ws") {
		t.Fatal("healthy read authority was denied by unrelated destination failure")
	}
	for _, scenario := range []struct {
		name string
		edit func(*filejanitor.Status)
	}{
		{"no root generation", func(s *filejanitor.Status) { s.Settings.RootID = "" }},
		{"foreign workspace", func(s *filejanitor.Status) { s.Settings.WorkspaceID = "foreign" }},
		{"no template", func(s *filejanitor.Status) { s.Applies = false }},
		{"overlapping root", func(s *filejanitor.Status) { s.Settings.RootConflictWorkspaceID = "other" }},
		{"revoked directory", func(s *filejanitor.Status) { s.Readiness.Checks[0].Status = filejanitor.ComponentFailed }},
		{"missing MCP check", func(s *filejanitor.Status) { s.Readiness.Checks = s.Readiness.Checks[:1] }},
		{"duplicate MCP check", func(s *filejanitor.Status) { s.Readiness.Checks = append(s.Readiness.Checks, s.Readiness.Checks[1]) }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			changed := janitorKnowledgeTestStatus(at)
			scenario.edit(&changed)
			if janitorKnowledgeReadAllowed(changed, "janitor-ws") {
				t.Fatal("untrusted root/permission was accepted")
			}
		})
	}
}

func TestJanitorKnowledgeSupportsCountAppliedDistinctMovesNotFailedUndoTimestamp(t *testing.T) {
	at := time.Now().UTC()
	status := janitorKnowledgeTestStatus(at)
	one, two, three := janitorKnowledgeTestAction("one", at), janitorKnowledgeTestAction("two", at), janitorKnowledgeTestAction("three", at)
	two.Undo = filejanitor.UndoFailed
	two.UndoneAt = at                                         // finishUndo records this even when the reversal failed
	actions := []filejanitor.FileAction{one, two, three, one} // retry of the same ID is not a fourth move
	result := projectJanitorKnowledgeSupports("janitor-ws", "local", status.Settings, actions, at.Add(time.Minute))
	if len(result) != 1 || len(result[0].ActionIDs) != 3 || result[0].Category != filejanitor.CategoryDocuments {
		t.Fatalf("three distinct applied moves were not recognized: %+v", result)
	}
	if strings.Contains(fmt.Sprintf("%+v", result), "private-filename") || strings.Contains(fmt.Sprintf("%+v", result), "disposable-fixture-folder") {
		t.Fatalf("private journal data escaped the evidence projection: %+v", result)
	}
	three.Undo, three.UndoneAt = filejanitor.UndoDone, at
	if result := projectJanitorKnowledgeSupports("janitor-ws", "local", status.Settings,
		[]filejanitor.FileAction{one, two, three}, at.Add(time.Minute)); len(result) != 0 {
		t.Fatalf("successful undo counted as support: %+v", result)
	}
	three.Undo, three.Result = filejanitor.UndoAvailable, filejanitor.ResultFailed
	if result := projectJanitorKnowledgeSupports("janitor-ws", "local", status.Settings,
		[]filejanitor.FileAction{one, two, three}, at.Add(time.Minute)); len(result) != 0 {
		t.Fatalf("failed action counted as support: %+v", result)
	}
	three = janitorKnowledgeTestAction("three", at)
	three.RootID = "previous-root"
	if result := projectJanitorKnowledgeSupports("janitor-ws", "local", status.Settings,
		[]filejanitor.FileAction{one, two, three}, at.Add(time.Minute)); len(result) != 0 {
		t.Fatalf("relinked root action counted as support: %+v", result)
	}
}
