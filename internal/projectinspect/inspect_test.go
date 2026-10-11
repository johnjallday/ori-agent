package projectinspect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
)

func fixtures(t *testing.T) softwarefixture.Set {
	t.Helper()
	set, err := softwarefixture.Build(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { softwarefixture.Restore(set.Unreadable) })
	return set
}

func inspect(t *testing.T, dir string, opts Options) Evidence {
	t.Helper()
	evidence, err := Inspect(context.Background(), dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func paths(e Evidence) []string {
	out := make([]string, 0, len(e.Documents))
	for _, doc := range e.Documents {
		out = append(out, doc.Path)
	}
	return out
}

func hasGap(e Evidence, code, source string) bool {
	for _, gap := range e.Gaps {
		if gap.Code == code && (source == "" || gap.Source == source) {
			return true
		}
	}
	return false
}

func commands(e Evidence, declared bool) []string {
	var out []string
	for _, command := range e.Commands {
		if command.Declared == declared {
			out = append(out, command.Command)
		}
	}
	return out
}

// everything is every piece of text an inspection hands back, for asserting
// that something was never read or never kept.
func everything(e Evidence) string {
	var b strings.Builder
	for _, doc := range e.Documents {
		b.WriteString(doc.Path + "\n" + doc.Text + "\n")
	}
	if e.Purpose != nil {
		b.WriteString(e.Purpose.Text + "\n")
	}
	for _, excerpt := range e.Conventions {
		b.WriteString(excerpt.Text + "\n")
	}
	for _, command := range e.Commands {
		b.WriteString(command.Command + "\n" + command.Source + "\n")
	}
	for _, gap := range e.Gaps {
		b.WriteString(gap.Source + "\n")
	}
	b.WriteString(e.Git.Remote + "\n" + e.Git.Branch + "\n" + strings.Join(e.Stack, ",") + "\n")
	return b.String()
}

func TestInspect_ReadsOnlyTheAllowlistedFilesInPriorityOrder(t *testing.T) {
	set := fixtures(t)
	before, err := softwarefixture.Hash(set.GoNode)
	if err != nil {
		t.Fatal(err)
	}
	evidence := inspect(t, set.GoNode, Options{})

	want := []string{"AGENTS.md", "README.md", "go.mod", "package.json", "Makefile", ".github/workflows/ci.yml"}
	if got := paths(evidence); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("documents = %v, want %v", got, want)
	}
	all := everything(evidence)
	for _, never := range []string{".env", "never-read-this", "node_modules", "A dependency, not this project", "web/package.json", "widget-web-client", "main.go", "count.go"} {
		if strings.Contains(all, never) {
			t.Errorf("the inspection read or reported %q", never)
		}
	}
	if evidence.Purpose == nil || evidence.Purpose.Source != "README.md" ||
		evidence.Purpose.Text != "Widget is a small service that counts widgets and shows them on a web page." {
		t.Fatalf("purpose = %+v", evidence.Purpose)
	}
	if strings.Join(evidence.Stack, ",") != "Go,Node.js" {
		t.Fatalf("stack = %v", evidence.Stack)
	}
	declared := strings.Join(commands(evidence, true), "|")
	for _, command := range []string{"make build", "make test", "make ci-local", "npm run build", "npm run test", "npm run lint"} {
		if !strings.Contains("|"+declared+"|", "|"+command+"|") {
			t.Errorf("declared commands %q lack %q", declared, command)
		}
	}
	// What a manifest merely implies is kept apart from what the project wrote.
	if inferred := strings.Join(commands(evidence, false), "|"); inferred != "go build ./...|go test ./..." {
		t.Fatalf("inferred commands = %q", inferred)
	}
	if len(evidence.Conventions) != 1 || evidence.Conventions[0].Source != "AGENTS.md" || !strings.Contains(evidence.Conventions[0].Text, "its own feature worktree") {
		t.Fatalf("conventions = %+v", evidence.Conventions)
	}
	for _, code := range []string{GapNoReadme, GapNoInstructions, GapNoCommands, GapSeveralGuides} {
		if hasGap(evidence, code, "") {
			t.Errorf("unexpected gap %s", code)
		}
	}
	if evidence.Partial || evidence.InspectedAt.IsZero() || len(evidence.Revision) != 64 {
		t.Fatalf("partial=%t at=%v revision=%q", evidence.Partial, evidence.InspectedAt, evidence.Revision)
	}

	// Reading changed nothing, and the same files give the same revision.
	after, err := softwarefixture.Hash(set.GoNode)
	if err != nil || after != before {
		t.Fatalf("the inspection changed the project: %v", err)
	}
	if again := inspect(t, set.GoNode, Options{}); again.Revision != evidence.Revision {
		t.Fatal("the same files gave a different revision")
	}
	if err := os.WriteFile(filepath.Join(set.GoNode, "README.md"), []byte("# Widget\n\nNow it counts gadgets.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed := inspect(t, set.GoNode, Options{}); changed.Revision == evidence.Revision || changed.Purpose.Text != "Now it counts gadgets." {
		t.Fatalf("a changed README kept its revision or purpose: %+v", changed.Purpose)
	}
}

// Found by reviewing the Ori checkout itself: its README opens with a quoted,
// emphasized line, it declares two dozen commands, and it has more workflow
// files than are read.
func TestInspect_ARealRepositorysShapeReadsCleanly(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"README.md": "# Ori\n\n![badge](x.svg)\n\n> **Your AI headquarters, on your machine.** Workspaces work the missions.\n\nMore below.\n",
		"go.mod":    "module x\n",
		"Makefile":  "test-unit:\n\tgo test\n\nbuild-all:\n\tgo build\n\nlint:\n\tgolangci-lint run\n\ntest:\n\tgo test ./...\n\nbuild:\n\tgo build ./...\n",
	}
	for i := range 6 {
		files[".github/workflows/w"+string(rune('a'+i))+".yml"] = "jobs:\n  x:\n    steps:\n      - run: make test\n"
	}
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	evidence := inspect(t, dir, Options{})
	if evidence.Purpose == nil || evidence.Purpose.Text != "Your AI headquarters, on your machine. Workspaces work the missions." {
		t.Fatalf("purpose = %+v", evidence.Purpose)
	}
	// The plainly named commands lead; their variants and what a manifest only
	// implies follow.
	var order []string
	for _, command := range evidence.Commands {
		if command.Kind != "other" {
			order = append(order, command.Command)
		}
	}
	want := "make build|make test|make lint|make build-all|make test-unit|go build ./...|go test ./..."
	if got := strings.Join(order, "|"); got != want {
		t.Fatalf("command order = %q, want %q", got, want)
	}
	// Four workflows are read. The rest are reported as that, and the project's
	// own README and manifests are not called a partial look because of them.
	workflows := 0
	for _, doc := range evidence.Documents {
		if doc.Class == ClassWorkflow {
			workflows++
		}
	}
	if workflows != 4 || evidence.Partial || !hasGap(evidence, GapMoreWorkflows, ".github/workflows") {
		t.Fatalf("workflows read = %d partial = %t gaps = %+v", workflows, evidence.Partial, evidence.Gaps)
	}
}

func TestInspect_GitLayoutsAndLineageWithoutRunningGit(t *testing.T) {
	set := fixtures(t)
	main := inspect(t, set.GoNode, Options{}).Git
	if main.Layout != GitRepository || main.Branch != "main" || main.Remote != "example.invalid/acme/widget.git" || main.RepositoryKey == "" {
		t.Fatalf("ordinary checkout = %+v", main)
	}
	worktree := inspect(t, set.Worktree, Options{}).Git
	if worktree.Layout != GitWorktree || worktree.Branch != "feature/login" || worktree.Remote != main.Remote {
		t.Fatalf("linked worktree = %+v", worktree)
	}
	sibling := inspect(t, set.SiblingWorktree, Options{}).Git
	if sibling.Layout != GitWorktree || sibling.Branch != "feature/billing" {
		t.Fatalf("sibling worktree = %+v", sibling)
	}
	// Worktrees of one repository share its key; a separate clone does not,
	// even though it names the same remote. The key relates checkouts; it never
	// says two folders are the same one.
	if worktree.RepositoryKey != main.RepositoryKey || sibling.RepositoryKey != main.RepositoryKey {
		t.Fatalf("worktrees do not share the repository key: %q %q %q", main.RepositoryKey, worktree.RepositoryKey, sibling.RepositoryKey)
	}
	clone := inspect(t, set.Clone, Options{}).Git
	if clone.RepositoryKey == main.RepositoryKey || clone.Remote != main.Remote {
		t.Fatalf("clone = %+v, want another repository directory with the same remote", clone)
	}
	if none := inspect(t, set.NonGit, Options{}).Git; none.Layout != GitNone || none.Remote != "" || none.RepositoryKey != "" {
		t.Fatalf("no git = %+v", none)
	}
}

// A .git file is a pointer anyone can write. It is followed only when the
// directory it names points back; otherwise nothing beyond it is read.
func TestInspect_AGitPointerThatDoesNotPointBackIsNotFollowed(t *testing.T) {
	set := fixtures(t)
	elsewhere := filepath.Join(set.Root, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"HEAD":      "ref: refs/heads/secret-branch\n",
		"config":    "[remote \"origin\"]\n\turl = https://example.invalid/private/elsewhere.git\n",
		"commondir": ".\n",
		"gitdir":    filepath.Join(set.Outside, ".git") + "\n", // points at some other folder
	} {
		if err := os.WriteFile(filepath.Join(elsewhere, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	project := filepath.Join(set.Root, "pointed")
	if err := os.MkdirAll(project, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: "+elsewhere+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evidence := inspect(t, project, Options{})
	if evidence.Git.Layout != GitUnverified || evidence.Git.Branch != "" || evidence.Git.Remote != "" || evidence.Git.RepositoryKey != "" {
		t.Fatalf("an unverified pointer was followed: %+v", evidence.Git)
	}
	if !hasGap(evidence, GapGitUnverified, ".git") {
		t.Fatal("the unverified layout was not reported")
	}
	if all := everything(evidence); strings.Contains(all, "secret-branch") || strings.Contains(all, "elsewhere.git") {
		t.Fatal("metadata behind an unverified pointer leaked")
	}

	// Malformed pointers and a linked .git are not followed either.
	for name, write := range map[string]func(string) error{
		"empty pointer": func(dir string) error { return os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir:\n"), 0o600) },
		"not a pointer": func(dir string) error { return os.WriteFile(filepath.Join(dir, ".git"), []byte("hello\n"), 0o600) },
		"missing target": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nonexistent/place\n"), 0o600)
		},
		"a link": func(dir string) error {
			return os.Symlink(filepath.Join(set.GoNode, ".git"), filepath.Join(dir, ".git"))
		},
	} {
		dir := filepath.Join(set.Root, strings.ReplaceAll(name, " ", "-"))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := write(dir); err != nil {
			t.Fatal(err)
		}
		if git := inspect(t, dir, Options{}).Git; git.Layout != GitUnverified || git.Remote != "" || git.Branch != "" {
			t.Errorf("%s = %+v", name, git)
		}
	}
}

func TestInspect_HostileTextIsDataAndCredentialsAreWithheld(t *testing.T) {
	set := fixtures(t)
	evidence := inspect(t, set.Hostile, Options{})
	all := everything(evidence)
	if strings.Contains(all, softwarefixture.CredentialShapedValue()) {
		t.Fatal("a credential-shaped value survived the inspection")
	}
	readme, ok := evidence.Document("README.md")
	if !ok || readme.Withheld != 1 || !strings.Contains(readme.Text, WithheldLine) || !hasGap(evidence, GapWithheld, "README.md") {
		t.Fatalf("readme = %+v", readme)
	}
	// The injected line is kept as the text it is. Nothing in the evidence is an
	// instruction: there is no field that carries one.
	if !strings.Contains(readme.Text, softwarefixture.InjectedInstruction) {
		t.Fatal("the README's own text was altered beyond withholding credentials")
	}
	if evidence.Purpose == nil || evidence.Purpose.Text != "A package used to test careful reading." {
		t.Fatalf("purpose = %+v", evidence.Purpose)
	}
	// The remote keeps its host and path and loses its user and token.
	if evidence.Git.Remote != "example.invalid/acme/hostile.git" {
		t.Fatalf("remote = %q", evidence.Git.Remote)
	}
	if _, read := evidence.Document(".env"); read || strings.Contains(all, "HOSTILE_FIXTURE_TOKEN") {
		t.Fatal(".env was read")
	}
	// A deploy script is not a build, test, lint or run command, and is not
	// offered as one.
	if got := strings.Join(commands(evidence, true), "|"); got != "npm run test" {
		t.Fatalf("declared commands = %q", got)
	}
}

func TestInspect_LinksUnreadableAndBinaryFilesAreSkippedAndReported(t *testing.T) {
	set := fixtures(t)

	escaped := inspect(t, set.SymlinkEscape, Options{})
	if strings.Contains(everything(escaped), "OUTSIDE-FIXTURE") {
		t.Fatal("a link out of the project was followed")
	}
	for _, name := range []string{"README.md", "AGENTS.md"} {
		if !hasGap(escaped, GapSkippedLink, name) {
			t.Errorf("the skipped link %s was not reported", name)
		}
	}
	if !hasGap(escaped, GapNoReadme, "") || !hasGap(escaped, GapNoInstructions, "") || strings.Join(paths(escaped), "|") != "go.mod" {
		t.Fatalf("documents = %v gaps = %+v", paths(escaped), escaped.Gaps)
	}

	if os.Getuid() != 0 {
		locked := inspect(t, set.Unreadable, Options{})
		if !hasGap(locked, GapUnreadable, "README.md") || strings.Join(paths(locked), "|") != "go.mod" {
			t.Fatalf("unreadable: documents = %v gaps = %+v", paths(locked), locked.Gaps)
		}
	}

	binary := filepath.Join(set.Root, "binary")
	if err := os.MkdirAll(binary, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binary, "README"), []byte("PK\x03\x04\x00\x00binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if evidence := inspect(t, binary, Options{}); !hasGap(evidence, GapSkippedBinary, "README") || len(evidence.Documents) != 0 {
		t.Fatalf("binary: %+v", evidence)
	}

	// A workflows directory that is a link is not entered.
	linked := filepath.Join(set.Root, "linked-workflows")
	if err := os.MkdirAll(filepath.Join(linked, ".github"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(set.GoNode, ".github", "workflows"), filepath.Join(linked, ".github", "workflows")); err != nil {
		t.Fatal(err)
	}
	if evidence := inspect(t, linked, Options{}); len(evidence.Documents) != 0 {
		t.Fatalf("a linked workflows directory was read: %v", paths(evidence))
	}
}

func TestInspect_LimitsStopEarlyAndSaySo(t *testing.T) {
	set := fixtures(t)

	few := inspect(t, set.GoNode, Options{MaxFiles: 2})
	if !few.Partial || few.PartialReason != PartialFiles || strings.Join(paths(few), "|") != "AGENTS.md|README.md" {
		t.Fatalf("file limit: partial=%t reason=%q documents=%v", few.Partial, few.PartialReason, paths(few))
	}

	short := inspect(t, set.GoNode, Options{MaxFileBytes: 10})
	readme, _ := short.Document("README.md")
	if !readme.Truncated || readme.Bytes != 10 || !hasGap(short, GapTruncated, "README.md") {
		t.Fatalf("per-file limit: %+v", readme)
	}

	small := inspect(t, set.GoNode, Options{MaxTotalBytes: 200})
	total := 0
	for _, doc := range small.Documents {
		total += doc.Bytes
	}
	if !small.Partial || small.PartialReason != PartialBytes || total > 200 {
		t.Fatalf("total limit: partial=%t reason=%q bytes=%d", small.Partial, small.PartialReason, total)
	}

	calls := 0
	base := time.Now()
	slow := inspect(t, set.GoNode, Options{Budget: time.Second, Now: func() time.Time {
		calls++
		return base.Add(time.Duration(calls) * 600 * time.Millisecond)
	}})
	if !slow.Partial || slow.PartialReason != PartialTime || len(slow.Documents) >= 6 {
		t.Fatalf("time budget: partial=%t reason=%q documents=%v", slow.Partial, slow.PartialReason, paths(slow))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled, err := Inspect(ctx, set.GoNode, Options{})
	if err != nil || !cancelled.Partial || cancelled.PartialReason != PartialTime || len(cancelled.Documents) != 0 {
		t.Fatalf("cancelled: %+v %v", cancelled, err)
	}
}

func TestInspect_GapsForAProjectThatSaysLittle(t *testing.T) {
	set := fixtures(t)
	bare := inspect(t, set.NonGit, Options{})
	for _, code := range []string{GapNoReadme, GapNoInstructions} {
		if !hasGap(bare, code, "") {
			t.Errorf("missing gap %s", code)
		}
	}
	if got := strings.Join(commands(bare, true), "|"); got != "npm run start" || bare.Commands[0].Kind != "run" {
		t.Fatalf("commands = %+v", bare.Commands)
	}

	// Two instruction files may disagree; the report says there are two.
	two := filepath.Join(set.Root, "two-guides")
	for name, text := range map[string]string{"AGENTS.md": "Use tabs.\n", "CLAUDE.md": "Use spaces.\n", "go.mod": "module x\n"} {
		if err := os.MkdirAll(two, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(two, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	guides := inspect(t, two, Options{})
	if !hasGap(guides, GapSeveralGuides, "AGENTS.md, CLAUDE.md") || len(guides.Conventions) != 2 {
		t.Fatalf("two guides: %+v", guides.Gaps)
	}
	// A manifest alone implies commands; it declares none.
	if !hasGap(guides, GapNoCommands, "") || len(commands(guides, false)) != 2 {
		t.Fatalf("inferred-only commands: %+v gaps=%+v", guides.Commands, guides.Gaps)
	}

	// A package.json that is not JSON is reported, not guessed at.
	broken := filepath.Join(set.Root, "broken")
	if err := os.MkdirAll(broken, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "package.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if evidence := inspect(t, broken, Options{}); !hasGap(evidence, GapUnsupportedFile, "package.json") {
		t.Fatalf("broken manifest: %+v", evidence.Gaps)
	}
}

// A tool's configuration file is evidence that it is configured for the
// project. The file is not opened, and nothing claims the tool works.
func TestInspect_ToolConfigurationIsNotedWithoutBeingRead(t *testing.T) {
	set := fixtures(t)
	if got := inspect(t, set.GoNode, Options{}).Tools; len(got) != 0 {
		t.Fatalf("tools without configuration: %+v", got)
	}
	config := filepath.Join(set.GoNode, ".herdr", "devflow.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("primary = \"claude\"\ntoken = \"never-read-this-either\"\n"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(config, 0o600) })
	evidence := inspect(t, set.GoNode, Options{})
	if len(evidence.Tools) != 1 || evidence.Tools[0].Name != "Herdr devflow" || evidence.Tools[0].Source != ".herdr/devflow.toml" {
		t.Fatalf("tools = %+v", evidence.Tools)
	}
	if strings.Contains(everything(evidence), "never-read-this-either") {
		t.Fatal("the tool configuration was read")
	}
}

func TestInspect_RefusesARootThatIsNotARealDirectory(t *testing.T) {
	set := fixtures(t)
	link := filepath.Join(set.Root, "link-to-project")
	if err := os.Symlink(set.GoNode, link); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{
		"a link": link, "a file": filepath.Join(set.GoNode, "go.mod"),
		"missing": filepath.Join(set.Root, "nope"), "relative": "widget", "empty": "",
	} {
		if _, err := Inspect(context.Background(), root, Options{}); !errors.Is(err, ErrNotDirectory) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSanitizeRemote(t *testing.T) {
	for raw, want := range map[string]string{
		"https://example.invalid/acme/widget.git":                    "example.invalid/acme/widget.git",
		"https://deploy:s3cret@example.invalid/acme/widget.git":      "example.invalid/acme/widget.git",
		"https://token@Example.INVALID:8443/acme/widget.git?x=1#top": "example.invalid/acme/widget.git",
		"ssh://git@example.invalid/acme/widget.git":                  "example.invalid/acme/widget.git",
		"git@example.invalid:acme/widget.git":                        "example.invalid/acme/widget.git",
		"example.invalid:acme/widget":                                "example.invalid/acme/widget",
		"/Users/someone/repos/widget.git":                            "",
		"../widget.git":                                              "",
		"file:///Users/someone/repos/widget.git":                     "",
		"https://example.invalid":                                    "",
		"https://example.invalid/acme/wid get.git":                   "",
		"":             "",
		"not a remote": "",
	} {
		if got := SanitizeRemote(raw); got != want {
			t.Errorf("SanitizeRemote(%q) = %q, want %q", raw, got, want)
		}
		if got := SanitizeRemote(raw); strings.ContainsAny(got, "@?# ") {
			t.Errorf("SanitizeRemote(%q) = %q carries a user, query or fragment", raw, got)
		}
	}
}
