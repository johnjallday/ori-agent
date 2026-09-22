package projecttemplates

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeBundledSkill(t *testing.T, root, name, prompt string) {
	t.Helper()
	dir := filepath.Join(root, "skills", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("---\nname: %s\ndescription: Test skill\n---\n%s\n", name, prompt)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImportAndDuplicateCarryOnlyBundledSkillMarkdown(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, ManifestFileName), []byte(`{"name":"Course","tools":{"skills":["syllabus-intake"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeBundledSkill(t, source, "syllabus-intake", "Read the syllabus.")
	if err := os.WriteFile(filepath.Join(source, "skills", "syllabus-intake", "ignored.sh"), []byte("exit 1"), 0o700); err != nil {
		t.Fatal(err)
	}
	library := t.TempDir()
	imported, err := ImportFolder(library, source, "Course")
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.BundledSkills) != 1 || imported.BundledSkills[0].Name != "syllabus-intake" {
		t.Fatalf("bundled = %+v", imported.BundledSkills)
	}
	if _, err := os.Stat(filepath.Join(imported.Path, "skills", "syllabus-intake", "ignored.sh")); !os.IsNotExist(err) {
		t.Fatalf("non-SKILL.md content was imported: %v", err)
	}
	duplicate, err := Duplicate(library, imported.ID, "Course Copy")
	if err != nil || len(duplicate.BundledSkills) != 1 {
		t.Fatalf("duplicate = %+v, %v", duplicate.BundledSkills, err)
	}
}

func TestCourseExampleLoadsItsDeclaredBundledSkill(t *testing.T) {
	tpl := newTemplate(filepath.Join("..", "..", "examples", "blueprints", "course"))
	if tpl.HasInvalidBundledSkills() || len(tpl.BundledSkills) != 1 || tpl.BundledSkills[0].Name != "syllabus-intake" {
		t.Fatalf("course bundled skills = %+v / %q", tpl.BundledSkills, tpl.BundledSkillsError)
	}
	if tpl.InputsError != "" || tpl.Inputs == nil || len(tpl.Inputs.Fields) != 2 || tpl.Inputs.Fields[0].IntakeKey != "course-materials" {
		t.Fatalf("course inputs = %+v / %q", tpl.Inputs, tpl.InputsError)
	}
	if !tpl.HasSkeleton {
		t.Fatal("course example must include a skeleton")
	}
	if tpl.Builtin || IsBuiltinStarterID(tpl.ID) {
		t.Fatal("course example must remain import-only, not an embedded starter")
	}
}

func TestLeaseExampleUsesOnlyTheGenericIntakeMechanism(t *testing.T) {
	tpl := newTemplate(filepath.Join("..", "..", "examples", "blueprints", "lease"))
	if tpl.IntakeRequirementsError != "" || tpl.SetupWizardError != "" || tpl.HasInvalidBundledSkills() {
		t.Fatalf("lease example errors: intake=%q wizard=%q skill=%q", tpl.IntakeRequirementsError, tpl.SetupWizardError, tpl.BundledSkillsError)
	}
	if len(tpl.IntakeRequirements) != 1 || tpl.IntakeRequirements[0].Skill != "lease-intake" || len(tpl.BundledSkills) != 1 {
		t.Fatalf("lease example declarations = intake:%+v skills:%+v", tpl.IntakeRequirements, tpl.BundledSkills)
	}
}

func TestBundledSkillsRejectInvalidOrExcessiveDeclarations(t *testing.T) {
	root := t.TempDir()
	declared := ""
	for i := 0; i < 9; i++ {
		name := fmt.Sprintf("skill-%d", i)
		writeBundledSkill(t, root, name, "Prompt")
		if i > 0 {
			declared += ","
		}
		declared += fmt.Sprintf("%q", name)
	}
	manifest := fmt.Sprintf(`{"name":"Too many","tools":{"skills":[%s]}}`, declared)
	if err := os.WriteFile(filepath.Join(root, ManifestFileName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	tpl := newTemplate(root)
	if !tpl.HasInvalidBundledSkills() {
		t.Fatalf("nine bundled skills were accepted: %+v", tpl.BundledSkills)
	}
}
