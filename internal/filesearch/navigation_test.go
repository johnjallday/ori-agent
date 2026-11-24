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

// Files' own place is a slot of its own: it never leaks into the standalone
// page's descriptor or the map's, and theirs never into it.
func TestFilesPlaceIsItsOwnSlot(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	s := &Service{life: life, cancel: cancel, places: make(map[tabKey]*tabPlace)}
	defer s.Close()
	const tab = "1822561b-104d-4d57-b079-33dded692620"
	const fresh = "2822561b-104d-4d57-b079-33dded692620"
	files := FilesPlace{Term: "harbor", Scope: `{"w":"ws1"}`, Selected: "idx:abc", Pages: "notes:2,files:3"}
	if got, err := s.FilesPlace("local", tab, nil); got != nil || err != nil {
		t.Fatal("a tab that never saved has a place")
	}
	if _, err := s.FilesPlace("local", tab, &files); err != nil {
		t.Fatal(err)
	}
	files.Term = "changed after the save"
	got, err := s.FilesPlace("local", tab, nil)
	if err != nil || got == nil || got.Term != "harbor" || got.Pages != "notes:2,files:3" || got.Selected != "idx:abc" {
		t.Fatalf("lost or aliased descriptor: %+v %v", got, err)
	}
	// Not another owner's, not another tab's, and not the other surfaces'.
	for _, p := range [][2]string{{"other", tab}, {"local", fresh}} {
		if got, err := s.FilesPlace(p[0], p[1], nil); got != nil || err != nil {
			t.Fatal("shared context")
		}
	}
	if got, _ := s.DeskPlace("local", tab, nil); got != nil {
		t.Fatal("the standalone page received Files' place")
	}
	if got, _ := s.MapPlace("local", tab, nil); got != nil {
		t.Fatal("the map received Files' place")
	}
	// Saving the other slots keeps it, and it survives them.
	if _, err := s.DeskPlace("local", tab, &DeskPlace{Term: "desk", Pages: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MapPlace("local", tab, &MapPlace{Zoom: 1}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.FilesPlace("local", tab, nil); got == nil || got.Term != "harbor" {
		t.Fatal("another slot's save erased Files' place")
	}
	if got, _ := s.DeskPlace("local", tab, nil); got == nil || got.Term != "desk" {
		t.Fatal("Files' place erased the standalone page's")
	}
	// A cleared search is saved as an empty term, so a later back-navigation
	// does not bring an old one back.
	if _, err := s.FilesPlace("local", tab, &FilesPlace{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.FilesPlace("local", tab, nil); got == nil || got.Term != "" {
		t.Fatal("a cleared search was not saved")
	}
	// Caller-owned names are refused.
	if _, err := s.FilesPlace("local", "target", &FilesPlace{}); err != ErrRequest {
		t.Fatal("caller-owned name accepted")
	}
}

func TestFilesPlaceRefusesWhatIsNotBounded(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	s := &Service{life: life, cancel: cancel, places: make(map[tabKey]*tabPlace)}
	defer s.Close()
	const tab = "1822561b-104d-4d57-b079-33dded692620"
	long := func(n int) string {
		out := make([]byte, n)
		for i := range out {
			out[i] = 'a'
		}
		return string(out)
	}
	bad := map[string]FilesPlace{
		"term over 512 bytes":   {Term: long(513)},
		"term over 128 runes":   {Term: string(make([]rune, 129))},
		"invalid utf-8 term":    {Term: "ab\xffcd"},
		"scope over 640 bytes":  {Scope: long(641)},
		"selected over 128":     {Selected: long(129)},
		"pages not kind:count":  {Pages: "notes"},
		"pages with spaces":     {Pages: "notes:2, files:3"},
		"pages count too big":   {Pages: "notes:1000"},
		"pages over 160 bytes":  {Pages: long(161)},
		"pages upper-case kind": {Pages: "Notes:2"},
	}
	for name, place := range bad {
		place := place
		if _, err := s.FilesPlace("local", tab, &place); err != ErrRequest {
			t.Errorf("%s: err = %v, want ErrRequest", name, err)
		}
	}
	if got, _ := s.FilesPlace("local", tab, nil); got != nil {
		t.Fatal("a refused place was kept")
	}
	if _, err := s.FilesPlace("local", tab, &FilesPlace{Term: "ok", Pages: "notes:2"}); err != nil {
		t.Fatalf("a good place was refused: %v", err)
	}
}
