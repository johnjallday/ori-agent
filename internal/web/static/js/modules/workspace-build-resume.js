// Resume opens the Create Workspace dialog in build mode on the open build.
// A page without the dialog goes Home, which has it. Shared by Today's
// unfinished-build row and the folder card's "Open that draft".
export function resumeWorkspaceBuild(win = window) {
  const manager = win.sessionManager;
  if (manager?.showAddWorkspaceModal && win.document?.getElementById?.('addFolderModal')) {
    win.PersonalAssistantPanel?.close?.({ restoreFocus: false });
    manager.showAddWorkspaceModal({ entryPoint: 'personal_assistant_ask', buildResume: true });
    return 'opened';
  }
  win.location.href = '/?build=resume';
  return 'navigated';
}
