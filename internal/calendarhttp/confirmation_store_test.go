package calendarhttp

import (
	"sync"
	"testing"
	"time"
)

func TestConfirmationStore_CreateAndConsumeExactlyOnce(t *testing.T) {
	s := newConfirmationStore(time.Minute)
	c := s.create("u1", "w1", "b1", "create_event", "hash-1")

	got, err := s.consume(c.ID, "u1", "w1", "b1", "create_event", "hash-1")
	if err != nil {
		t.Fatalf("first consume should succeed: %v", err)
	}
	if got.ID != c.ID {
		t.Fatalf("consumed confirmation ID mismatch: %q vs %q", got.ID, c.ID)
	}

	if _, err := s.consume(c.ID, "u1", "w1", "b1", "create_event", "hash-1"); err == nil {
		t.Fatal("a second consume of the same confirmation must fail (replay)")
	}
}

func TestConfirmationStore_RejectsPayloadTamper(t *testing.T) {
	s := newConfirmationStore(time.Minute)
	c := s.create("u1", "w1", "b1", "create_event", "hash-original")

	if _, err := s.consume(c.ID, "u1", "w1", "b1", "create_event", "hash-DIFFERENT"); err == nil {
		t.Fatal("a changed payload hash must be rejected")
	}
	// The confirmation must still be usable with the correct hash afterward
	// (a failed tamper attempt must not itself consume the confirmation).
	if _, err := s.consume(c.ID, "u1", "w1", "b1", "create_event", "hash-original"); err != nil {
		t.Fatalf("the original, untampered payload should still consume successfully: %v", err)
	}
}

func TestConfirmationStore_RejectsWrongUser(t *testing.T) {
	s := newConfirmationStore(time.Minute)
	c := s.create("u1", "w1", "b1", "create_event", "hash-1")
	if _, err := s.consume(c.ID, "someone-else", "w1", "b1", "create_event", "hash-1"); err == nil {
		t.Fatal("a different user must not be able to consume the confirmation")
	}
}

func TestConfirmationStore_RejectsWrongWorkspace(t *testing.T) {
	s := newConfirmationStore(time.Minute)
	c := s.create("u1", "w1", "b1", "create_event", "hash-1")
	if _, err := s.consume(c.ID, "u1", "w2", "b1", "create_event", "hash-1"); err == nil {
		t.Fatal("a different workspace must not be able to consume the confirmation")
	}
}

func TestConfirmationStore_RejectsWrongBindingOrOperation(t *testing.T) {
	s := newConfirmationStore(time.Minute)
	c := s.create("u1", "w1", "b1", "create_event", "hash-1")
	if _, err := s.consume(c.ID, "u1", "w1", "b2", "create_event", "hash-1"); err == nil {
		t.Fatal("a different binding must be rejected")
	}
	if _, err := s.consume(c.ID, "u1", "w1", "b1", "update_event", "hash-1"); err == nil {
		t.Fatal("a different operation must be rejected")
	}
}

func TestConfirmationStore_RejectsUnknownID(t *testing.T) {
	s := newConfirmationStore(time.Minute)
	if _, err := s.consume("does-not-exist", "u1", "w1", "b1", "create_event", "hash-1"); err == nil {
		t.Fatal("an unknown confirmation id must be rejected")
	}
}

func TestConfirmationStore_ExpiresAfterTTL(t *testing.T) {
	s := newConfirmationStore(10 * time.Millisecond)
	c := s.create("u1", "w1", "b1", "create_event", "hash-1")

	time.Sleep(30 * time.Millisecond)
	if _, err := s.consume(c.ID, "u1", "w1", "b1", "create_event", "hash-1"); err == nil {
		t.Fatal("an expired confirmation must be rejected")
	}
}

func TestConfirmationStore_IDsAreUniqueAndNonEmpty(t *testing.T) {
	s := newConfirmationStore(time.Minute)
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		c := s.create("u1", "w1", "b1", "create_event", "hash")
		if c.ID == "" {
			t.Fatal("confirmation ID must not be empty")
		}
		if seen[c.ID] {
			t.Fatalf("confirmation ID collision: %q", c.ID)
		}
		seen[c.ID] = true
	}
}

// TestConfirmationStore_ConcurrentConsumeIsExactlyOnce is the direct test of
// FR31's "single-use" guarantee under concurrency: many goroutines race to
// consume the same confirmation id; exactly one must win.
func TestConfirmationStore_ConcurrentConsumeIsExactlyOnce(t *testing.T) {
	s := newConfirmationStore(time.Minute)
	c := s.create("u1", "w1", "b1", "create_event", "hash-1")

	const attempts = 25
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0

	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			if _, err := s.consume(c.ID, "u1", "w1", "b1", "create_event", "hash-1"); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful consume under concurrency, got %d", successes)
	}
}

func TestHashMutationPayload_DeterministicAndSensitiveToChange(t *testing.T) {
	p1 := normalizedMutationPayload{Operation: "create_event", CalendarID: "cal-1", Title: "Sync", StartTime: "2026-07-20T10:00:00Z", EndTime: "2026-07-20T11:00:00Z"}
	p2 := p1
	if hashMutationPayload(p1) != hashMutationPayload(p2) {
		t.Fatal("identical payloads must hash identically")
	}
	p2.Title = "Different Title"
	if hashMutationPayload(p1) == hashMutationPayload(p2) {
		t.Fatal("changing a field must change the hash")
	}
}
