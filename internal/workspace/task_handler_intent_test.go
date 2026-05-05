package workspace

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/toolapi"
)

func TestIsLikelyBrowserAutomationIntent_DoesNotMatchFilesystemStepPromptWithFileExtensions(t *testing.T) {
	description := `Complete internal execution step 2 of 7 for this task.

Overall task: Gather DNM related files into DNM folder

Current step: Inspect candidate directories
Step detail: Look through likely source folders for relevant material.
Step type: Discovery

Completed step results so far:
- Check allowed filesystem scope: Step 1 complete. Files found include DNM Publishing Agreement - Stereotype.pages, DNM Publishing Agreement - Stereotype.pdf, and Papparazi - DNM Publishing Agreement.pdf.
`

	if isLikelyBrowserAutomationIntent(description) {
		t.Fatalf("expected filesystem step prompt not to be classified as browser automation")
	}
}

func TestIsLikelyBrowserAutomationIntent_MatchesBrowserTask(t *testing.T) {
	description := "Open instagram.com, click the profile tab, and extract the current follower count."
	if !isLikelyBrowserAutomationIntent(description) {
		t.Fatalf("expected browser task to be classified as browser automation")
	}
}

func TestTaskBrowserIntentDescription_UsesOverallTaskDescriptionForStructuredStep(t *testing.T) {
	task := Task{
		Description: "Complete internal execution step 2 of 7 for this task.\nCurrent step: Inspect candidate directories\nStep type: Discovery",
		Context: map[string]interface{}{
			"execution_overall_task_description": "Gather DNM related files into DNM folder",
		},
	}

	got := taskBrowserIntentDescription(task)
	if got != "Gather DNM related files into DNM folder" {
		t.Fatalf("expected overall task description, got %q", got)
	}
	if taskRequiresBrowserAutomation(task) {
		t.Fatalf("expected structured filesystem step not to require browser automation")
	}
}

func TestTaskRequiresBrowserAutomation_UsesOverallDescriptionForStructuredBrowserTask(t *testing.T) {
	task := Task{
		Description: "Complete internal execution step 2 of 6 for this task.\nCurrent step: Open the target page\nStep type: Action",
		Context: map[string]interface{}{
			"execution_overall_task_description": "Open instagram.com and extract the follower count.",
		},
	}

	if !taskRequiresBrowserAutomation(task) {
		t.Fatalf("expected structured browser step to require browser automation")
	}
}

func TestAgentSupportsBrowserAutomation_DoesNotTreatRawFetchAsBrowserAutomation(t *testing.T) {
	h := &LLMTaskHandler{}
	ag := &resolvedTaskAgent{
		Agent:      &agent.Agent{},
		MCPServers: []string{"fetch"},
	}

	if h.agentSupportsBrowserAutomation(ag) {
		t.Fatalf("expected raw fetch MCP not to be considered browser automation for URL tasks")
	}
}

func TestAgentSupportsBrowserAutomation_RecognizesUtilityWebTools(t *testing.T) {
	h := &LLMTaskHandler{}
	h.SetUtilityToolProvider(taskUtilityProviderStub{tools: map[string]toolapi.Tool{
		"web_search": taskHandlerToolStub{name: "web_search"},
	}})
	ag := &resolvedTaskAgent{Agent: &agent.Agent{}}

	if !h.agentSupportsBrowserAutomation(ag) {
		t.Fatalf("expected native web_search utility to satisfy browser/web capability")
	}
}
