package learningmissions

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStatusRedactsUnavailableSourceWithoutWritingOrReenrolling(t *testing.T) {
	s, _ := testService(t)
	offer := propose(t, s)
	before, _ := s.store.Owner(t.Context(), "local")
	s.source = sourceStub{missing: true}
	status, err := s.Status(t.Context(), "local")
	if err != nil || len(status.Instances) != 1 || status.Instances[0].Progress.Blocker != "source_unavailable" {
		t.Fatalf("projection: %+v %v", status, err)
	}
	visible := s.VisibleOutcome(t.Context(), "local", offer)
	data, err := json.Marshal(visible)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), offer.Instance.Definition.Goal) || visible.Instance.Definition.WorkspaceID != "" {
		t.Fatal("inaccessible text or link escaped")
	}
	after, _ := s.store.Owner(t.Context(), "local")
	if before.Revision != after.Revision || before.Epoch != after.Epoch {
		t.Fatal("status mutated owner")
	}
	saved, err := s.store.Instance(t.Context(), "local", offer.Instance.ID)
	if err != nil || saved.Definition.Goal != offer.Instance.Definition.Goal || saved.State != Offered {
		t.Fatal("projection changed immutable saved contract")
	}
}

func TestStatusReadinessDoesNotGenerateOrPersistBlockers(t *testing.T) {
	s, _ := testService(t)
	out := propose(t, s)
	s.now = func() time.Time { return time.UnixMilli(out.Instance.Definition.ExpiresAt + 1) }
	status, err := s.Status(t.Context(), "local")
	if err != nil || status.Instances[0].Progress.Blocker != "offer_expired" {
		t.Fatalf("expired offer: %+v %v", status, err)
	}
	saved, _ := s.store.Instance(t.Context(), "local", out.Instance.ID)
	if saved.Progress.Blocker != "" || saved.Revision != 1 {
		t.Fatal("GET changed saved state")
	}
}
