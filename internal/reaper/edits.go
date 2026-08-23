package reaper

import (
	"errors"
	"strconv"
	"strings"
)

// Track edits are the mutations that have no REAPER command ID, so each one is
// generated Lua run through the installed runner.
//
// REAPER's Web Remote identifies tracks only by position — there is no GUID at
// read time — so every edit is a compare-and-swap: the generated Lua re-reads
// the name at the index and applies the change only if it still matches the
// name Ori believed was there. This is the correctness centre of the feature,
// and it holds for every kind below: renaming a track changes the guard value
// itself, but coloring or toggling a track still guards on its name.
// The Lua also captures the prior value into a receipt file so the server can
// build a specific inverse for Undo.
const (
	TrackEditRename = "rename"
	TrackEditColor  = "color"
	TrackEditMute   = "mute"
	TrackEditSolo   = "solo"
	TrackEditArm    = "arm"
	TrackEditMove   = "move"

	// receiptFileName sits beside last_status.txt under the canonical runner
	// root and is cleared before every run.
	receiptFileName = "last_receipt.txt"

	// maxTrackNameBytes bounds a name well below anything REAPER accepts, so
	// generated Lua stays small and a hostile name cannot balloon the script.
	maxTrackNameBytes = 512

	// maxTrackColorValue bounds a color to the flag bit plus 24 bits of RGB —
	// REAPER's actual I_CUSTOMCOLOR range — so no stray bit reaches Lua.
	// trackCustomColorFlag is defined in client.go, alongside Track.Color.
	maxTrackColorValue = trackCustomColorFlag | 0xFFFFFF
)

var (
	ErrInvalidTrackEdit            = errors.New("invalid REAPER track edit")
	ErrInvalidReceipt              = errors.New("invalid REAPER edit receipt")
	ErrTrackIdentityChanged        = errors.New("REAPER track identity changed")
	ErrTrackMoveTargetOutOfRange   = errors.New("REAPER track move target is out of range")
	ErrFolderDepthUnavailable      = errors.New("REAPER folder depth is unavailable")
	ErrFolderParentMoveUnsupported = errors.New("REAPER folder-parent moves are unsupported")
)

// TrackEdit is one guarded single-track mutation. Index is 1-based to match
// the Web Remote TRACK line the console rendered from; ExpectedName is the
// identity guard for every kind, and the empty string is a legitimate value
// because REAPER reports an unnamed track's name as empty on both the Web
// Remote and P_NAME paths. Only the field matching Kind is meaningful.
type TrackEdit struct {
	Kind         string `json:"operation"`
	Index        int    `json:"index"`
	ExpectedName string `json:"expected_name"`
	NewName      string `json:"new_name,omitempty"`  // TrackEditRename
	NewColor     int64  `json:"new_color,omitempty"` // TrackEditColor
	NewBool      bool   `json:"new_bool,omitempty"`  // TrackEditMute, TrackEditSolo, TrackEditArm
	NewIndex     int    `json:"new_index,omitempty"` // TrackEditMove, 1-based target position
}

// RenameEdit builds a guarded rename.
func RenameEdit(index int, expectedName, newName string) TrackEdit {
	return TrackEdit{Kind: TrackEditRename, Index: index, ExpectedName: expectedName, NewName: newName}
}

// ColorEdit builds a guarded color change. color is REAPER's raw
// I_CUSTOMCOLOR integer; pass 0 to clear a track's color.
func ColorEdit(index int, expectedName string, color int64) TrackEdit {
	return TrackEdit{Kind: TrackEditColor, Index: index, ExpectedName: expectedName, NewColor: color}
}

// MuteEdit, SoloEdit, and ArmEdit build the three guarded toggle changes.
func MuteEdit(index int, expectedName string, muted bool) TrackEdit {
	return TrackEdit{Kind: TrackEditMute, Index: index, ExpectedName: expectedName, NewBool: muted}
}

func SoloEdit(index int, expectedName string, soloed bool) TrackEdit {
	return TrackEdit{Kind: TrackEditSolo, Index: index, ExpectedName: expectedName, NewBool: soloed}
}

