package projecttemplates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewTemplate_LoadsIntakeRequirements(t *testing.T) {
	tpl := loadTemplateWithManifest(t, `{
		"name":"Course",
		"tools":{"skills":["syllabus-intake"]},
		"directory_requirements":[{"key":"course-root","label":"Course folder"}],
		"intake_requirements":[{
			"key":" Course-Materials ",
			"label":" Course materials ",
			"skill":"syllabus-intake",
			"sources":{"files":true,"url":true,"directory_key":" Course-Root "},
			"accepted_extensions":["PDF",".md"],
			"proposal_kinds":["Ticket","memory"]
		}]
	}`)

	if tpl.IntakeRequirementsError != "" || tpl.HasInvalidIntakeRequirements() {
		t.Fatalf("valid intake reported an error: %q", tpl.IntakeRequirementsError)
	}
	if len(tpl.IntakeRequirements) != 1 {
		t.Fatalf("intake requirements = %+v", tpl.IntakeRequirements)
	}
	requirement := tpl.IntakeRequirements[0]
	if requirement.Key != "course-materials" || requirement.Label != "Course materials" || requirement.Sources.DirectoryKey != "course-root" {
		t.Fatalf("intake was not normalized: %+v", requirement)
	}
}

func TestNewTemplate_InvalidIntakeRequirementsFailClosed(t *testing.T) {
	tests := []struct {
		name     string
		block    string
		contains string
	}{
		{
			name:     "undeclared skill",
			block:    `[{"key":"course","label":"Course","skill":"missing","sources":{"files":true},"proposal_kinds":["ticket"]}]`,
			contains: "tools.skills",
		},
		{
			name:     "behavior field",
			block:    `[{"key":"course","label":"Course","skill":"syllabus-intake","sources":{"files":true},"proposal_kinds":["ticket"],"url":"https://example.test"}]`,
			contains: "unknown field",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tpl := loadTemplateWithManifest(t, `{
				"name":"Course",
				"tools":{"skills":["syllabus-intake"]},
				"intake_requirements":`+test.block+`
			}`)
			if len(tpl.IntakeRequirements) != 0 || !tpl.HasInvalidIntakeRequirements() {
				t.Fatalf("invalid intake did not fail closed: %+v / %q", tpl.IntakeRequirements, tpl.IntakeRequirementsError)
			}
			if !strings.Contains(tpl.IntakeRequirementsError, test.contains) {
				t.Fatalf("error %q does not mention %q", tpl.IntakeRequirementsError, test.contains)
			}
			foundWarning := false
			for _, warning := range tpl.Warnings {
				foundWarning = foundWarning || strings.Contains(warning, "intake_requirements") && strings.Contains(warning, "blocks workspace creation")
			}
			if !foundWarning {
				t.Fatalf("warnings do not report blocked creation: %v", tpl.Warnings)
			}
		})
	}
}

func TestNewTemplate_IntakeWizardStepResolvesAgainstIntakeRequirements(t *testing.T) {
	tpl := loadTemplateWithManifest(t, `{
		"name":"Course",
		"tools":{"skills":["syllabus-intake"]},
		"intake_requirements":[{"key":"course","label":"Course","skill":"syllabus-intake","sources":{"files":true},"proposal_kinds":["ticket"]}],
		"setup_wizard":{"version":1,"title":"Set up course","steps":[{"id":"materials","kind":"intake","requirement_key":"course","required":true}]}
	}`)
	if tpl.SetupWizardError != "" || tpl.SetupWizard == nil || len(tpl.SetupWizard.Steps) != 1 {
		t.Fatalf("intake wizard did not resolve: %+v / %q", tpl.SetupWizard, tpl.SetupWizardError)
	}
}

func TestNewTemplate_IntakeStepCannotChooseItsAdapter(t *testing.T) {
	tpl := loadTemplateWithManifest(t, `{
		"name":"Course",
		"tools":{"skills":["syllabus-intake"]},
		"intake_requirements":[{"key":"course","label":"Course","skill":"syllabus-intake","sources":{"files":true},"proposal_kinds":["ticket"]}],
		"setup_wizard":{"version":1,"title":"Set up course","steps":[{"id":"materials","kind":"intake","requirement_key":"course","adapter":"blueprint_intake","required":true}]}
	}`)
	if !tpl.HasInvalidSetupWizard() || !strings.Contains(tpl.SetupWizardError, "kind is served by compiled adapter") {
		t.Fatalf("manifest-selected intake adapter was not rejected: %q", tpl.SetupWizardError)
	}
}

func TestNewTemplate_RejectsDirectoryAndIntakeStepsForSameFolder(t *testing.T) {
	tpl := loadTemplateWithManifest(t, `{
		"name":"Course",
		"tools":{"skills":["syllabus-intake"]},
		"directory_requirements":[{"key":"course-root","label":"Course folder"}],
		"intake_requirements":[{"key":"course","label":"Course","skill":"syllabus-intake","sources":{"directory_key":"course-root"},"proposal_kinds":["ticket"]}],
		"setup_wizard":{"version":1,"title":"Set up course","steps":[
			{"id":"folder","kind":"directory","requirement_key":"course-root","required":true},
			{"id":"materials","kind":"intake","requirement_key":"course","required":true}
		]}
	}`)
	if !tpl.HasInvalidSetupWizard() || !strings.Contains(tpl.SetupWizardError, "both directory step") {
		t.Fatalf("shared directory was not rejected: %q", tpl.SetupWizardError)
	}
}

func TestIntakeEligibleFixture_LoadsAndInstantiates(t *testing.T) {
	template, err := LoadFolder(filepath.Join("testdata", "intake-eligible"))
	if err != nil {
		t.Fatal(err)
	}
	if template.HasInvalidIntakeRequirements() || template.HasInvalidSetupWizard() || len(template.IntakeRequirements) != 1 {
		t.Fatalf("fixture is not intake eligible: %+v", template)
	}
	workspaceFolder := t.TempDir()
	result, err := InstantiateTemplate(template, workspaceFolder, "My Course")
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectPath != "my-course" {
		t.Fatalf("ProjectPath = %q", result.ProjectPath)
	}
	if _, err := os.Stat(filepath.Join(workspaceFolder, result.ProjectPath, "course-notes.md")); err != nil {
		t.Fatalf("fixture skeleton was not instantiated: %v", err)
	}
}

func TestNewTemplate_WithoutIntakeRequirementsIsUnchanged(t *testing.T) {
	tpl := loadTemplateWithManifest(t, `{"name":"Plain"}`)
	if tpl.IntakeRequirements != nil || tpl.IntakeRequirementsError != "" || tpl.HasInvalidIntakeRequirements() {
		t.Fatalf("plain template gained intake state: %+v", tpl)
	}
}
