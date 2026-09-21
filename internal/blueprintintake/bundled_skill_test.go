package blueprintintake

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const bundledSkillText = "---\nname: syllabus-intake\ndescription: Read a syllabus\n---\n\nReturn dated items only."

func newBundledRunner(t *testing.T, existingText string) (*IntakeRunner, *skills.Manager, *workspace.Workspace) {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Course"})
	ws.ID = "course"
	ws.SharedData = map[string]any{"entry_agent_name": "Manager"}
	ws.AgentInstances = []workspace.AgentInstance{{ID: "manager", Name: "Manager", EntryPoint: true}}
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		IntakeRequirements: []workspace.IntakeRequirement{{Key: "materials", Skill: "syllabus-intake", Sources: workspace.IntakeSources{Files: true}, ProposalKinds: []string{"ticket"}}},
		BundledSkills:      []workspace.BundledSkillSnapshot{{Name: "syllabus-intake", Description: "Read a syllabus", Text: bundledSkillText}},
	})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(t.TempDir(), "Manager")
	manager := skills.NewManager(skills.ManagerConfig{AgentStorePath: filepath.Join(t.TempDir(), "agents.json")})
	manager.SetAgentFolderResolver(func(string) (string, bool) { return agentDir, true })
	if existingText != "" {
		if err := manager.InstallBundledSkill("Manager", "syllabus-intake", existingText, false); err != nil {
			t.Fatal(err)
		}
	}
	sources := NewSourceService(store, store)
	return NewIntakeRunner(store, sources, manager, cannedExecutor{}), manager, ws
}

func TestBundledSkillTrustEnablesReviewedExactText(t *testing.T) {
	runner, _, ws := newBundledRunner(t, bundledSkillText)
	readiness, err := runner.SkillReadiness(ws.ID, "materials")
	if err != nil || readiness.Missing != "not trusted" {
		t.Fatalf("readiness = %+v, %v", readiness, err)
	}
	review, exists, err := runner.BundledSkillReview(ws.ID, "materials")
	if err != nil || !exists || review.BundledText != bundledSkillText || review.Collision {
		t.Fatalf("review = %+v, %v", review, err)
	}
	review, err = runner.TrustBundledSkill(ws.ID, "materials", "")
	if err != nil || !review.Ready {
		t.Fatalf("trusted review = %+v, %v", review, err)
	}
	readiness, err = runner.SkillReadiness(ws.ID, "materials")
	if err != nil || !readiness.Ready {
		t.Fatalf("ready = %+v, %v", readiness, err)
	}
}

func TestBundledSkillCollisionRequiresExplicitChoice(t *testing.T) {
	existing := "---\nname: syllabus-intake\ndescription: Existing\n---\n\nExisting instructions."
	runner, manager, ws := newBundledRunner(t, existing)
	if readiness, err := runner.SkillReadiness(ws.ID, "materials"); err != nil || readiness.Missing != "skill conflict" {
		t.Fatalf("collision readiness = %+v, %v", readiness, err)
	}
	if _, err := runner.TrustBundledSkill(ws.ID, "materials", ""); err == nil {
		t.Fatal("collision was accepted without a choice")
	}
	review, err := runner.TrustBundledSkill(ws.ID, "materials", "existing")
	if err != nil || !review.Ready || review.Decision != "existing" {
		t.Fatalf("existing choice = %+v, %v", review, err)
	}
	text, _, err := manager.GetSkillMarkdown("Manager", "syllabus-intake")
	if err != nil || text != existing {
		t.Fatalf("existing skill changed: %q, %v", text, err)
	}

	replacementRunner, replacementManager, replacementWS := newBundledRunner(t, existing)
	replacement, err := replacementRunner.TrustBundledSkill(replacementWS.ID, "materials", "bundled")
	if err != nil || !replacement.Ready {
		t.Fatalf("bundled choice = %+v, %v", replacement, err)
	}
	text, _, err = replacementManager.GetSkillMarkdown("Manager", "syllabus-intake")
	if err != nil || text != bundledSkillText {
		t.Fatalf("bundled skill was not installed: %q, %v", text, err)
	}
}