func ArmEdit(index int, expectedName string, armed bool) TrackEdit {
	return TrackEdit{Kind: TrackEditArm, Index: index, ExpectedName: expectedName, NewBool: armed}
}

// MoveEdit builds a guarded reorder from a 1-based source position to a
// 1-based target position.
func MoveEdit(index int, expectedName string, newIndex int) TrackEdit {
	return TrackEdit{Kind: TrackEditMove, Index: index, ExpectedName: expectedName, NewIndex: newIndex}
}

// IsFolderParent is the one authoritative classification used by move
// preflights. A negative closing track remains movable; only a positive depth
// starts a folder whose group movement is outside this feature's contract.
func (t Track) IsFolderParent() bool {
	return t.FolderDepth > 0
}

// ValidateTrackMove checks one move against a fresh live snapshot before any
// Lua runs. The final generated Lua repeats the folder-parent check to close
// the time-of-check/time-of-use gap.
func ValidateTrackMove(state State, edit TrackEdit) error {
	if edit.Kind != TrackEditMove || edit.Validate() != nil {
		return ErrInvalidTrackEdit
	}
	if edit.NewIndex > state.TrackCount {
		return ErrTrackMoveTargetOutOfRange
	}
	var source *Track
	for index := range state.Tracks {
		if state.Tracks[index].Index == edit.Index {
			source = &state.Tracks[index]
			break
		}
	}
	if source == nil || source.Name != edit.ExpectedName {
		return ErrTrackIdentityChanged
	}
	if !state.FolderDepthAvailable {
		return ErrFolderDepthUnavailable
	}
	if source.IsFolderParent() {
		return ErrFolderParentMoveUnsupported
	}
	return nil
}

// Inverse returns the edit that reverses this one, guarded on the value Ori
// wrote. Applying it is only correct when the forward edit actually applied,
// so callers must build it from the receipt rather than from hope. prior is
// the receipt's raw prior-value text, interpreted per kind.
func (e TrackEdit) Inverse(prior string) TrackEdit {
	switch e.Kind {
	case TrackEditColor:
		priorColor, _ := strconv.ParseInt(strings.TrimSpace(prior), 10, 64)
		return TrackEdit{Kind: TrackEditColor, Index: e.Index, ExpectedName: e.ExpectedName, NewColor: priorColor}
	case TrackEditMute, TrackEditSolo, TrackEditArm:
		return TrackEdit{
			Kind: e.Kind, Index: e.Index, ExpectedName: e.ExpectedName,
			NewBool: strings.TrimSpace(prior) == "1",
		}
	case TrackEditMove:
		// The move left the name unchanged, so the guard still matches; only
		// the position to check has moved, to where the forward edit put it.
		priorIndex, _ := strconv.Atoi(strings.TrimSpace(prior))
		return TrackEdit{
			Kind: TrackEditMove, Index: e.NewIndex, ExpectedName: e.ExpectedName, NewIndex: priorIndex,
		}
	default: // TrackEditRename
		// The forward rename changed what identifies the track going forward;
		// every other kind leaves the name — and so the guard — untouched.
		return TrackEdit{Kind: TrackEditRename, Index: e.Index, ExpectedName: e.NewName, NewName: prior}
	}
}

// Validate rejects an edit before any Lua is generated.
func (e TrackEdit) Validate() error {
	if e.Index < 1 {
		return ErrInvalidTrackEdit
	}
	if len(e.ExpectedName) > maxTrackNameBytes {
		return ErrInvalidTrackEdit
	}
	switch e.Kind {
	case TrackEditRename:
		if len(e.NewName) > maxTrackNameBytes || strings.TrimSpace(e.NewName) == "" {
			return ErrInvalidTrackEdit
		}
	case TrackEditColor:
		if e.NewColor < 0 || e.NewColor > maxTrackColorValue {
			return ErrInvalidTrackEdit
		}
		if e.NewColor != 0 && e.NewColor&trackCustomColorFlag == 0 {
			return ErrInvalidTrackEdit
		}
	case TrackEditMute, TrackEditSolo, TrackEditArm:
		// NewBool has no invalid values.
	case TrackEditMove:
		// A target beyond the current track count is rejected by the caller,
		// which knows the live count; this only rejects structural nonsense.
		if e.NewIndex < 1 {
			return ErrInvalidTrackEdit
		}
	default:
		return ErrInvalidTrackEdit
	}
	return nil
}

