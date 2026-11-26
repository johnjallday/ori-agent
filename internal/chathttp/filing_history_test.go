package chathttp

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type historyProbe struct{ owner, id, query string }

func (p *historyProbe) LookupFilingHistory(_ context.Context, owner, id, q string) (filejanitor.FilingHistoryLookup, error) {
	p.owner, p.id, p.query = owner, id, q
	return filejanitor.FilingHistoryLookup{}, nil
}
func TestFilingHistoryToolRequiresCurrentAssociatedInstance(t *testing.T) {
	p, store, ws := assessmentFixture(t)
	probe := &historyProbe{}
	p.filingHistory = probe
	p.SetUserProfileDeps(nil, userprofile.LocalUserProvider{})
	if p.canRecallFiling() {
		t.Fatal("lead without association got history")
	}
	ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), OwnedResources: []workspace.CapabilityResource{{Kind: workspace.ResourceCompanionAgent, ID: p.executingInstanceID}}}}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	if intake := maybeBuildWorkspacePlanningFormResponse(&resolvedChatAgent{Agent: &agent.Agent{}, WorkspaceTools: p}, "Where did my hotel receipt go?", normalizedChatRouteContext{WorkspaceID: ws.ID}, store, nil); intake != nil {
		t.Fatal("Filing lookup redirected to travel planning")
	}
	tool := p.filingHistoryTool()
	if _, err := tool.Call(t.Context(), `{"filename":"hotel receipt"}`); err != nil || probe.id != ws.ID || probe.owner != userprofile.LocalUserID || probe.query != "hotel receipt" {
		t.Fatal(probe, err)
	}
	if _, err := tool.Call(t.Context(), `{"filename":"hotel","workspace_id":"other"}`); err == nil {
		t.Fatal("accepted client scope")
	}
	p.SetExecutingInstanceID("")
	if _, err := tool.Call(t.Context(), `{"filename":"hotel"}`); err == nil {
		t.Fatal("namesake read history")
	}
}
