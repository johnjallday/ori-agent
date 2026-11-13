package knownfolder

import (
	"path/filepath"
	"testing"
)

func TestLinuxUserFoldersAreDataNotShell(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name, id, content, want string
		fail                    bool
	}{
		{"translated", "desktop", `XDG_DESKTOP_DIR="$HOME/Bureau"`, filepath.Join(home, "Bureau"), false},
		{"remapped", "documents", `XDG_DOCUMENTS_DIR="/srv/Documents"`, "/srv/Documents", false},
		{"missing settings", "downloads", "", filepath.Join(home, "Downloads"), false},
		{"other entry", "downloads", `XDG_DESKTOP_DIR="$HOME/Bureau"`, filepath.Join(home, "Downloads"), false},
		{"disabled", "desktop", `XDG_DESKTOP_DIR="$HOME/"`, home, false},
		{"braced home", "documents", `XDG_DOCUMENTS_DIR="${HOME}/Dokumente" # comment`, filepath.Join(home, "Dokumente"), false},
		{"escaped quotes", "documents", `XDG_DOCUMENTS_DIR="$HOME/My \"Documents\""`, filepath.Join(home, `My "Documents"`), false},
		{"shell", "desktop", `XDG_DESKTOP_DIR="$(touch /tmp/never)"`, "", true},
		{"backticks", "desktop", "XDG_DESKTOP_DIR=\"`whoami`\"", "", true},
		{"commands after", "desktop", `XDG_DESKTOP_DIR="$HOME/Desktop"; touch /tmp/never`, "", true},
		{"unknown expansion", "desktop", `XDG_DESKTOP_DIR="$OTHER/Desktop"`, "", true},
		{"unquoted", "desktop", `XDG_DESKTOP_DIR=$HOME/Desktop`, "", true},
		{"unterminated", "desktop", `XDG_DESKTOP_DIR="$HOME/Desktop`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := linuxUserFolder(home, tc.id, tc.content)
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("got %q %v; want %q error=%v", got, err, tc.want, tc.fail)
			}
		})
	}
}
