package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
)

type observedHQSchedule struct {
	*fakeHireBriefs
	enabledWrites []bool
}

func (s *observedHQSchedule) UpdateConfig(ctx context.Context, config dailybrief.Config) (*dailybrief.Config, error) {
	s.enabledWrites = append(s.enabledWrites, config.ScheduleEnabled)
	return s.fakeHireBriefs.UpdateConfig(ctx, config)
}

func TestHQSetup_DisabledScheduleNeverBecomesEnabledAndReplayCannotChangeIt(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	observed := &observedHQSchedule{fakeHireBriefs: fixture.briefs}
	fixture.coordinator.briefs = observed
	request := fixture.request()
	disabled := false
	request.ScheduleEnabled = &disabled
	result, err := fixture.coordinator.Setup(ctx, "local", request)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Status != StatusActive || result.BriefConfig.ScheduleEnabled {
		t.Fatalf("unexpected activation: %#v", result)
	}
	replay, err := fixture.coordinator.Setup(ctx, "local", request)
	if err != nil || replay.BriefConfig.ScheduleEnabled {
		t.Fatalf("replay changed schedule: %#v %v", replay, err)
	}
	enabled := true
	request.ScheduleEnabled = &enabled
	if _, err = fixture.coordinator.Setup(ctx, "local", request); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed replay must conflict: %v", err)
	}
	if len(observed.enabledWrites) != 1 || observed.enabledWrites[0] {
		t.Fatalf("schedule writes = %v", observed.enabledWrites)
	}
	if fixture.creator.calls != 1 || fixture.hq.designateCalls != 1 {
		t.Fatal("replay created another HQ")
	}
}

func TestHQSetup_ResumePreservesDisabledScheduleFromDurableClaim(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	observed := &observedHQSchedule{fakeHireBriefs: fixture.briefs}
	fixture.coordinator.briefs = observed
	fixture.creator.err = errors.New("injected creation interruption")
	request := fixture.request()
	enabled := false
	request.ScheduleEnabled = &enabled
	if _, err := fixture.coordinator.Setup(ctx, "local", request); err == nil {
		t.Fatal("expected interruption")
	}
	state, err := store.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := decodeStoredHQRequest(state)
	if err != nil || !saved.ScheduleDisabled {
		t.Fatalf("disabled choice not journalled: %#v %v", saved, err)
	}
	tampered := state.Clone()
	tampered.HQPayloadJSON = strings.Replace(state.HQPayloadJSON, `"schedule_disabled":true`, `"schedule_disabled":false`, 1)
	if _, err := decodeStoredHQRequest(tampered); err == nil {
		t.Fatal("changed schedule consent passed the stored hash check")
	}
	// Changing the caller's pointer after claiming cannot rewrite the consent.
	enabled = true
	fixture.creator.err = nil
	result, err := fixture.coordinator.Resume(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Status != StatusActive || result.BriefConfig.ScheduleEnabled {
		t.Fatalf("resume enabled schedule: %#v", result)
	}
	if len(observed.enabledWrites) != 1 || observed.enabledWrites[0] {
		t.Fatalf("schedule writes = %v", observed.enabledWrites)
	}
}

func TestHQSetup_DefaultSchedulePreservesLegacyPayloadBytes(t *testing.T) {
	normalized, err := normalizeHQSetupRequest(validHQSetupRequest())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	const legacy = `{"version":1,"request_id":"hq-request-1","hq_name":"Command Post","timezone":"America/New_York","schedule_days":["mon","wed","fri"],"schedule_time":"07:30","scope":"all","selected_workspace_ids":null,"include_future_workspaces":false,"notify_on_ready":true}`
	if string(payload) != legacy || normalized.Hash != PayloadHash([]byte(legacy)) {
		t.Fatalf("legacy request bytes/hash changed: %s", payload)
	}
	request := validHQSetupRequest()
	enabled := true
	request.ScheduleEnabled = &enabled
	explicit, err := normalizeHQSetupRequest(request)
	if err != nil || explicit.Hash != normalized.Hash {
		t.Fatalf("enabled/default should normalize equally: %v", err)
	}
	ctx := context.Background()
	store, _ := newTestStore(t)
	fixture := newHQFixture(t, store)
	result, err := fixture.coordinator.Setup(ctx, "local", fixture.request())
	if err != nil || !result.BriefConfig.ScheduleEnabled {
		t.Fatalf("legacy enabled default changed: %#v %v", result, err)
	}
}
