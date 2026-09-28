package workspacecontinuity

import (
	"errors"
	"testing"
)

// Imported record IDs reach URLs, attributes and inline handlers across the
// existing UI, so anything beyond Ori's own ID shapes is refused at the claim.
func TestRestoredRecordIDsCannotCarryMarkup(t *testing.T) {
	for _, id := range []string{"chat-1", "3f2b9c1e-8d4a-4f7e-9a1b-2c3d4e5f6a7b", "call_abc123", "toolu_01ABC", "rev-2026-08-31", "a.b"} {
		if !SafeRecordID(id) {
			t.Errorf("%q should be accepted", id)
		}
	}
	for _, id := range []string{"a');fetch('/x');('", `x" onmouseover="y`, "<b>", "a b", "-leading", ".hidden", "", "a;b", "a&b"} {
		if SafeRecordID(id) {
			t.Errorf("%q should be refused", id)
		}
	}
	db, _ := localFixture(t)
	tx, err := db.BeginTx(t.Context(), nil)
	must(t, err)
	defer func() { _ = tx.Rollback() }()
	scope := RestoreScope{OperationID: "op-1", WorkspaceID: "ws-1", UserID: "local"}
	if _, err := ClaimRecord(t.Context(), tx, scope, "sessions", "sessions", "a');alert(1);('", Digest([]byte("x"))); !errors.Is(err, ErrInvalid) {
		t.Fatal("an unsafe imported record ID was claimed", err)
	}
}
