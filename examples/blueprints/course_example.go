// Package blueprints packages the portable Course example for explicit use
// from Documents. It does not register it as a built-in or library starter.
package blueprints

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

//go:embed course/template.json course/skills/syllabus-intake/SKILL.md
var example embed.FS

// StageCourse materializes only the file-intake steps in a private temporary
// directory. The ordinary template loader validates them and snapshots the
// inert skill. No placeholder link, integration or automation is copied.
func StageCourse(temp string) (string, error) {
	var m struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Tools       struct {
			Skills []string `json:"skills"`
		} `json:"tools"`
		Intake []workspace.IntakeRequirement `json:"intake_requirements"`
		Wizard workspace.SetupWizard         `json:"setup_wizard"`
	}
	raw, err := example.ReadFile("course/template.json")
	if err != nil {
		return "", err
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	for i := range m.Intake {
		m.Intake[i].Sources = workspace.IntakeSources{Files: true, ReviewFiles: true}
	}
	m.Wizard.Steps = slices.DeleteFunc(m.Wizard.Steps, func(s workspace.SetupWizardStep) bool { return s.Kind != "intake" && s.Kind != "intake_review" })
	raw, err = json.Marshal(m)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(temp, "course")
	dir := filepath.Join(dest, "skills", "syllabus-intake")
	if err = os.MkdirAll(dir, 0750); err != nil {
		return "", err
	}
	skill, err := example.ReadFile("course/skills/syllabus-intake/SKILL.md")
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(dir, "SKILL.md"), skill, 0600); err != nil {
		return "", err
	}
	return dest, os.WriteFile(filepath.Join(dest, "template.json"), raw, 0600)
}
