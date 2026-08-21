package reaper

import (
	"errors"
	"strings"
	"testing"
)

func TestBulkPlanRejectsAnEmptyPlan(t *testing.T) {
	if err := (BulkPlan{}).Validate(); !errors.Is(err, ErrInvalidTrackEdit) {
		t.Fatalf("empty plan = %v, want ErrInvalidTrackEdit", err)
	}
	if _, err := (BulkPlan{}).Lua("/tmp/receipt.txt"); !errors.Is(err, ErrInvalidTrackEdit) {
		t.Fatalf("empty plan Lua = %v, want ErrInvalidTrackEdit", err)
	}
}

func TestBulkPlanRejectsAnInvalidEdit(t *testing.T) {
	plan := BulkPlan{Edits: []TrackEdit{RenameEdit(1, "Drums", "")}}
	if err := plan.Validate(); !errors.Is(err, ErrInvalidTrackEdit) {
		t.Fatalf("invalid edit = %v, want ErrInvalidTrackEdit", err)
	}
}

func TestBulkPlanOrdersRenamesColorsAndTogglesBeforeMoves(t *testing.T) {
	// PRD open question 6: a reorder must never invalidate the indices later
	// edits in the same plan depend on, so non-move kinds execute first.
	plan := BulkPlan{Edits: []TrackEdit{
		MoveEdit(3, "Guitar", 1),
		RenameEdit(1, "Kick", "Kick Drum"),
		ColorEdit(2, "Bass", trackCustomColorFlag|0xFF0000),
		MuteEdit(1, "Kick", true),
	}}
	ordered := plan.Ordered()
	if len(ordered) != 4 {
		t.Fatalf("ordered = %+v", ordered)
	}
	for i, kind := range []string{TrackEditRename, TrackEditColor, TrackEditMute, TrackEditMove} {
		if ordered[i].Kind != kind {
			t.Fatalf("ordered[%d].Kind = %q, want %q (full order: %+v)", i, ordered[i].Kind, kind, ordered)
		}
	}
}

func TestBulkPlanLuaChecksEveryGuardBeforeApplyingAnything(t *testing.T) {
	plan := BulkPlan{Edits: []TrackEdit{
		RenameEdit(1, "Kick", "Kick Drum"),
		MuteEdit(2, "Bass", true),
		MoveEdit(3, "Guitar", 1),
	}}
	lua, err := plan.Lua("/tmp/receipt.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Every guard check happens before the single Undo_BeginBlock, so nothing
	// can apply until every one of them has been evaluated.
	firstApply := strings.Index(lua, "Undo_BeginBlock")
	if firstApply < 0 {
		t.Fatalf("no Undo_BeginBlock found:\n%s", lua)
	}
	for _, guard := range []string{
		`failed[#failed + 1] = 1`,
		`failed[#failed + 1] = 2`,
		`failed[#failed + 1] = 3`,
	} {
		position := strings.Index(lua, guard)
		if position < 0 || position > firstApply {
			t.Fatalf("guard %q did not run before Undo_BeginBlock:\n%s", guard, lua)
		}
	}
	if !strings.Contains(lua, `if #failed > 0 then`) {
		t.Fatalf("missing all-or-nothing check:\n%s", lua)
	}
	if !strings.Contains(lua, `write_receipt("guard_failed\n" .. table.concat(failed, ","))`) {
		t.Fatalf("missing guard-failure receipt:\n%s", lua)
	}
	if !strings.Contains(lua, `write_receipt("applied\n3")`) {
		t.Fatalf("missing applied-count receipt:\n%s", lua)
	}
	// One undo block for the whole plan (PRD requirement 23).
	if strings.Count(lua, "Undo_BeginBlock") != 1 || strings.Count(lua, "Undo_EndBlock") != 1 {
		t.Fatalf("plan did not run as one undo step:\n%s", lua)
	}
}

func TestBulkPlanLuaAppliesInTheOrderedSequence(t *testing.T) {
	plan := BulkPlan{Edits: []TrackEdit{
		MoveEdit(3, "Guitar", 1),
		RenameEdit(1, "Kick", "Kick Drum"),
	}}
	lua, err := plan.Lua("/tmp/receipt.txt")
	if err != nil {
		t.Fatal(err)
	}
	rename := strings.Index(lua, `reaper.GetSetMediaTrackInfo_String(tr, "P_NAME"`)
	move := strings.Index(lua, "reaper.ReorderSelectedTracks")
	if rename < 0 || move < 0 || rename > move {
		t.Fatalf("rename did not apply before move:\n%s", lua)
	}
}

func TestParseBulkReceiptReadsAppliedCountAndFailedIndices(t *testing.T) {
	applied, err := ParseBulkReceipt([]byte("applied\n3"))
	if err != nil || !applied.Applied || applied.AppliedCount != 3 {
		t.Fatalf("applied receipt = %+v, %v", applied, err)
	}

	refused, err := ParseBulkReceipt([]byte("guard_failed\n2,4"))
	if err != nil || refused.Applied || len(refused.FailedIndices) != 2 || refused.FailedIndices[0] != 2 || refused.FailedIndices[1] != 4 {
		t.Fatalf("guard_failed receipt = %+v, %v", refused, err)
	}

	empty, err := ParseBulkReceipt([]byte("guard_failed\n"))
	if err != nil || empty.Applied || len(empty.FailedIndices) != 0 {
		t.Fatalf("empty guard_failed receipt = %+v, %v", empty, err)
	}

	for _, bad := range []string{"", "ok", "applied\n0", "applied\nabc", "guard_failed\n1,x"} {
		if _, err := ParseBulkReceipt([]byte(bad)); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("receipt %q = %v, want ErrInvalidReceipt", bad, err)
		}
	}
}

func TestBulkPlanLuaRejectsAnUntrustedReceiptPath(t *testing.T) {
	plan := BulkPlan{Edits: []TrackEdit{RenameEdit(1, "Kick", "Kick Drum")}}
	for _, path := range []string{"", "relative/receipt.txt", "/tmp/re\nceipt.txt"} {
		if _, err := plan.Lua(path); err == nil {
			t.Fatalf("receipt path %q was accepted", path)
		}
	}
}
