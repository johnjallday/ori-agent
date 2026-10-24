package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

type readinessModelReader struct{ provider, model string }

func (m *readinessModelReader) GetSystemModel() (string, string) { return m.provider, m.model }
func (m *readinessModelReader) GetSystemReasoningEffort() string { return "" }

func TestFolderSetupModelRereadsConfigurationWithoutReleaseCacheLease(t *testing.T) {
	f := newDraftServerFixture(t)
	reader := &readinessModelReader{}
	f.builder.sessionHandler.SetSystemModelReader(reader)
	f.builder.sessionHandler.SetProjectTemplateResolver(func(id, _ string) (projecttemplates.Template, error) {
		return projecttemplates.Template{ID: id, Name: "Fixture", Agents: []projecttemplates.AgentSpec{{Name: "Readiness agent"}}}, nil
	})
	host := &folderSetupHost{builder: f.builder}
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{
		Subject: personalassistant.FolderCandidateRecord{Name: "Session", Shape: "audio", MarkerName: "*.rpp"},
	}}
	target, err := host.target(req.Offer)
	if err != nil {
		t.Fatal(err)
	}
	target.provider = nil // isolate the local configuration read from Home placement
	host.cache = map[string]cachedPlanFacts{"local|" + target.entry.Key: {at: time.Now(), facts: foldersetup.PlanFacts{
		Integration: foldersetup.Plugin{State: personalassistant.FolderInstallReady, Name: "Fixture"},
	}}}
	facts, err := host.facts(t.Context(), req, target)
	if err != nil || facts.Model.State != "missing" || facts.Model.Settings != "system" {
		t.Fatalf("known absent model: %+v %v", facts.Model, err)
	}
	reader.provider, reader.model = "fixture", "first"
	facts, err = host.facts(t.Context(), req, target)
	if err != nil || facts.Model.State != "configured" || facts.Model.Name != "first" {
		t.Fatalf("configuration stayed cached: %+v %v", facts.Model, err)
	}
	req.Plan.Intent.Model = &personalassistant.FolderSetupModelIntent{Source: "system", Provider: "fixture", Model: "first"}
	if err := host.validateSetupModel(req); err != nil {
		t.Fatal(err)
	}
	reader.model = "changed"
	if err := host.validateSetupModel(req); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatal("changed accepted model was silently adopted", err)
	}
	if req.Plan.Intent.Model.Model != "first" {
		t.Fatal("accepted witness was refreshed")
	}
	_, _, unknown := host.pluginHire("local", target, foldersetup.Plugin{State: personalassistant.FolderInstallInstall})
	if unknown.State != "not_checked" || unknown.Settings != "" {
		t.Fatal("uninstalled blueprint inferred a missing model", unknown)
	}
}

func TestFolderSetupRecheckFencesAnInflightReadinessCacheWrite(t *testing.T) {
	(*folderSetupHost)(nil).InvalidateSetupReadiness("local")
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	host := &folderSetupHost{builder: &ServerBuilder{reviewedIntegrationReader: setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
		close(entered)
		<-release
		return setupjourney.CanonicalStepRead{}, errors.New("fixture unavailable")
	})}}
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{
		Subject: personalassistant.FolderCandidateRecord{Name: "Session", Shape: "audio", MarkerName: "*.rpp"},
	}}
	go func() { defer close(done); host.ReadSetupReadiness(t.Context(), req) }()
	<-entered
	host.InvalidateSetupReadiness("local")
	close(release)
	<-done
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.readiness) != 0 {
		t.Fatal("a pre-recheck response repopulated the cache")
	}
}
