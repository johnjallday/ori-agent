package web

import (
	"strings"
	"testing"
)

func TestCanvasAddDisclosureKeepsNodeActionsSeparateFromHiring(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("workspace-canvas", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`<details id="canvas-add-disclosure"`,
		`aria-controls="canvas-add-options">Add`,
		`role="group" aria-label="Add to canvas"`,
		`onclick="showAddCanvasAgentModal()"`,
		`onclick="showAddAttachmentModal()"`,
		`onclick="showAddTaskModal(this)"`,
		`onclick="showAddStoreNodeModal()"`,
		`onclick="showAddDirectoryModal()"`,
		`async function showAddCanvasAgentModal()`,
		`src="/js/modules/workspace-canvas-controls.js"`,
		`onclick="hideAgentDetails(this)" aria-label="Close agent details"`,
		`onclick="hideAttachmentDetails(this)" aria-label="Close attachment details"`,
		`onclick="hideTaskDetails(this)" aria-label="Close ticket details"`,
		`onclick="executeCurrentTask()" style="display: none;"`,
		`onclick="editCurrentTask()"`,
		`onclick="deleteCurrentTask()"`,
		`onclick="unassignCurrentTask()" style="display: none;"`,
		`tabindex="0" role="region" aria-label="Canvas timeline"`,
	} {
		if !strings.Contains(html, fragment) {
			t.Errorf("missing Canvas node action/disclosure contract %q", fragment)
		}
	}
	if strings.Contains(html, `onclick="showAddAgentModal()" title="Add Agent"`) {
		t.Error("Canvas Add Agent must not dispatch the shared hiring modal")
	}
}
