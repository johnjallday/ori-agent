package reaper

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestRenameEditValidatesBeforeGeneratingLua(t *testing.T) {
	cases := []struct {
		name string
		edit TrackEdit
	}{
		{"zero index", RenameEdit(0, "Drums", "Kick")},
		{"negative index", RenameEdit(-1, "Drums", "Kick")},
		{"empty new name", RenameEdit(1, "Drums", "")},
		{"whitespace new name", RenameEdit(1, "Drums", "   ")},
		{"oversized new name", RenameEdit(1, "Drums", strings.Repeat("x", maxTrackNameBytes+1))},
		{"oversized expected name", RenameEdit(1, strings.Repeat("x", maxTrackNameBytes+1), "Kick")},
		{"unknown kind", TrackEdit{Kind: "explode", Index: 1, NewName: "Kick"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.edit.Validate(); !errors.Is(err, ErrInvalidTrackEdit) {
				t.Fatalf("Validate() = %v, want ErrInvalidTrackEdit", err)
			}
			if lua, err := testCase.edit.Lua("/tmp/receipt.txt"); err == nil || lua != "" {
				t.Fatalf("invalid edit generated Lua: %q, %v", lua, err)
			}
		})
	}
}

func TestRenameEditAcceptsAnEmptyExpectedName(t *testing.T) {
	// REAPER reports an unnamed track's name as empty on both the Web Remote
	// TRACK line and P_NAME, so "" is a legitimate guard value.
	edit := RenameEdit(4, "", "Kick")
	if err := edit.Validate(); err != nil {
		t.Fatalf("empty expected name rejected: %v", err)
	}
	lua, err := edit.Lua("/tmp/receipt.txt")
	if err != nil || !strings.Contains(lua, `local expected = ""`) {
		t.Fatalf("expected-name literal = %v, lua=%q", err, lua)
	}
}

func TestRenameEditLuaGuardsOnNameAndWritesAReceipt(t *testing.T) {
	lua, err := RenameEdit(3, "Drums", "Kick").Lua("/Users/x/.ori-reaper/last_receipt.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// The guard re-reads the name and refuses rather than guessing.
		`reaper.GetSetMediaTrackInfo_String(tr, "P_NAME", "", false)`,
		`if not ok or current ~= expected then`,
		`write_receipt("guard_failed\n")`,
		// Lua is 0-based; the Web Remote index is 1-based.
		`reaper.GetTrack(0, index - 1)`,
		`local index = 3`,
		// The write happens inside one undo block.
		`reaper.Undo_BeginBlock()`,
		`reaper.GetSetMediaTrackInfo_String(tr, "P_NAME", new_value, true)`,
		`reaper.Undo_EndBlock("Ori: rename track", -1)`,
		// The prior value comes back for the specific inverse.
		`write_receipt("applied\n" .. current)`,
	} {
		if !strings.Contains(lua, want) {
			t.Fatalf("generated Lua missing %q:\n%s", want, lua)
		}
	}
	// Names are byte-escaped, so a raw name never appears as a bare literal.
	if strings.Contains(lua, `"Drums"`) || strings.Contains(lua, `"Kick"`) {
		t.Fatalf("names were not byte-escaped:\n%s", lua)
	}
}

func TestRenameEditLuaEscapesHostileNames(t *testing.T) {
	hostile := "\"]]..os.exit()--\n\\"
	lua, err := RenameEdit(1, hostile, "Safe").Lua("/tmp/receipt.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Every byte becomes a decimal escape, so nothing in the name can close
	// the literal or start executable Lua.
	if strings.Contains(lua, "os.exit") || strings.Contains(lua, "]]") {
		t.Fatalf("hostile name leaked into generated Lua:\n%s", lua)
	}
	if !strings.Contains(lua, `local expected = "\34\93\93`) {
		t.Fatalf("expected byte escapes for the hostile name:\n%s", lua)
	}
}

func TestRenameEditLuaRejectsAnUntrustedReceiptPath(t *testing.T) {
	for _, path := range []string{"", "relative/receipt.txt", "/tmp/re\nceipt.txt"} {
		if _, err := RenameEdit(1, "Drums", "Kick").Lua(path); err == nil {
			t.Fatalf("receipt path %q was accepted", path)
		}
	}
}

func TestColorEditValidatesRange(t *testing.T) {
	cases := []struct {
		name  string
		color int64
		valid bool
	}{
		{"no color", 0, true},
		{"flagged red", trackCustomColorFlag | 0xFF0000, true},
		{"flagged white", trackCustomColorFlag | 0xFFFFFF, true},
		{"missing flag bit", 0xFF0000, false},
		{"stray high bits", trackCustomColorFlag | 0x2FFFFFF, false},
		{"negative", -1, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ColorEdit(1, "Kick", testCase.color).Validate()
			if testCase.valid && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !testCase.valid && !errors.Is(err, ErrInvalidTrackEdit) {
				t.Fatalf("Validate() = %v, want ErrInvalidTrackEdit", err)
			}
		})
	}
}

