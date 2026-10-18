package linkcontext

import (
	"fmt"
	"testing"
	"time"
)

func heldObservation(id string) Observation {
	return Observation{Version: Version, ID: id, URL: "https://example.com/" + id, Host: "example.com", Kind: KindPage, FetchedAt: fixedNow}
}

func TestHeldLinks_HoldResolveAndRelease(t *testing.T) {
	now := fixedNow
	held := NewHeldLinks().WithClock(func() time.Time { return now })
	id := held.Hold("local", heldObservation("a"))
	if id != "a" {
		t.Fatalf("id: %q", id)
	}
	if got, ok := held.Resolve("local", "a"); !ok || got.URL != "https://example.com/a" {
		t.Fatalf("resolve: %+v %v", got, ok)
	}
	if _, ok := held.Resolve("someone-else", "a"); ok {
		t.Fatal("another user's id must not resolve")
	}
	held.Release("someone-else", "a")
	if _, ok := held.Resolve("local", "a"); !ok {
		t.Fatal("another user cannot release it")
	}
	held.Release("local", "a")
	if _, ok := held.Resolve("local", "a"); ok {
		t.Fatal("a released link is gone")
	}
	held.Release("local", "a")
	if held.Hold("", heldObservation("b")) != "" || held.Hold("local", Observation{}) != "" {
		t.Fatal("a link needs a user and an id")
	}
}

func TestHeldLinks_UsingALinkKeepsItAndIdleOnesExpire(t *testing.T) {
	now := fixedNow
	held := NewHeldLinks().WithClock(func() time.Time { return now })
	held.Hold("local", heldObservation("a"))
	held.Hold("local", heldObservation("b"))
	now = now.Add(HeldTTL - time.Minute)
	if _, ok := held.Resolve("local", "a"); !ok {
		t.Fatal("still within its TTL")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := held.Resolve("local", "a"); !ok {
		t.Fatal("using it kept it for another TTL")
	}
	if _, ok := held.Resolve("local", "b"); ok {
		t.Fatal("an idle link expires")
	}
}

func TestHeldLinks_AFullHandLetsTheLeastRecentlyUsedGo(t *testing.T) {
	now := fixedNow
	held := NewHeldLinks().WithClock(func() time.Time { return now })
	for i := 0; i < MaxHeldPerUser; i++ {
		now = now.Add(time.Second)
		held.Hold("local", heldObservation(fmt.Sprintf("l%d", i)))
	}
	now = now.Add(time.Second)
	held.Resolve("local", "l0") // the oldest, used again
	now = now.Add(time.Second)
	held.Hold("local", heldObservation("new"))
	held.Hold("other", heldObservation("theirs"))
	if _, ok := held.Resolve("local", "l1"); ok {
		t.Fatal("the least recently used link should have gone")
	}
	for _, id := range []string{"l0", "l2", "new"} {
		if _, ok := held.Resolve("local", id); !ok {
			t.Fatalf("%s should still be held", id)
		}
	}
	if _, ok := held.Resolve("other", "theirs"); !ok {
		t.Fatal("another user's hand is separate")
	}
}
