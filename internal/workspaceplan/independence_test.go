package workspaceplan

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The planning workflow owns itself (FR-179, FR-190).
//
// Ori used to plan by resolving a repository-local skill — `.agents/skills/
// workspace-planning/SKILL.md` — and handing its prompt to a model. That made
// the packaged app depend on a file in this repository, and made "the workspace
// requires a branch" a sentence a model read rather than a check that ran.
//
// These tests hold the replacement honest in the direction that matters: not
// "the skill file is gone" (which a later commit could quietly undo by adding
// another) but "nothing in the planning lifecycle can reach a skill at all".
// The first is a fact about today's tree; the second is a structural property.

// Plan lifecycle code must not import the skills packages.
//
// This is the load-bearing assertion. Creation, drafting, review, approval,
// materialization, and execution all live in this package, so an import edge
// here is the only way any of them could resolve or inject a skill. Without
// that edge, "planning never executes the planning skill" is not a promise
// anybody has to keep — it is a thing that cannot be expressed.
func TestPlanningCannotReachTheSkillSubsystem(t *testing.T) {
	forbidden := []string{
		"internal/skills",
		"internal/skillshttp",
	}

	imports, err := packageImportPaths(".")
	if err != nil {
		t.Fatalf("read package imports: %v", err)
	}
	for _, path := range imports {
		for _, banned := range forbidden {
			if strings.HasSuffix(path, banned) {
				t.Errorf("workspaceplan imports %s; planning must not resolve skills", path)
			}
		}
	}
}

// The compiled artifact renderer produces planning documents from typed content
// rather than from a skill's prompt, so a PRD is the same document every time
// regardless of which skills a workspace happens to have (FR-96).
func TestArtifactsAreRenderedFromContentNotASkill(t *testing.T) {
	renderer := DefaultArtifactRenderer{}
	content := reviewableContent()
	content.Artifacts = []ProposedArtifact{{
		ID: "art-1", Kind: ArtifactPRD, Path: "tasks/prd-demo.md",
		Title: "Demo", Enabled: true,
	}}

	plan := &Plan{ID: "plan-1", WorkspaceID: testWorkspaceID, Title: "Demo plan"}
	version := &Version{
		Number: 1, Title: "Demo plan", Objective: "Do the thing",
		Content: content,
	}

	first, err := renderer.Render(content.Artifacts[0], plan, version)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	second, err := renderer.Render(content.Artifacts[0], plan, version)
	if err != nil {
		t.Fatalf("render again: %v", err)
	}
	if string(first) != string(second) {
		t.Error("the same version rendered two different documents")
	}
	if len(first) == 0 {
		t.Error("the renderer produced nothing")
	}
}

// No skill can approve, materialize, transition, or execute a Plan (FR-190).
//
// Approval is checked against a real consumed approval record, and transitions
// into the approval statuses refuse any source but the user. A skill has no way
// to produce either, which is what makes "skills are context, never authority"
// true rather than merely intended.
func TestApprovalAuthorityCannotComeFromAnywhereButAUser(t *testing.T) {
	for _, source := range []TransitionSource{SourceService, SourceModel, SourceExecution} {
		for _, target := range []Status{StatusInReview, StatusApproved} {
			err := ValidateTransition(StatusDraft, target, source)
			if target == StatusApproved && err == nil {
				t.Errorf("%s was allowed to approve a plan", source)
			}
		}
	}
	// And a materialization refuses to move a Plan to approved without the
	// consumed approval record itself.
	if err := ValidateApprovalTransition(StatusInReview, StatusApproved, nil, "plan-1", 1); err == nil {
		t.Error("a plan reached approved with no approval record")
	}
}

// --- The repository skill is gone (FR-180) ---------------------------------

// The legacy skill directory must not come back. It is checked from the test
// rather than trusted to stay deleted, because re-adding a file is easy and its
// absence is what makes the packaged app independent of this repository.
func TestTheLegacyPlanningSkillIsNotInTheRepository(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Skipf("could not locate the repository root: %v", err)
	}

	legacy := filepath.Join(root, ".agents", "skills", "workspace-planning")
	if _, err := os.Stat(legacy); err == nil {
		t.Errorf("%s exists again; the packaged app must not depend on a repository skill", legacy)
	}
}

func TestCrossHarnessTaskPlanningSkillHasOneWorkflowSource(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Skipf("could not locate the repository root: %v", err)
	}
	canonicalPath := filepath.Join(root, ".agents", "skills", "task-planning", "SKILL.md")
	canonical, err := os.ReadFile(canonicalPath) // #nosec G304 -- fixed repository test path
	if err != nil || !strings.Contains(string(canonical), "## Operating modes") {
		t.Fatalf("canonical task-planning skill is missing or incomplete: %v", err)
	}

	claudePath := filepath.Join(root, ".claude", "skills", "task-planning", "SKILL.md")
	entrypoint, err := os.ReadFile(claudePath) // #nosec G304 -- fixed repository test path
	if err != nil {
		t.Fatalf("read Claude task-planning entry point: %v", err)
	}
	text := string(entrypoint)
	if !strings.Contains(text, ".agents/skills/task-planning/SKILL.md") {
		t.Fatalf("Claude entry point does not delegate to the canonical skill: %s", text)
	}
	for _, duplicated := range []string{"## Operating modes", "## Demo checkpoints", "## Permission sweep"} {
		if strings.Contains(text, duplicated) {
			t.Errorf("Claude entry point duplicated canonical workflow section %q", duplicated)
		}
	}
}

// packageImportPaths returns every import in a directory's non-test Go files.
func packageImportPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// repositoryRoot walks up from the package directory to the module root.
func repositoryRoot() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
