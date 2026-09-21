package blueprintintake

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestConsentStatementNamesCloudProviderOrLocalHandling(t *testing.T) {
	cloud := ConsentStatement(ModelProvider{Name: "OpenAI"})
	if !strings.Contains(cloud, "sent to OpenAI") || !strings.Contains(cloud, "Nothing is created until you review it") {
		t.Fatalf("cloud statement = %q", cloud)
	}
	local := ConsentStatement(ModelProvider{Name: "Ollama", Local: true})
	if !strings.Contains(local, "stays on this computer") || !strings.Contains(local, "Ollama") {
		t.Fatalf("local statement = %q", local)
	}
}

func TestConsentIsStoredForActorAndCurrentProvider(t *testing.T) {
	service, _, ws := newSourceTestService(t, []string{".txt"})
	provider := ModelProvider{Name: "openai"}
	service.SetProviderResolver(func(context.Context, string) (ModelProvider, error) { return provider, nil })

	accepted, err := service.AcceptConsent(context.Background(), ws.ID, "materials", "user-7")
	if err != nil {
		t.Fatal(err)
	}
	if !accepted.Accepted || accepted.AcceptedBy != "user-7" || accepted.AcceptedAt == nil || accepted.Provider != "openai" {
		t.Fatalf("accepted consent = %+v", accepted)
	}
	status, err := service.ConsentStatus(context.Background(), ws.ID, "materials")
	if err != nil || !status.Accepted || status.AcceptedBy != "user-7" {
		t.Fatalf("stored consent = %+v, %v", status, err)
	}

	provider = ModelProvider{Name: "ollama", Local: true}
	status, err = service.ConsentStatus(context.Background(), ws.ID, "materials")
	if err != nil || status.Accepted || !strings.Contains(status.Statement, "stays on this computer") {
		t.Fatalf("provider change reused consent: %+v, %v", status, err)
	}
}

func TestIntakeSetupCannotCompleteWithoutSourcesAndConsent(t *testing.T) {
	service, _, ws := newSourceTestService(t, []string{".txt"})
	service.SetProviderResolver(func(context.Context, string) (ModelProvider, error) {
		return ModelProvider{Name: "openai"}, nil
	})
	requirement, _ := ws.TemplateIntakeRequirement("materials")
	req := setupwizard.StepRequest{WorkspaceID: ws.ID, Intake: &requirement, Step: workspace.SetupWizardStep{Kind: workspace.SetupStepKindIntake}}

	readiness, err := service.EvaluateSetup(context.Background(), req)
	if err != nil || readiness.Ready || readiness.ErrorCategory != setupwizard.ErrorCategoryNotConfigured {
		t.Fatalf("empty readiness = %+v, %v", readiness, err)
	}
	if _, err := service.AddFile(context.Background(), ws.ID, "materials", "notes.txt", bytes.NewBufferString("hello")); err != nil {
		t.Fatal(err)
	}
	readiness, err = service.EvaluateSetup(context.Background(), req)
	if err != nil || readiness.Ready || readiness.ErrorCategory != setupwizard.ErrorCategoryPermissionRequired {
		t.Fatalf("unconsented readiness = %+v, %v", readiness, err)
	}
	if _, err := service.ConfirmSetup(context.Background(), req, setupwizard.StepAction{Type: setupwizard.ActionConfirm}); err == nil {
		t.Fatal("ConfirmSetup completed without consent")
	}
	if _, err := service.AcceptConsent(context.Background(), ws.ID, "materials", "local-user"); err != nil {
		t.Fatal(err)
	}
	readiness, err = service.ConfirmSetup(context.Background(), req, setupwizard.StepAction{Type: setupwizard.ActionConfirm})
	if err != nil || !readiness.Ready {
		t.Fatalf("consented readiness = %+v, %v", readiness, err)
	}
}