// Lua generates the guarded script for this edit. receiptPath is a trusted
// absolute path under the canonical runner root, supplied by the Runner; it is
// never derived from a browser request and never travels back to one.
func (e TrackEdit) Lua(receiptPath string) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	if !strings.HasPrefix(receiptPath, "/") || strings.Contains(receiptPath, "\n") {
		return "", ErrInvalidTrackEdit
	}

	var script strings.Builder
	script.WriteString("-- Generated by Ori: guarded single-track edit.\n")
	script.WriteString("local receipt_path = " + luaString(receiptPath) + "\n")
	script.WriteString("local expected = " + luaString(e.ExpectedName) + "\n")
	script.WriteString("local index = " + strconv.Itoa(e.Index) + "\n\n")
	script.WriteString(`local function write_receipt(body)
  local file = io.open(receipt_path, "wb")
  if file then
    file:write(body)
    file:close()
  end
end

-- Compare and swap: REAPER identifies tracks only by position, so refuse
-- rather than guess if the track at this index is no longer the one Ori read.
local tr = reaper.GetTrack(0, index - 1)
if not tr then
  write_receipt("guard_failed\n")
  return
end

local ok, current = reaper.GetSetMediaTrackInfo_String(tr, "P_NAME", "", false)
if not ok or current ~= expected then
  write_receipt("guard_failed\n")
  return
end

`)
	if e.Kind == TrackEditMove {
		script.WriteString(`-- Folder groups must move atomically. Refuse before opening an undo
-- block if this source became a folder parent after the HTTP preflight.
local folder_depth = reaper.GetMediaTrackInfo_Value(tr, "I_FOLDERDEPTH")
if folder_depth > 0 then
  write_receipt("folder_parent\n")
  return
end

`)
	}
	script.WriteString(e.mutationLua())
	return script.String(), nil
}

// applyLua emits just the mutation call itself, assuming `tr` is already
// bound and the guard already passed. This is the one piece shared between a
// single guarded edit (wrapped below with its own undo block and receipt) and
// a bulk plan (internal/reaper/bulk.go), which wraps a whole ordered set of
// these in one undo block and needs no per-edit receipt at all — a plan
// reverses through REAPER's global undo, not a specific inverse.
func (e TrackEdit) applyLua() string {
	switch e.Kind {
	case TrackEditColor:
		return "reaper.SetMediaTrackInfo_Value(tr, \"I_CUSTOMCOLOR\", " + strconv.FormatInt(e.NewColor, 10) + ")\n"
	case TrackEditMute:
		return "reaper.SetMediaTrackInfo_Value(tr, \"B_MUTE\", " + luaBool(e.NewBool) + ")\n"
	case TrackEditSolo:
		return "reaper.SetMediaTrackInfo_Value(tr, \"I_SOLO\", " + luaBool(e.NewBool) + ")\n"
	case TrackEditArm:
		// I_RECARM needs CSurf_OnRecArmChange rather than a plain
		// SetMediaTrackInfo_Value write — verified against live REAPER
		// (tasks-reaper-track-strips.md group 3.2).
		return "reaper.CSurf_OnRecArmChange(tr, " + luaBool(e.NewBool) + ")\n"
	case TrackEditMove:
		// The 0-based beforeIndex REAPER wants depends on direction, verified
		// against live REAPER (tasks-reaper-track-strips.md group 4.1): moving
		// a track past its own original slot shifts everything after it down
		// by one, so a forward move needs the target itself, not target-1.
		beforeIndex := e.NewIndex - 1
		if e.NewIndex > e.Index {
			beforeIndex = e.NewIndex
		}
		return "reaper.SetOnlyTrackSelected(tr)\n" +
			"reaper.ReorderSelectedTracks(" + strconv.Itoa(beforeIndex) + ", 0)\n"
	default: // TrackEditRename
		return "reaper.GetSetMediaTrackInfo_String(tr, \"P_NAME\", " + luaString(e.NewName) + ", true)\n"
	}
}

