package connections

import (
	"errors"
	"testing"
	"time"
)

func TestStateStore_BeginConsumeSingleUse(t *testing.T) {
	s := NewStateStore(time.Minute)
	pa, err := s.Begin(BeginParams{LocalUserID: "u1", Product: ProductGmail, ReturnTo: "/x", CallbackURI: "cb", ActiveSubject: "sub-1"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if pa.State == "" || pa.Nonce == "" || pa.State == pa.Nonce {
		t.Fatalf("state/nonce should be non-empty and distinct: %+v", pa)
	}

	got, ok := s.Consume(pa.State)
	if !ok {
		t.Fatal("first Consume should succeed")
	}
	if got.LocalUserID != "u1" || got.Product != ProductGmail || got.ActiveSubject != "sub-1" || got.Nonce != pa.Nonce {
		t.Fatalf("Consume returned wrong pending: %+v", got)
	}
	if _, ok := s.Consume(pa.State); ok {
		t.Fatal("second Consume must fail (single-use)")
	}
}

func TestStateStore_UnknownAndBlank(t *testing.T) {
	s := NewStateStore(time.Minute)
	if _, ok := s.Consume("nope"); ok {
		t.Fatal("unknown state must not consume")
	}
	if _, ok := s.Consume("  "); ok {
		t.Fatal("blank state must not consume")
	}
}

func TestStateStore_Expiry(t *testing.T) {
	s := NewStateStore(time.Minute)
	base := time.Now()
	s.now = func() time.Time { return base }
	pa, err := s.Begin(BeginParams{Product: ProductDrive})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	s.now = func() time.Time { return base.Add(2 * time.Minute) }
	if _, ok := s.Consume(pa.State); ok {
		t.Fatal("expired state must not consume")
	}
}

func TestStateStore_Discard(t *testing.T) {
	s := NewStateStore(time.Minute)
	pa, _ := s.Begin(BeginParams{Product: ProductCalendar})
	s.Discard(pa.State)
	if _, ok := s.Consume(pa.State); ok {
		t.Fatal("discarded state must not consume")
	}
}

func TestStateStore_RandFailure(t *testing.T) {
	s := NewStateStore(time.Minute)
	s.randRead = func([]byte) (int, error) { return 0, errors.New("boom") }
	if _, err := s.Begin(BeginParams{Product: ProductGmail}); err == nil {
		t.Fatal("Begin must fail when randomness is unavailable")
	}
}
