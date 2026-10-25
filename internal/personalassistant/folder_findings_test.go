package personalassistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func TestFolderFindings_AreFrozenForTheExactSubject(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, root := range []bool{true, false} {
		candidate := folderdigest.Candidate{Name: "Literal <b>session</b>", Path: "/not-exposed/session", IsRoot: root, FileCount: 7}
		record := folderCandidateRecord(candidate, FolderChoiceProject, "Observed metadata")
		offer := FolderOffer{ID: "o", Subject: record, FolderName: "Parent", ScannedAt: now, Partial: true}
		service := &FolderDigestService{}
		view := service.describeOffer(context.Background(), offer, false)
		if view.Subject.ObservedFiles == nil || *view.Subject.ObservedFiles != 7 || view.Subject.IsRoot != root || !view.Partial || view.ScannedAt != now {
			t.Fatalf("scoped findings = %+v", view)
		}
		candidate.FileCount = 99 // later source changes cannot rewrite saved facts
		if *view.Subject.ObservedFiles != 7 {
			t.Fatal("metadata not frozen")
		}
		encoded, err := json.Marshal(view)
		if err != nil || strings.Contains(string(encoded), "/not-exposed") {
			t.Fatalf("projection leaked a path: %s %v", encoded, err)
		}
	}
}

func TestFolderFindings_LegacyUnknownAndBoundedCounts(t *testing.T) {
	legacy := FolderCandidateRecord{Key: FolderKey("/legacy"), Name: "Legacy", Kind: FolderChoiceProject}
	if err := validateFolderCandidate(legacy); err != nil || legacy.ObservedFiles != nil {
		t.Fatalf("legacy count must remain unknown: %+v %v", legacy, err)
	}
	for _, n := range []int{-1, 5001} {
		bad := legacy
		bad.ObservedFiles = &n
		if validateFolderCandidate(bad) == nil {
			t.Fatal("accepted unbounded count")
		}
	}
	record := folderCandidateRecord(folderdigest.Candidate{Name: "Empty", FileCount: 0}, FolderChoiceProject, "")
	if record.ObservedFiles == nil || *record.ObservedFiles != 0 {
		t.Fatal("an observed zero is different from legacy unknown")
	}
}