// mutationLua emits the guard-passed body for a single guarded edit: read the
// prior value, apply the new one inside its own undo block, and write the
// prior value to the receipt so a specific inverse can be built.
func (e TrackEdit) mutationLua() string {
	switch e.Kind {
	case TrackEditColor:
		return "local prior = reaper.GetMediaTrackInfo_Value(tr, \"I_CUSTOMCOLOR\")\n" +
			"reaper.Undo_BeginBlock()\n" +
			e.applyLua() +
			"reaper.Undo_EndBlock(\"Ori: recolor track\", -1)\n" +
			"reaper.TrackList_AdjustWindows(false)\n" +
			"reaper.UpdateArrange()\n\n" +
			"write_receipt(\"applied\\n\" .. string.format(\"%d\", prior))\n"
	case TrackEditMute:
		return e.toggleLua("B_MUTE", "mute")
	case TrackEditSolo:
		return e.toggleLua("I_SOLO", "solo")
	case TrackEditArm:
		return e.toggleLua("I_RECARM", "arm")
	case TrackEditMove:
		return "reaper.Undo_BeginBlock()\n" +
			e.applyLua() +
			"reaper.Undo_EndBlock(\"Ori: move track\", -1)\n" +
			"reaper.TrackList_AdjustWindows(false)\n" +
			"reaper.UpdateArrange()\n\n" +
			// The source position was already known without reading REAPER.
			"write_receipt(\"applied\\n\" .. index)\n"
	default: // TrackEditRename
		return "reaper.Undo_BeginBlock()\n" +
			e.applyLua() +
			"reaper.Undo_EndBlock(\"Ori: rename track\", -1)\n" +
			"reaper.TrackList_AdjustWindows(false)\n" +
			"reaper.UpdateArrange()\n\n" +
			"write_receipt(\"applied\\n\" .. current)\n"
	}
}

func (e TrackEdit) toggleLua(property, undoLabel string) string {
	return "local prior = reaper.GetMediaTrackInfo_Value(tr, \"" + property + "\")\n" +
		"reaper.Undo_BeginBlock()\n" +
		e.applyLua() +
		"reaper.Undo_EndBlock(\"Ori: " + undoLabel + " track\", -1)\n" +
		"reaper.TrackList_AdjustWindows(false)\n" +
		"reaper.UpdateArrange()\n\n" +
		"write_receipt(\"applied\\n\" .. (prior ~= 0 and \"1\" or \"0\"))\n"
}

func luaBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// EditReceipt is what the generated Lua reported back. Applied is the
// authority on whether the project changed: the runner reports ok whenever the
// script itself ran cleanly, including when the guard deliberately refused.
type EditReceipt struct {
	Applied bool
	Prior   string
	Refusal string
}

// ParseEditReceipt reads the receipt contract: the first line is applied,
// guard_failed, or folder_parent. For applied receipts, everything after the
// first newline is the prior value verbatim so names containing spaces round-trip.
func ParseEditReceipt(data []byte) (EditReceipt, error) {
	text := string(data)
	outcome := text
	prior := ""
	if newline := strings.IndexByte(text, '\n'); newline >= 0 {
		outcome = text[:newline]
		prior = text[newline+1:]
	}
	switch strings.TrimSpace(outcome) {
	case "applied":
		return EditReceipt{Applied: true, Prior: prior}, nil
	case "guard_failed":
		return EditReceipt{Applied: false, Refusal: "guard_failed"}, nil
	case "folder_parent":
		return EditReceipt{Applied: false, Refusal: "folder_parent"}, nil
	default:
		return EditReceipt{}, ErrInvalidReceipt
	}
}

// luaString encodes a Go string as a Lua literal using decimal byte escapes.
// Quotes, newlines, backslashes, and long-bracket sequences in a user-supplied
// track name therefore cannot terminate the literal or inject code.
func luaString(value string) string {
	var quoted strings.Builder
	quoted.Grow(len(value)*4 + 2)
	quoted.WriteByte('"')
	for i := 0; i < len(value); i++ {
		quoted.WriteByte('\\')
		quoted.WriteString(strconv.Itoa(int(value[i])))
	}
	quoted.WriteByte('"')
	return quoted.String()
}
