package usagelimits

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClaudeMirrorWrapperPreservesInputAndPreviousOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX status line wrapper")
	}
	for _, tt := range []struct {
		name, command, input, output string
		wantFailure                  bool
	}{
		{"no previous command", "", `{"rate_limits":{"five_hour":{"used_percentage":23}}}`, "", false},
		{"previous command reads stdin", `while IFS= read -r line; do printf '<%s>\n' "$line"; done`, "one\ntwo\n", "<one>\n<two>\n", false},
		{"ignores large stdin", `printf 'original status'`, strings.Repeat("payload\n", 100000), "original status", false},
		{"command exits nonzero", `printf 'original failure'; exit 7`, "complete input", "original failure", true},
		{"literal quoting", `printf '%s' "it's \$HOME"`, "arbitrary\x00bytes\n", "it's $HOME", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "spaces ' quotes $(not-a-command)")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			mirror := filepath.Join(dir, ClaudeMirrorFilename)
			script, err := renderClaudeMirrorWrapper(mirror, tt.command)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "wrapper")
			if err := os.WriteFile(path, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", path) // #nosec G204 -- generated wrapper in the test's own temporary directory, with invented input/commands.
			cmd.Stdin = strings.NewReader(tt.input)
			output, err := cmd.Output()
			if (err != nil) != tt.wantFailure || string(output) != tt.output {
				t.Fatalf("previous command behavior changed: output=%q err=%v", output, err)
			}
			data, err := os.ReadFile(mirror)
			if err != nil || !bytes.Equal(data, []byte(tt.input)) {
				t.Fatal("mirror did not preserve all stdin bytes")
			}
			info, err := os.Stat(mirror)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("mirror must remain owner-only")
			}
			leftovers, _ := filepath.Glob(mirror + ".tmp.*")
			if len(leftovers) != 0 {
				t.Fatal("successful atomic replacement left temporary files")
			}
		})
	}
}
