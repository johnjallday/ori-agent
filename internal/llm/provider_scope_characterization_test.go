package llm

import (
	"os/exec"
	"strings"
	"testing"
)

// These tests record the provider characterization performed against Codex CLI
// 0.147.0 and Claude Code 2.1.233 on macOS before runtime grants were designed.
// They intentionally inspect documented CLI help rather than assuming a flag:
//
//   - Codex exposes workspace-write plus --add-dir, and a real home-directory
//     fixture proved workspace + one added root writable while a sibling was
//     denied. (Fixtures under /tmp are invalid evidence because Codex's sandbox
//     intentionally allows the system temporary directory.)
//   - Claude exposes --add-dir and acceptEdits. A real fixture run with
//     acceptEdits wrote through Write to cwd + one --add-dir root and denied a
//     sibling. bypassPermissions is therefore unnecessary and cannot be used as
//     evidence of confinement.
//   - Neither CLI exposes a loopback-only shell-network flag. Runtime scope must
//     keep general shell network disabled and reach loopback only through a
//     compiled, capability-owned MCP/helper operation.
func TestInstalledCLIsExposeCharacterizedNarrowFilesystemMechanisms(t *testing.T) {
	cases := []struct {
		binary string
		args   []string
		want   []string
	}{
		{binary: "codex", args: []string{"exec", "--help"}, want: []string{"--add-dir", "workspace-write"}},
		{binary: "claude", args: []string{"--help"}, want: []string{"--add-dir", "acceptEdits"}},
	}
	for _, tc := range cases {
		t.Run(tc.binary, func(t *testing.T) {
			path, err := exec.LookPath(tc.binary)
			if err != nil {
				t.Skipf("%s is not installed", tc.binary)
			}
			output, err := exec.Command(path, tc.args...).CombinedOutput()
			if err != nil {
				t.Fatalf("%s help: %v: %s", tc.binary, err, output)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(output), want) {
					t.Errorf("%s does not advertise %q", tc.binary, want)
				}
			}
		})
	}
}

// This starts red by design: the pre-feature Claude native posture used
// bypassPermissions. The installed CLI characterization above proves
// acceptEdits + --add-dir can enforce the approved roots, so implementation
// must add a distinct capability scope rather than bless this broad posture.
func TestClaudeRuntimeGrantMustNotRelyOnBypassPermissions(t *testing.T) {
	nat := &claudeNativeMCP{WorkspaceDir: "/workspace", AdditionalWritableRoots: []string{"/runner"}, Scoped: true}
	args, err := buildClaudeArgs("haiku", "scope test", nil, nat)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "bypassPermissions") {
		t.Fatal("runtime capability scope cannot use bypassPermissions as proof of confinement")
	}
}
