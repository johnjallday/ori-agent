package workspace

import "testing"

// library-setup-staffing: a reservation carries its time, which Settle uses as
// proof; it is cleared when the agent is recorded or the reservation released.
func TestProjectStaffingConsentsReservationLifecycle(t *testing.T) {
	store := NewInMemoryStore()
	homeID := consentHome(t, store)
	consents := NewProjectStaffingConsents(store)
	if _, err := consents.Grant(homeID, reaperGrant()); err != nil {
		t.Fatal(err)
	}
	reserve := func(name string) {
		t.Helper()
		if err := consents.ReserveAgent(homeID, "ori-reaper", "reaper-song", consentDigest, "reaper-assistant", name); err != nil {
			t.Fatal(err)
		}
	}
	read := func() ProjectStaffingConsentRole {
		t.Helper()
		consent, err := consents.Read(homeID)
		if err != nil || consent == nil {
			t.Fatalf("read = %v %v", consent, err)
		}
		return consent.Roles[0]
	}
	reserve("REAPER Assistant")
	first := read()
	if first.PendingName != "REAPER Assistant" || first.ReservedAt == nil {
		t.Fatalf("reserved = %+v", first)
	}
	// Reserving the same name again keeps the original time.
	version := mustVersion(t, store, homeID)
	reserve("REAPER Assistant")
	if again := read(); !again.ReservedAt.Equal(*first.ReservedAt) || mustVersion(t, store, homeID) != version {
		t.Fatalf("a repeated reservation rewrote it: %+v", again)
	}
	// A release names the reservation it gives up; another name changes nothing.
	if err := consents.ReleaseReservation(homeID, "reaper-assistant", "Someone Else"); err != nil {
		t.Fatal(err)
	}
	if role := read(); role.PendingName != "REAPER Assistant" {
		t.Fatalf("an unrelated release changed it: %+v", role)
	}
	if err := consents.ReleaseReservation(homeID, "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	if role := read(); role.PendingName != "" || role.ReservedAt != nil {
		t.Fatalf("released = %+v", role)
	}
	reserve("REAPER Assistant 2")
	if err := consents.RecordAgent(homeID, "ori-reaper", "reaper-song", consentDigest, "reaper-assistant", "REAPER Assistant 2"); err != nil {
		t.Fatal(err)
	}
	if role := read(); role.AgentName != "REAPER Assistant 2" || role.PendingName != "" || role.ReservedAt != nil {
		t.Fatalf("recorded = %+v", role)
	}
	// A recorded agent is never released.
	if err := consents.ReleaseReservation(homeID, "reaper-assistant", "REAPER Assistant 2"); err != nil {
		t.Fatal(err)
	}
	if role := read(); role.AgentName != "REAPER Assistant 2" {
		t.Fatalf("a recorded agent was released: %+v", role)
	}
	// The clone is deep: changing a copy's time does not reach the store.
	consent, _ := consents.Read(homeID)
	clone := consent.Clone()
	if clone.Roles[0].ReservedAt != nil {
		t.Fatal("unexpected reservation")
	}
}
