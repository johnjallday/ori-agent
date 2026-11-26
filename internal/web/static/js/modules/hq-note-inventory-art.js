/**
 * hq-note-inventory-art.js — the small pictures on the note inventory's slots.
 *
 * Each picture is fixed local art for something known about a slot: its kind
 * (an Inbox note is a letter, a journal page a calendar, a folder a folder),
 * or for a project a stable pick from its name. None is chosen from a note's
 * text, which the inventory has not read. Every picture is decorative: the
 * slot's own words say what it is.
 */

const PICTURES = Object.freeze({
  // A folded map with a small house: a project.
  map:
    '<path d="M6 13 L17 9 L29 13 L41 9 V35 L29 39 L17 35 L6 39 Z" fill="#7fb59a"/>' +
    '<path d="M17 9 V35 M29 13 V39" stroke="#4f8a6d" stroke-width="1.5"/>' +
    '<path d="M9 30 C15 24 20 28 26 22 S36 18 39 15" stroke="#e8d6b4" stroke-width="2" fill="none"/>' +
    '<path d="M27 21 L33 16 L39 21 V29 H27 Z" fill="#c9733f"/>' +
    '<rect x="31" y="24" width="4" height="5" fill="#5a3a0c"/>',
  // A blueprint sheet: a project.
  blueprint:
    '<rect x="7" y="9" width="32" height="26" fill="#5f9fb8"/>' +
    '<rect x="10" y="12" width="26" height="20" fill="none" stroke="#d9eef5" stroke-width="1.5"/>' +
    '<path d="M10 22 H22 V12 M22 22 V32 M28 22 H36" stroke="#d9eef5" stroke-width="1.5"/>' +
    '<rect x="9" y="35" width="32" height="4" fill="#3d6f84"/>',
  // A clipboard with a checklist: a project.
  clipboard:
    '<rect x="11" y="8" width="26" height="32" rx="1.5" fill="#d9c08e"/>' +
    '<rect x="14" y="12" width="20" height="25" fill="#f6ead6"/>' +
    '<rect x="18" y="5" width="12" height="6" rx="1" fill="#6f7a7f"/>' +
    '<path d="M16 17 l2 2 l3 -4" stroke="#4f8a6d" stroke-width="1.8" fill="none"/>' +
    '<rect x="23" y="16" width="9" height="2" fill="#8a7a5c"/>' +
    '<rect x="16" y="23" width="4" height="4" fill="none" stroke="#8a7a5c" stroke-width="1.2"/>' +
    '<rect x="23" y="24" width="9" height="2" fill="#8a7a5c"/>' +
    '<rect x="23" y="31" width="7" height="2" fill="#8a7a5c"/>',
  // A framed picture: a project.
  frame:
    '<rect x="6" y="11" width="28" height="24" fill="#c9733f"/>' +
    '<rect x="9" y="14" width="22" height="18" fill="#e8b9a0"/>' +
    '<path d="M9 32 L17 22 L23 28 L27 24 L31 28 V32 Z" fill="#7a5a48"/>' +
    '<circle cx="34" cy="20" r="8" fill="#5f9fb8"/>' +
    '<rect x="32" y="18" width="4" height="4" fill="#e8b54b"/>',
  // An envelope: an Inbox note.
  letter:
    '<rect x="7" y="12" width="34" height="24" rx="1.5" fill="#e8d6b4"/>' +
    '<path d="M7 13 L24 26 L41 13" stroke="#8a6a3c" stroke-width="2" fill="none"/>' +
    '<rect x="31" y="15" width="7" height="6" fill="#c9733f"/>',
  // A calendar page: a journal note.
  calendar:
    '<rect x="9" y="10" width="30" height="28" rx="1.5" fill="#f6ead6"/>' +
    '<rect x="9" y="10" width="30" height="8" fill="#c9733f"/>' +
    '<rect x="14" y="6" width="3" height="7" fill="#6f7a7f"/><rect x="31" y="6" width="3" height="7" fill="#6f7a7f"/>' +
    '<rect x="14" y="22" width="5" height="4" fill="#8a7a5c"/><rect x="22" y="22" width="5" height="4" fill="#8a7a5c"/>' +
    '<rect x="30" y="22" width="5" height="4" fill="#e8b54b"/><rect x="14" y="29" width="5" height="4" fill="#8a7a5c"/>',
  // A folder: a folder in a connected notebook.
  folder:
    '<path d="M6 14 H19 L22 17 H42 V37 H6 Z" fill="#d9a94a"/>' +
    '<rect x="6" y="19" width="36" height="18" fill="#e8b54b"/>',
  // A plain page: anything else.
  page:
    '<path d="M12 7 H30 L37 14 V41 H12 Z" fill="#f6ead6"/>' +
    '<path d="M30 7 V14 H37" fill="#d9c08e"/>' +
    '<rect x="16" y="19" width="16" height="2" fill="#8a7a5c"/>' +
    '<rect x="16" y="25" width="16" height="2" fill="#8a7a5c"/>' +
    '<rect x="16" y="31" width="10" height="2" fill="#8a7a5c"/>'
});

export const INVENTORY_PICTURES = Object.freeze(Object.keys(PICTURES));

// inventoryPicture returns the markup for one picture, or the plain page.
export function inventoryPicture(name) {
  const body = PICTURES[name] || PICTURES.page;
  return (
    '<svg viewBox="0 0 48 48" width="48" height="48" aria-hidden="true" focusable="false">' +
    '<rect x="4" y="40" width="40" height="3" fill="rgba(0,0,0,0.22)"/>' +
    body +
    '</svg>'
  );
}
