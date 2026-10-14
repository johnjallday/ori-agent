package projectlibrary

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestHomeArrangementContext_ReadOnlyDisclosurePinsRuntimeBeforeSharing(t *testing.T) {
	f, authority, _ := homeArrangementFixture(t)
	before := f.doc(t)
	calls, resolves := 0, 0
	fingerprint := strings.Repeat("a", 64)
	provider := homePlanningProvider{chat: func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		calls++
		return &llm.ChatResponse{Content: "Which projects belong together?"}, nil
	}}
	resolve := func(_ context.Context, actual ManagerAuthority) (ArrangementRuntime, error) {
		resolves++
		if actual != authority {
			t.Fatal("foreign runtime", actual)
		}
		return ArrangementRuntime{Provider: provider, Model: "fixture", ProviderFingerprint: fingerprint, Revalidate: func(context.Context) error { return nil }}, nil
	}
	if _, err := f.library.ReadArrangementPlanningContext(t.Context(), "foreign", f.scope.HomeID, resolve); err == nil || resolves != 0 {
		t.Fatal("foreign owner reached runtime", err)
	}
	view, err := f.library.ReadArrangementPlanningContext(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, resolve)
	if err != nil || view.AgentName != authority.AgentName || view.Model != "fixture" || view.Provider != "openai" || len(view.ContextPin) != 64 || view.Revision != before.Revision || view.SchemaVersion != before.SchemaVersion {
		t.Fatal(view, err)
	}
	if calls != 0 || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("disclosure invoked model or wrote catalog")
	}
	repeat, err := f.library.ReadArrangementPlanningContext(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, resolve)
	if err != nil || repeat != view {
		t.Fatal("unchanged disclosure unstable", err)
	}
	in := ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Group client projects", ExpectedRevision: view.Revision, ExpectedContext: view.ContextPin, ShareCatalogMetadata: true, AllowUpgrade: true}
	fingerprint = strings.Repeat("b", 64)
	if _, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, in); !errors.Is(err, workspacearrangement.ErrStale) || calls != 0 {
		t.Fatal("changed runtime received metadata", err, calls)
	}
	fresh, err := f.library.ReadArrangementPlanningContext(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, resolve)
	if err != nil || fresh.ContextPin == view.ContextPin {
		t.Fatal("runtime pin did not change", err)
	}
	in.ExpectedContext = fresh.ContextPin
	saved, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, in)
	if err != nil || saved.History == nil || calls != 1 {
		t.Fatal("fresh pinned plan failed", err, calls)
	}
	if _, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, in); !errors.Is(err, ErrConflict) || calls != 1 {
		t.Fatal("lost-response retry reached model", err, calls)
	}
}
