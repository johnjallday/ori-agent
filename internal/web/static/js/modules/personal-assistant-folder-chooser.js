// The folder chooser's render decision, shared by Home's folder card and the
// interview wizard. It has no side effects: importing it mounts nothing.

export const FOLDER_CHIP_ICON = '\u{1F4C1}';

// folderChooserView is the chooser's render decision: which chips to show,
// whether the native picker chip appears, and the note that replaces it.
export function folderChooserView(digest) {
  const chips = Array.isArray(digest?.chips)
    ? digest.chips
        .filter(chip => chip && typeof chip.id === 'string' && chip.id.trim())
        .map(chip => ({ id: chip.id.trim(), label: String(chip.label || chip.id).trim() }))
    : [];
  const pickerVisible = digest?.picker_available === true;
  // The server explains what can be chosen; the fallbacks only cover a
  // payload without a note. A chooser with nothing to press must say so.
  let note = String(digest?.picker_note || '').trim();
  if (!note && !pickerVisible) {
    note = chips.length ? 'Pick a folder from the list for now.' : 'No folder can be chosen here.';
  }
  return {
    chips,
    pickerVisible,
    filePickerVisible: pickerVisible && digest?.file_picker_available === true,
    pickerLabel: 'Pick another folder…',
    filePickerLabel: 'Pick a file…',
    note
  };
}
