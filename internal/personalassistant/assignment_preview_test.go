package personalassistant

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildAssignmentPreview_MapsExplicitRowsToCanonicalRecords(t *testing.T) {
	preview, payload, err := BuildAssignmentPreview("preview-1", 3, AssignmentInput{Rows: []AssignmentInputRow{
		{Type: AssignmentRowPriority, Title: "Finish release notes", Detail: "Use the reviewed outline.", Due: "2026-09-01"},
		{Type: AssignmentRowIOwe, Title: "Send Maya the draft", Counterparty: "Maya", Due: "2026-09-01T15:00:00-04:00"},
		{Type: AssignmentRowWaitingOn, Title: "Budget approval", Counterparty: "Finance"},
		{Type: AssignmentRowFixedCommitment, Title: "Dentist at 3pm", Action: "Leave for the dentist by 2:30pm", Detail: "Bring insurance card"},
		{Type: AssignmentRowFixedCommitment, Title: "School pickup at 4pm", Due: "2026-09-01T16:00:00-04:00"},
	}})
	if err != nil {
		t.Fatalf("BuildAssignmentPreview: %v", err)
	}
	if preview.AssignmentVersion != 3 || preview.Count != 5 || preview.PayloadHash != PayloadHash(payload) {
		t.Fatalf("preview = %#v payload=%s", preview, payload)
	}
	want := []struct {
		record, category, state, title string
		awaiting                       bool
	}{
		{"ticket", "today_priority", "ready", "Finish release notes", true},
		{"follow_up", "i_owe", "active", "Send Maya the draft", false},
		{"follow_up", "waiting_on", "active", "Budget approval", false},
		{"ticket", "fixed_commitment_action", "ready", "Leave for the dentist by 2:30pm", true},
		{"follow_up", "needs_decision", "active", "School pickup at 4pm", false},
	}
	seenIDs := make(map[string]bool, len(preview.Items))
	for index, item := range preview.Items {
		if item.ID == "" || seenIDs[item.ID] {
			t.Fatalf("item %d has invalid immutable id %q", index, item.ID)
		}
		seenIDs[item.ID] = true
		if string(item.RecordType) != want[index].record || item.Category != want[index].category ||
			item.State != want[index].state || item.Title != want[index].title ||
			item.AwaitingExecutionIntent != want[index].awaiting {
			t.Fatalf("item %d = %#v; want %#v", index, item, want[index])
		}
	}
	if !strings.Contains(preview.Items[3].Detail, "Fixed commitment: Dentist at 3pm") ||
		preview.Items[1].Due != "2026-09-01T15:00:00-04:00" {
		t.Fatalf("fixed detail or due was not retained: %#v", preview.Items)
	}
}

func TestBuildAssignmentPreview_AllEmptyInputIsHonestEmptyPreview(t *testing.T) {
	preview, _, err := BuildAssignmentPreview("preview-empty", 1, AssignmentInput{Rows: []AssignmentInputRow{{}, {}}})
	if err != nil {
		t.Fatalf("BuildAssignmentPreview: %v", err)
	}
	if preview.Count != 0 || len(preview.Items) != 0 || preview.PayloadHash == "" {
		t.Fatalf("empty preview = %#v", preview)
	}
}

func TestBuildAssignmentPreview_ItemIDsAreStableWithinPreviewAndChangeAcrossPreviews(t *testing.T) {
	input := AssignmentInput{Rows: []AssignmentInputRow{{Type: AssignmentRowPriority, Title: "One thing"}}}
	first, _, err := BuildAssignmentPreview("preview-a", 1, input)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, _, err := BuildAssignmentPreview("preview-a", 1, input)
	if err != nil {
		t.Fatal(err)
	}
	superseding, _, err := BuildAssignmentPreview("preview-b", 2, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Items[0].ID != rebuilt.Items[0].ID || first.PayloadHash != rebuilt.PayloadHash {
		t.Fatal("the same preview identity did not rebuild deterministically")
	}
	if first.Items[0].ID == superseding.Items[0].ID || first.PayloadHash == superseding.PayloadHash {
		t.Fatal("a superseding preview reused immutable item identity")
	}
}

func TestBuildAssignmentPreview_RejectsUnboundedAmbiguousAndSecretRows(t *testing.T) {
	tests := []struct {
		name  string
		input AssignmentInput
	}{
		{"unknown type", AssignmentInput{Rows: []AssignmentInputRow{{Type: "guess", Title: "Thing"}}}},
		{"missing title", AssignmentInput{Rows: []AssignmentInputRow{{Type: AssignmentRowPriority}}}},
		{"multiline title", AssignmentInput{Rows: []AssignmentInputRow{{Type: AssignmentRowPriority, Title: "one\ntwo"}}}},
		{"invalid due", AssignmentInput{Rows: []AssignmentInputRow{{Type: AssignmentRowIOwe, Title: "Thing", Due: "tomorrowish"}}}},
		{"action on non commitment", AssignmentInput{Rows: []AssignmentInputRow{{Type: AssignmentRowPriority, Title: "Thing", Action: "Do it"}}}},
		{"secret detail", AssignmentInput{Rows: []AssignmentInputRow{{Type: AssignmentRowPriority, Title: "Thing", Detail: "token sk-abcdefghijklmnopqrstuvwxyz"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := BuildAssignmentPreview("preview", 1, test.input); !errors.Is(err, ErrValidation) {
				t.Fatalf("error = %v, want validation", err)
			}
		})
	}

	rows := make([]AssignmentInputRow, MaxAssignmentRows+1)
	if _, _, err := BuildAssignmentPreview("preview", 1, AssignmentInput{Rows: rows}); !errors.Is(err, ErrValidation) {
		t.Fatalf("too-many-rows error = %v", err)
	}
}
