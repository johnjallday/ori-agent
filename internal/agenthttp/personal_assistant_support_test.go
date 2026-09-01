package agenthttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type supportReader struct {
	projection *personalassistant.Projection
}

func (r supportReader) Get(context.Context, string) (*personalassistant.Projection, error) {
	return r.projection, nil
}

func TestPersonalAssistantSupportPresentation_IsTruthfulAndPAFOnly(t *testing.T) {
	refs := []workspace.WorkspaceRef{{ID: "hq-1", Name: "Personal HQ"}}
	classifier := personalAssistantSupportClassifier{
		reader: supportReader{projection: &personalassistant.Projection{
			State: personalassistant.APIStateActive, AssistantID: "assistant-1", HQWorkspaceID: "hq-1",
		}},
		provider: userprofile.LocalUserProvider{},
	}
	if got := classifier.classify(context.Background(), "Journal", refs); got != "Assistant support" {
		t.Fatalf("PAF Journal presentation=%q", got)
	}
	if got := classifier.classify(context.Background(), "User Journal", refs); got != "" {
		t.Fatalf("unrelated user agent was reframed=%q", got)
	}
	if got := classifier.classify(context.Background(), "Journal", []workspace.WorkspaceRef{{ID: "other"}}); got != "" {
		t.Fatalf("foreign Journal was reframed=%q", got)
	}
}
