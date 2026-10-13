package usagelimits

import (
	"errors"
	"strings"
)

// renderClaudeMirrorWrapper captures the complete input before chaining. A
// pipeline to an existing command that ignores stdin can truncate tee's copy
// with SIGPIPE; replaying the temp file preserves every byte in that case too.
// Only the shell, tee and mv are required, besides the user's existing command.
func renderClaudeMirrorWrapper(mirrorPath, previousCommand string) (string, error) {
	if mirrorPath == "" || strings.ContainsRune(mirrorPath, 0) || strings.ContainsRune(previousCommand, 0) {
		return "", errors.New("invalid status line mirror configuration")
	}
	chain := ""
	if previousCommand != "" {
		chain = "/bin/sh -c " + quoteMirrorShell(previousCommand) + " < \"$tmp\" || status=$?\n"
	}
	return `#!/bin/sh
# Ori Claude Code usage mirror. Installed only with explicit user consent.
umask 077
mirror=` + quoteMirrorShell(mirrorPath) + `
tmp="${mirror}.tmp.$$"
# Refuse to clobber an existing temporary file, including a symlink.
(set -C; : > "$tmp") || exit 1
tee "$tmp" > /dev/null || exit 1
status=0
` + chain + `mv -f "$tmp" "$mirror" || exit 1
exit "$status"
`, nil
}

func quoteMirrorShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
