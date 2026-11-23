package filesearch

import (
	"context"
	"testing"
)

func TestTabPlacesIsolateOwnerSurfaceAndLifetime(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	s := &Service{life: life, cancel: cancel, places: make(map[tabKey]*tabPlace)}
	defer s.Close()
	const tab = "1822561b-104d-4d57-b079-33dded692620"
	const fresh = "2822561b-104d-4d57-b079-33dded692620"
	v := DeskPlace{Term: "proposal", Pages: 2, Selected: "opaque", Generation: 3}
	if _, err := s.DeskPlace("local", tab, &v); err != nil {
		t.Fatal(err)
	}
	v.Term = "changed"
	for _, p := range [][2]string{{"other", tab}, {"local", fresh}} {
		got, err := s.DeskPlace(p[0], p[1], nil)
		if got != nil || err != nil {
			t.Fatal("shared context")
		}
	}
	if got, err := s.MapPlace("local", tab, nil); got != nil || err != nil {
		t.Fatal("map received a file descriptor")
	}
	if _, err := s.MapPlace("local", tab, &MapPlace{Zoom: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DeskPlace("local", tab, nil)
	if err != nil || got == nil || got.Term != "proposal" {
		t.Fatal("lost descriptor")
	}
	if _, err = s.DeskPlace("local", "target", &v); err != ErrRequest {
		t.Fatal("caller-owned name accepted")
	}
	s.Close()
	if _, err = s.DeskPlace("local", tab, nil); err != ErrUnavailable || len(s.places) != 0 {
		t.Fatal("service shutdown retained context")
	}
}
