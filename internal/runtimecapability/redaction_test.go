package runtimecapability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/logger"
)

var runtimeSecrets = []string{
	"/Users/realperson/Music/Secret Song.rpp",
	"localhost:8080",
	"token=super-secret",
	"command_id=12345",
	"realperson@example.com",
	"acct_987654321",
	"https://user:password@example.test/private",
}

func captureRuntimeEvents(t *testing.T) *[]logger.Fields {
	t.Helper()
	var events []logger.Fields
	original := emitRuntimeEvent
	emitRuntimeEvent = func(_ string, fields logger.Fields) {
		copy := logger.Fields{}
		for key, value := range fields {
			copy[key] = value
		}
		events = append(events, copy)
	}
	t.Cleanup(func() { emitRuntimeEvent = original })
	return &events
}

func TestRuntimeStatusPersistenceAndEventsRedactSensitiveAdapterData(t *testing.T) {
	secretText := strings.Join(runtimeSecrets, " ")
	adapter := &recordingAdapter{
		id:      "runtime_adapter",
		durable: DurableResult{State: DurableInProgress, ReasonCode: "runtime_missing", Summary: secretText, Action: &Action{Token: "repair", Code: "repair", Label: "Open /Users/realperson/private"}},
	}
	registry := NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	contract := contractWithRequirements("runtime")
	contract.OperatingModes[0].Description = runtimeSecrets[0]
	contract.Requirements[0].Adapter = adapter.ID()
	contract.Requirements[0].Description = runtimeSecrets[1]
	contract.Requirements[0].Disclosure = runtimeSecrets[2]
	store := &runtimeStore{ws: runtimeWorkspace(contract)}
	service := NewService(store, registry)

	status, err := service.Status(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	encodedStatus, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range runtimeSecrets {
		if strings.Contains(string(encodedStatus), secret) {
			t.Errorf("status leaked %q: %s", secret, encodedStatus)
		}
	}
	if len(status.Requirements) != 1 || status.Requirements[0].Summary != "" || status.Requirements[0].Action != nil {
		t.Fatalf("sensitive adapter projection was not dropped: %+v", status.Requirements)
	}

	encodedState, err := json.Marshal(store.ws.GetRuntimeState())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range runtimeSecrets {
		if strings.Contains(string(encodedState), secret) {
			t.Errorf("persisted state leaked %q: %s", secret, encodedState)
		}
	}
	if strings.Contains(string(encodedState), "summary") || strings.Contains(string(encodedState), "action") {
		t.Fatalf("persisted runtime state copied transient adapter output: %s", encodedState)
	}

	events := captureRuntimeEvents(t)
	adapter.durableErr = errors.New(secretText)
	if _, err := service.Status(context.Background(), store.ws.ID); err != nil {
		t.Fatal(err)
	}
	if len(*events) == 0 {
		t.Fatal("adapter failure emitted no structured event")
	}
	allowed := map[string]bool{eventFieldName: true, eventFieldAdapter: true, eventFieldCategory: true}
	for _, fields := range *events {
		for key, value := range fields {
			if !allowed[key] {
				t.Errorf("event carries non-allowlisted field %q", key)
			}
			rendered := fmt.Sprint(value)
			for _, secret := range runtimeSecrets {
				if strings.Contains(rendered, secret) {
					t.Errorf("event field %q leaked %q", key, secret)
				}
			}
		}
	}
}
