package templateonboarding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	sessionmodel "github.com/johnjallday/ori-agent/internal/session"
)

func TestServiceResolveAndStartStatusFromEntryAgent(t *testing.T) {
	for _, tc := range []struct {
		name string
		ws   *sessionmodel.Workspace
		want Status
	}{
		{
			name: "no entry agent",
			ws:   &sessionmodel.Workspace{ID: "ws-1", Name: "Song"},
			want: StatusPendingEntryAgent,
		},
		{
			name: "explicit entry agent",
			ws: &sessionmodel.Workspace{
				ID:         "ws-1",
				Name:       "Song",
				SharedData: map[string]any{entryAgentNameKey: "Producer"},
				AgentInstances: []sessionmodel.AgentInstance{
					{Name: "Producer", EntryPoint: true},
				},
			},
			want: StatusCollecting,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := NewService(NewStore(testResolver{"ws-1": t.TempDir()}))
			summary, handled, err := service.ResolveAndStart(context.Background(), tc.ws, onboardingProjectTemplate())
			if err != nil {
				t.Fatalf("ResolveAndStart: %v", err)
			}
			if !handled {
				t.Fatal("expected onboarding to be handled")
			}
			if summary.Status != tc.want {
				t.Fatalf("summary status=%q, want %q", summary.Status, tc.want)
			}
		})
	}
}

func TestServiceInvalidSpecIsNotHandled(t *testing.T) {
	service := NewService(NewStore(testResolver{"ws-1": t.TempDir()}))
	tpl := projecttemplates.Template{
		ID:         "bad",
		Onboarding: json.RawMessage(`{"version":"2","completion":{"type":"none"}}`),
	}
	summary, handled, err := service.ResolveAndStart(context.Background(), &sessionmodel.Workspace{ID: "ws-1"}, tpl)
	if err != nil {
		t.Fatalf("ResolveAndStart: %v", err)
	}
	if handled || summary != nil {
		t.Fatalf("invalid spec should not be handled, summary=%+v handled=%v", summary, handled)
	}
}

func onboardingProjectTemplate() projecttemplates.Template {
	return projecttemplates.Template{
		ID: "onboarding",
		Onboarding: json.RawMessage(`{
			"version":"1",
			"fields":[{"id":"song_name","label":"Song name","type":"string","required":true}],
			"completion":{"type":"none","instantiate_skeleton":true}
		}`),
	}
}