func TestColorEditLuaGuardsOnNameAndWritesTheColor(t *testing.T) {
	color := int64(trackCustomColorFlag | 0xFF0000)
	lua, err := ColorEdit(2, "Bass", color).Lua("/tmp/receipt.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`if not ok or current ~= expected then`,
		`local prior = reaper.GetMediaTrackInfo_Value(tr, "I_CUSTOMCOLOR")`,
		`reaper.SetMediaTrackInfo_Value(tr, "I_CUSTOMCOLOR", ` + strconv.FormatInt(color, 10) + `)`,
		`write_receipt("applied\n" .. string.format("%d", prior))`,
	} {
		if !strings.Contains(lua, want) {
			t.Fatalf("generated Lua missing %q:\n%s", want, lua)
		}
	}
}

func TestToggleEditsGuardOnNameAndUseTheVerifiedRecArmCall(t *testing.T) {
	cases := []struct {
		name     string
		edit     TrackEdit
		property string
		setCall  string
	}{
		{"mute on", MuteEdit(1, "Kick", true), "B_MUTE", `reaper.SetMediaTrackInfo_Value(tr, "B_MUTE", 1)`},
		{"mute off", MuteEdit(1, "Kick", false), "B_MUTE", `reaper.SetMediaTrackInfo_Value(tr, "B_MUTE", 0)`},
		{"solo on", SoloEdit(1, "Kick", true), "I_SOLO", `reaper.SetMediaTrackInfo_Value(tr, "I_SOLO", 1)`},
		// Record-arm needs CSurf_OnRecArmChange, not a plain
		// SetMediaTrackInfo_Value write — verified against live REAPER.
		{"arm on", ArmEdit(1, "Kick", true), "I_RECARM", `reaper.CSurf_OnRecArmChange(tr, 1)`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			lua, err := testCase.edit.Lua("/tmp/receipt.txt")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(lua, `reaper.GetMediaTrackInfo_Value(tr, "`+testCase.property+`")`) {
				t.Fatalf("missing prior-value read for %s:\n%s", testCase.property, lua)
			}
			if !strings.Contains(lua, testCase.setCall) {
				t.Fatalf("missing expected mutation call %q:\n%s", testCase.setCall, lua)
			}
			if !strings.Contains(lua, `write_receipt("applied\n" .. (prior ~= 0 and "1" or "0"))`) {
				t.Fatalf("missing boolean receipt write:\n%s", lua)
			}
		})
	}
}

func TestToggleInverseFlipsTheBooleanGuardedOnTheSameName(t *testing.T) {
	forward := MuteEdit(3, "Guitar", true)
	inverse := forward.Inverse("1") // the receipt reported the prior state was muted
	if inverse.Kind != TrackEditMute || inverse.Index != 3 || inverse.ExpectedName != "Guitar" || !inverse.NewBool {
		t.Fatalf("inverse = %+v", inverse)
	}
	unmuted := forward.Inverse("0")
	if unmuted.NewBool {
		t.Fatalf("inverse from prior=0 should restore unmuted: %+v", unmuted)
	}
}

func TestColorInverseParsesThePriorIntegerGuardedOnTheSameName(t *testing.T) {
	forward := ColorEdit(1, "Kick", trackCustomColorFlag|0xFF0000)
	inverse := forward.Inverse("0")
	if inverse.Kind != TrackEditColor || inverse.Index != 1 || inverse.ExpectedName != "Kick" || inverse.NewColor != 0 {
		t.Fatalf("inverse = %+v", inverse)
	}
}

