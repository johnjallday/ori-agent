package sessionhttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type continuityTemplateStore struct{ workspace.Store }

func (s *continuityTemplateStore) CheckWorkspaceExecution(context.Context, string, bool) error {
	return workspace.ErrWorkspaceExecutionInactive
}

func TestContinuityTemplateFirstOpenDoesNotConsumeUnadmittedSetup(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeStarterTaskTemplate(t, handler.templatesRootResolver(), "starter-template", true)
	_, response := postCreateWorkspace(t, handler, `{"name":"Inactive template","template_id":"starter-template"}`)
	id := response["folder"].(map[string]any)["id"].(string)
	handler.workspaceTaskStore = &continuityTemplateStore{Store: handler.taskMutationStore()}
	handler.SetTemplateSetupTaskStarter(func(string, string) error { t.Fatal("inactive first open reached task starter"); return nil })
	result := postTemplateSetupStart(t, handler, id)
	if result["started"] != false || result["reason"] != "local_activation_required" {
		t.Fatal(result)
	}
	tasks := workspaceTasksFromStore(t, handler, id)
	if len(tasks) == 0 {
		t.Fatal("missing setup fixture")
	}
	for _, task := range tasks {
		if _, consumed := task.Context[taskContextSetupConsumedAt]; consumed {
			t.Fatal("inactive first open consumed portable setup intent")
		}
	}
}
