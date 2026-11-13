package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

// The folder agent names a stopped reading by the library's read-turn reason
// codes without importing them; the two lists must not drift apart, or a reason
// the library records would reach the card with no words.
func TestFolderAgentLearningReasonsAreTheLibrarysReadReasons(t *testing.T) {
	for library, agent := range map[string]string{
		projectlibrary.FactsReadNoModel:          folderagent.LearningReasonNoModel,
		projectlibrary.FactsReadAssistantMissing: folderagent.LearningReasonAssistantMissing,
		projectlibrary.FactsReadNoAssistant:      folderagent.LearningReasonNoAssistant,
		projectlibrary.FactsReadPluginUpdate:     folderagent.LearningReasonPluginUpdate,
		projectlibrary.FactsReadPluginFailed:     folderagent.LearningReasonPluginFailed,
		projectlibrary.FactsReadTimeLimit:        folderagent.LearningReasonTimeLimit,
		projectlibrary.FactsReadRestarted:        folderagent.LearningReasonRestarted,
		projectlibrary.FactsReadDetailsOff:       folderagent.LearningReasonDetailsOff,
		projectlibrary.FactsReadNoCall:           folderagent.LearningReasonNoCall,
		projectlibrary.FactsReadModelError:       folderagent.LearningReasonModelError,
	} {
		if library != agent {
			t.Errorf("library reason %q is %q on the folder agent", library, agent)
		}
	}
	// A read that ended stopped carries its reason into the reading.
	progress := &learnProgress{total: 6}
	progress.readEnded(projectlibrary.FactsReadRun{Status: "stopped", Reason: projectlibrary.FactsReadNoModel, AgentName: "App Assistant"})
	got := progress.final(true)
	if got.Status != folderagent.LearningStopped || got.Reason != folderagent.LearningReasonNoModel || got.Reader != "App Assistant" {
		t.Fatalf("final = %+v", got)
	}
	// A finished read leaves no reason.
	done := &learnProgress{total: 6}
	done.readEnded(projectlibrary.FactsReadRun{Status: "finished", AgentName: "App Assistant"})
	if got := done.final(true); got.Status != folderagent.LearningDone || got.Reason != "" || got.Reader != "App Assistant" {
		t.Fatalf("done = %+v", got)
	}
}