func TestMoveEditValidatesTargetPosition(t *testing.T) {
	if err := MoveEdit(2, "Bass", 0).Validate(); !errors.Is(err, ErrInvalidTrackEdit) {
		t.Fatalf("target 0 = %v, want ErrInvalidTrackEdit", err)
	}
	if err := MoveEdit(2, "Bass", -1).Validate(); !errors.Is(err, ErrInvalidTrackEdit) {
		t.Fatalf("negative target = %v, want ErrInvalidTrackEdit", err)
	}
	if err := MoveEdit(2, "Bass", 4).Validate(); err != nil {
		t.Fatalf("valid move rejected: %v", err)
	}
}

func TestMoveEditLuaUsesTheVerifiedDirectionalBeforeIndex(t *testing.T) {
	// Verified against live REAPER (tasks-reaper-track-strips.md group 4.1):
	// backward moves use target-1; forward moves use target, uncompensated.
	cases := []struct {
		name         string
		source, dest int
		wantBefore   int
	}{
		{"backward to first", 3, 1, 0},
		{"forward to end", 2, 4, 4},
		{"adjacent forward", 1, 2, 2},
		{"no-op position", 2, 2, 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			lua, err := MoveEdit(testCase.source, "Kick", testCase.dest).Lua("/tmp/receipt.txt")
			if err != nil {
				t.Fatal(err)
			}
			want := "reaper.ReorderSelectedTracks(" + strconv.Itoa(testCase.wantBefore) + ", 0)"
			if !strings.Contains(lua, want) {
				t.Fatalf("missing %q:\n%s", want, lua)
			}
		})
	}
}

func TestMoveEditLuaGuardsOnNameAndSelectsBeforeReordering(t *testing.T) {
	lua, err := MoveEdit(2, "Bass", 4).Lua("/tmp/receipt.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`if not ok or current ~= expected then`,
		`reaper.SetOnlyTrackSelected(tr)`,
		`write_receipt("applied\n" .. index)`,
	} {
		if !strings.Contains(lua, want) {
			t.Fatalf("generated Lua missing %q:\n%s", want, lua)
		}
	}
}

func TestMoveInverseGuardsOnTheNewPositionAndRestoresTheOldOne(t *testing.T) {
	forward := MoveEdit(2, "Bass", 4)
	inverse := forward.Inverse("2")
	if inverse.Kind != TrackEditMove || inverse.Index != 4 || inverse.ExpectedName != "Bass" || inverse.NewIndex != 2 {
		t.Fatalf("inverse = %+v", inverse)
	}
}

func TestInverseRestoresThePriorNameGuardedOnWhatOriWrote(t *testing.T) {
	forward := RenameEdit(2, "Drums", "Kick")
	inverse := forward.Inverse("Drums")
	if inverse.Index != 2 || inverse.ExpectedName != "Kick" || inverse.NewName != "Drums" {
		t.Fatalf("inverse = %+v", inverse)
	}
	lua, err := inverse.Lua("/tmp/receipt.txt")
	if err != nil {
		t.Fatal(err)
	}
	// The inverse guards on the value Ori wrote, so a user edit in between
	// makes it refuse rather than clobber.
	if !strings.Contains(lua, `local expected = "\75\105\99\107"`) {
		t.Fatalf("inverse guard is not the value Ori wrote:\n%s", lua)
	}
}

func TestParseEditReceiptReadsTheOutcomeAndPriorValue(t *testing.T) {
	applied, err := ParseEditReceipt([]byte("applied\nDrums"))
	if err != nil || !applied.Applied || applied.Prior != "Drums" {
		t.Fatalf("applied receipt = %+v, %v", applied, err)
	}

	// Prior values keep their spacing, because the whole remainder is the value.
	spaced, err := ParseEditReceipt([]byte("applied\n  Lead Guitar  "))
	if err != nil || spaced.Prior != "  Lead Guitar  " {
		t.Fatalf("spaced receipt = %+v, %v", spaced, err)
	}

	// An unnamed track round-trips as an empty prior value.
	empty, err := ParseEditReceipt([]byte("applied\n"))
	if err != nil || !empty.Applied || empty.Prior != "" {
		t.Fatalf("empty receipt = %+v, %v", empty, err)
	}

	refused, err := ParseEditReceipt([]byte("guard_failed\n"))
	if err != nil || refused.Applied {
		t.Fatalf("guard_failed receipt = %+v, %v", refused, err)
	}

	for _, bad := range []string{"", "ok", "applied-ish\nDrums", "error: boom"} {
		if _, err := ParseEditReceipt([]byte(bad)); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("receipt %q = %v, want ErrInvalidReceipt", bad, err)
		}
	}
}
