package server

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

// A file the person had Ori put into a folder of their choosing says nothing
// about which Filing category they prefer, so it must never become a category
// preference in the assistant's knowledge.
func TestJanitorSupportIgnoresPlacementsIntoChosenFolders(t *testing.T) {
	at := time.Now().UTC()
	settings := janitorKnowledgeTestStatus(at).Settings
	now := at.Add(time.Minute)

	move := janitorKnowledgeTestAction("m1", at)
	if !janitorSupportAction(move, "janitor-ws", "local", settings, now) {
		t.Fatal("control: an applied, approved category move is support")
	}

	place := move
	place.ID = "p1"
	place.Operation = filejanitor.OperationPlace
	place.DestinationCategory = ""
	place.DestinationRelative = "Taxes/" + place.SourceName
	place.Place = &filejanitor.PlacedInto{RootDirID: "1:1", FolderID: "1:2"}
	place.BeforeFingerprint.FileID = "1:3"
	place.AfterFingerprint.FileID = "1:3"
	if err := place.Validate(); err != nil {
		t.Fatalf("the placement must be a valid journal record for this test to mean anything: %v", err)
	}
	if janitorSupportAction(place, "janitor-ws", "local", settings, now) {
		t.Fatal("a placement counted as a category preference")
	}

	// Nor may it count by being shaped like a canonical category destination.
	disguised := place
	disguised.DestinationRelative = "Filed/Documents/" + place.SourceName
	if janitorSupportAction(disguised, "janitor-ws", "local", settings, now) {
		t.Fatal("a placement disguised as a category move counted")
	}
}
