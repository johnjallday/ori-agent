package projectconnection

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestSplitProjectPersistsResolvedReviewedHomeDeclaration(t *testing.T) {
	home := &workspace.AssistantProgramDeclaration{ID: "music-producer-assistant"}
	template := projecttemplates.Template{ID: "plugin:reaper-plugin:reaper-song", ResolvedAssistantHome: home}
	snapshot := &workspace.GroupRequirementSnapshot{SelectedComposition: workspace.GroupRequirementCompositionGrouped}
	provenance := templateProvenance(template, time.Now(), snapshot)
	if provenance.AssistantProgram == nil || provenance.AssistantProgram.ID != home.ID {
		t.Fatalf("split project loses canonical Home declaration: %#v", provenance)
	}
	home.ID = "changed"
	if provenance.AssistantProgram.ID != "music-producer-assistant" {
		t.Fatal("mutated persisted Home declaration")
	}
	standalone := templateProvenance(template, time.Now())
	if standalone.AssistantProgram != nil {
		t.Fatal("unreviewed Home declaration on standalone project")
	}
}
