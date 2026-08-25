package workspacesurfacehttp

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func testConfirmationBinding() confirmationBinding {
	return confirmationBinding{
		UserID: "user-a", WorkspaceID: "workspace-a", PluginID: "plugin-a", Generation: 7,
		CapabilityID: "tools", CallerID: "plugin:plugin-a:tools:main", OperationID: "setting.validate",
	}
}

func TestConfirmationCanonicalPayloadIsSingleUseBeforeServiceDispatch(t *testing.T) {
	store := newConfirmationStore()
	binding := testConfirmationBinding()
	id, err := store.issue(binding, json.RawMessage(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.approve(id, binding)
	if err != nil {
		t.Fatal(err)
	}
	// Canonical JSON makes object key order irrelevant while retaining exact
	// normalized values.
	if err := store.consume(token, binding, json.RawMessage(`{"a":1,"b":2}`)); err != nil {
		t.Fatalf("consume error = %v", err)
	}
	// Consumption happens before service dispatch. Even if the native process
	// crashes now, the same approval cannot be replayed.
	if err := store.consume(token, binding, json.RawMessage(`{"a":1,"b":2}`)); !errors.Is(err, errConfirmationInvalid) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestConfirmationRejectsCrossCallerChangedPayloadAndExpiry(t *testing.T) {
	store := newConfirmationStore()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	binding := testConfirmationBinding()
	id, err := store.issue(binding, json.RawMessage(`{"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	other := binding
	other.WorkspaceID = "workspace-b"
	if _, err := store.approve(id, other); !errors.Is(err, errConfirmationInvalid) {
		t.Fatalf("cross-workspace approval error = %v", err)
	}
	token, err := store.approve(id, binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.consume(token, binding, json.RawMessage(`{"enabled":false}`)); !errors.Is(err, errConfirmationInvalid) {
		t.Fatalf("changed payload error = %v", err)
	}
	now = now.Add(confirmationTTL + time.Nanosecond)
	if err := store.consume(token, binding, json.RawMessage(`{"enabled":true}`)); !errors.Is(err, errConfirmationInvalid) {
		t.Fatalf("expired token error = %v", err)
	}
}
