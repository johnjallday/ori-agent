package folderdigest

import (
	"strings"
	"testing"
)

func TestProjectSummaryCopyRequiresItsSupportedFileMarker(t *testing.T) {
	for _, name := range []string{"Session.rpp", "nested/SESSION.RPP", strings.Repeat("long-", 60) + ".rpp"} {
		shape, title, guidance, ok := ProjectSummaryForFile(name)
		if !ok || shape != ShapeAudio || title != "Summarize the REAPER session: tracks, overview and one next step" || !strings.Contains(guidance, "does not establish edit history") {
			t.Fatalf("copy for %q = %q %q %q %t", name, shape, title, guidance, ok)
		}
	}
	for _, name := range []string{"take.wav", "Session.logicx", "README.md", "", "rpp-not-a-marker.txt"} {
		if _, _, _, ok := ProjectSummaryForFile(name); ok {
			t.Fatalf("invented application-specific summary for %q", name)
		}
	}
}
