package http

import "testing"

func TestNotebookPreviewLogPathOmitsCaptureFilename(t *testing.T) {
	const prefix = "/api/personal-assistant/second-brain/preview/"
	if got := logPath(prefix + "inbox/private-first-line.md"); got != prefix+"[note]" {
		t.Fatal("preview filename in request log")
	}
	if got := logPath("/api/workspaces"); got != "/api/workspaces" {
		t.Fatal("unrelated route changed")
	}
}
