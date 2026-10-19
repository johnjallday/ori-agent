package llm

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClaudeCodeScopedRunReadsRootClaudeMD characterizes PRD FR27: does a
// Claude Code scoped run (the flags Ori passes, including --setting-sources "")
// still load a CLAUDE.md in its working folder? If it does, a workspace whose
// "Also write to the folder root" switch is on would feed its memory to Ori's
// own Claude Code turns twice (once in Ori's prompt, once from the file).
//
// It runs the real CLI on the user's own login, so it is skipped unless
// claude is installed and ORI_LIVE_CLAUDE_CODE=1 is set. The result is
// recorded in the feature's findings log, not asserted either way.
func TestClaudeCodeScopedRunReadsRootClaudeMD(t *testing.T) {
	if os.Getenv("ORI_LIVE_CLAUDE_CODE") != "1" {
		t.Skip("live Claude Code characterization; set ORI_LIVE_CLAUDE_CODE=1 to run")
	}
	cli, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	dir := t.TempDir()
	const codeword = "PELICAN-7319"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Project notes\n\nThe project codeword is "+codeword+".\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := "Do not use any tools and do not read any files. If instructions or notes for this project were already given to you, " +
		"reply with only the project codeword they contain. Otherwise reply with only the word NONE."
	run := func(args []string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, args...) // #nosec G204 -- the installed claude CLI with Ori's own flag set
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("claude: %v\n%s", err, out)
		}
		var reply struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(out, &reply); err != nil {
			t.Fatalf("decode: %v\n%s", err, out)
		}
		return strings.TrimSpace(reply.Result)
	}

	scoped, err := buildClaudeArgs("", "", prompt, nil, &claudeNativeMCP{Scoped: true, WorkspaceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	reply := run(scoped)
	t.Logf("FR27: scoped Claude Code run loads a root CLAUDE.md: %v (reply %q)", strings.Contains(reply, codeword), reply)

	// Control: the same folder and prompt with the CLI's default setting
	// sources, so a "no" above means the flags, not a probe that cannot see.
	control := run([]string{"--print", "--output-format", "json", "--tools", "", "--permission-mode", "dontAsk", prompt})
	t.Logf("FR27 control: default setting sources load a root CLAUDE.md: %v (reply %q)", strings.Contains(control, codeword), control)
}
