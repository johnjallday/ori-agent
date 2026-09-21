package sessionhttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestNewTemplateProvenance_IntakeSnapshotSurvivesLaterTemplateEdit(t *testing.T) {
	template := projecttemplates.Template{
		ID: "course",
		IntakeRequirements: []projecttemplates.IntakeRequirement{{
			Key: "materials", Label: "Original materials", Skill: "syllabus",
			Sources: workspace.IntakeSources{Files: true}, AcceptedExtensions: []string{".pdf"}, ProposalKinds: []string{"ticket"},
		}},
		BundledSkills: []projecttemplates.BundledSkill{{Name: "syllabus", Description: "Read it", Digest: "digest", Text: "exact text"}},
	}
	created := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Course"})
	created.SetTemplateProvenance(newTemplateProvenance(template, nil))

	// The source template is edited after creation. The workspace must continue
	// to use the declaration it reviewed and snapshotted at creation time.
	template.IntakeRequirements[0].Label = "Edited later"
	template.IntakeRequirements[0].AcceptedExtensions[0] = ".exe"
	template.BundledSkills[0].Text = "changed text"

	got, ok := created.TemplateIntakeRequirement("materials")
	if !ok || got.Label != "Original materials" || len(got.AcceptedExtensions) != 1 || got.AcceptedExtensions[0] != ".pdf" {
		t.Fatalf("later template edit changed workspace intake snapshot: %+v, %v", got, ok)
	}
	if provenance := created.GetTemplateProvenance(); len(provenance.BundledSkills) != 1 || provenance.BundledSkills[0].Text != "exact text" {
		t.Fatalf("later template edit changed bundled skill snapshot: %+v", provenance.BundledSkills)
	}
}
