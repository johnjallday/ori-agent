//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// SHFileOperationW constants (see shellapi.h).
const (
	foDelete          = 0x0003
	fofAllowUndo      = 0x0040 // send to the Recycle Bin instead of deleting
	fofNoConfirmation = 0x0010
	fofSilent         = 0x0004
	fofNoErrorUI      = 0x0400
)

// shFileOpStructW mirrors SHFILEOPSTRUCTW from shellapi.h.
type shFileOpStructW struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

var (
	modshell32           = syscall.NewLazyDLL("shell32.dll")
	procSHFileOperationW = modshell32.NewProc("SHFileOperationW")
)

// moveToTrash sends abs to the Windows Recycle Bin via SHFileOperation with the
// FOF_ALLOWUNDO flag. The Recycle Bin exposes no stable path, so the returned
// token is empty; restore works by original path.
func moveToTrash(abs string) (string, error) {
	// pFrom must be a double-null-terminated list of paths.
	from, err := syscall.UTF16FromString(abs)
	if err != nil {
		return "", err
	}
	from = append(from, 0) // second NUL terminates the list

	op := shFileOpStructW{
		wFunc:  foDelete,
		pFrom:  &from[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI,
	}

	ret, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if ret != 0 {
		return "", fmt.Errorf("SHFileOperation failed (code 0x%x) moving %q to Recycle Bin", ret, abs)
	}
	if op.fAnyOperationsAborted != 0 {
		return "", fmt.Errorf("move to Recycle Bin was aborted for %q", abs)
	}
	return "", nil
}

// restoreFromTrash performs a best-effort restore from the Recycle Bin by
// matching the item's original location, using the Shell Automation API via
// PowerShell. If it fails, the item remains in the Recycle Bin for manual
// restore. Verb names are locale-dependent, so this is not guaranteed.
func restoreFromTrash(originalPath, _ string) error {
	const script = `
$ErrorActionPreference = 'Stop'
$target = $env:ORI_RESTORE_TARGET
$shell = New-Object -ComObject Shell.Application
$bin = $shell.Namespace(0xA)
foreach ($item in @($bin.Items())) {
  $from = $item.ExtendedProperty('System.Recycle.DeletedFrom')
  if ($from) {
    $full = Join-Path $from $item.Name
    if ($full -ieq $target) {
      $verb = $item.Verbs() | Where-Object { ($_.Name -replace '&','') -match '^(Restore|Put Back|Undelete)$' } | Select-Object -First 1
      if ($verb) { $verb.DoIt(); exit 0 }
    }
  }
}
exit 1
`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "ORI_RESTORE_TARGET="+originalPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not restore %q from the Recycle Bin (it remains there for manual restore): %w", originalPath, err)
	}

	// The restore verb completes asynchronously; wait briefly for the folder to
	// reappear so the caller can re-register it.
	for range 40 {
		if _, err := os.Stat(originalPath); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("restore of %q from the Recycle Bin did not complete in time", originalPath)
}
