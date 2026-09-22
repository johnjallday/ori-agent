package blueprintintake

import (
	"context"
	"strings"
	"testing"
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
