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

func TestInferTaskExecutionSteps_UsesListingPlanForReadOnlyFilesystemRequest(t *testing.T) {
	steps := InferTaskExecutionSteps(Task{Description: "Give me list of files in DNM folder"})
	if len(steps) != 3 {
		t.Fatalf("expected 3 listing steps, got %d", len(steps))
	}

	if got := steps[0].Title; got != "Check allowed filesystem scope" {
		t.Fatalf("unexpected step 1 title %q", got)
	}
	if got := steps[1].Title; got != "Inspect the target directory" {
		t.Fatalf("unexpected step 2 title %q", got)
	}
	if got := steps[2].Title; got != "Return the file list" {
		t.Fatalf("unexpected final step title %q", got)
	}
	if got := steps[2].Detail; got != "Return the concrete file list, or explain clearly if the folder is missing or empty." {
		t.Fatalf("unexpected final step detail %q", got)
	}
}
