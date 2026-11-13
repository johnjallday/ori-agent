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

// assistant-reads-projects: a reservation carries a random ID, the proof a hire
// with no project stamps on the agent it creates. It lives exactly as long as
// the pending name.
func TestProjectStaffingConsentsReservationIDLifecycle(t *testing.T) {
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
	if len(first.ReservationID) != 32 {
		t.Fatalf("reservation id = %q", first.ReservationID)
	}
	// Reserving the same name again keeps the ID: a retry stamps the same proof.
	reserve("REAPER Assistant")
	if again := read(); again.ReservationID != first.ReservationID {
		t.Fatalf("a repeated reservation changed its id: %q then %q", first.ReservationID, again.ReservationID)
	}
	// Another name is another reservation.
	reserve("REAPER Assistant 2")
	second := read()
	if second.ReservationID == "" || second.ReservationID == first.ReservationID {
		t.Fatalf("a new reservation kept the old id: %+v", second)
	}
	if err := consents.ReleaseReservation(homeID, "reaper-assistant", "REAPER Assistant 2"); err != nil {
		t.Fatal(err)
	}
	if role := read(); role.ReservationID != "" || role.PendingName != "" {
		t.Fatalf("released = %+v", role)
	}
	reserve("REAPER Assistant")
	if err := consents.RecordAgent(homeID, "ori-reaper", "reaper-song", consentDigest, "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	if role := read(); role.AgentName != "REAPER Assistant" || role.ReservationID != "" {
		t.Fatalf("recorded = %+v", role)
	}
	if _, err := consents.ForgetAgent(homeID, "ori-reaper", "reaper-song", "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	if role := read(); role.AgentName != "" || role.ReservationID != "" {
		t.Fatalf("forgotten = %+v", role)
	}

	// A reservation written before the ID existed gains one when it is reserved
	// again under the same name, and keeps its time.
	reserve("REAPER Assistant")
	before := read()
	if err := store.Update(homeID, func(home *Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectStaffingConsent.Roles[0].ReservationID = ""
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reserve("REAPER Assistant")
	if healed := read(); healed.ReservationID == "" || !healed.ReservedAt.Equal(*before.ReservedAt) {
		t.Fatalf("an older reservation did not gain an id, or lost its time: %+v", healed)
	}

	// An ID with no pending name is not a record the store accepts.
	consent, _ := consents.Read(homeID)
	consent.Roles[0].PendingName, consent.Roles[0].ReservedAt = "", nil
	if err := consent.Validate(); err == nil {
		t.Fatal("a reservation id without a pending name passed validation")
	}
}
