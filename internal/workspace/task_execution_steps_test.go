package workspace

import "testing"

func TestIsReadOnlyFilesystemListingIntent_MatchesFolderListingRequest(t *testing.T) {
	description := "Give me list of files in DNM folder"
	if !IsReadOnlyFilesystemListingIntent(description) {
		t.Fatalf("expected %q to be classified as a read-only filesystem listing request", description)
	}
	if IsLikelyFilesystemExecutionIntent(description) {
		t.Fatalf("expected %q not to be classified as a filesystem mutation workflow", description)
	}
}

func TestIsReadOnlyFilesystemListingIntent_DoesNotMatchMutationWorkflow(t *testing.T) {
	description := "Gather DNM related files into DNM folder"
	if IsReadOnlyFilesystemListingIntent(description) {
		t.Fatalf("expected %q not to be classified as a read-only filesystem listing request", description)
	}
	if !IsLikelyFilesystemExecutionIntent(description) {
		t.Fatalf("expected %q to remain classified as a filesystem mutation workflow", description)
	}
}

func TestInferTaskExecutionSteps_DoesNotUseStructuredPlanForReadOnlyFilesystemRequest(t *testing.T) {
	steps := InferTaskExecutionSteps(Task{Description: "Give me list of files in DNM folder"})
	if len(steps) != 0 {
		t.Fatalf("expected no structured listing steps, got %d", len(steps))
	}
}
