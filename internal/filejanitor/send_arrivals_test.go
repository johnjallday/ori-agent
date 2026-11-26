//go:build darwin || linux

package filejanitor

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pressed is the fixture after one press that sent lease.pdf and invoice.pdf to
// Documents and filed photo.png into Downloads/Filed/Images.
func pressed(t *testing.T) (*sendFilingFixture, FilingResult) {
	t.Helper()
	f := newSendFilingFixture(t, true)
	result, err := f.press(f.plan(), "press-arrivals-0123456789")
	if err != nil || result.Sent != 2 {
		t.Fatalf("press: %v %+v", err, result)
	}
	return f, result
}

func (f *sendFilingFixture) arrivals(workspaceID string) []ArrivalView {
	f.t.Helper()
	got, err := f.s.ListArrivals("local", workspaceID)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

// FR 36, 37: the receiver shows what arrived, from whom, by name.
func TestArrivals_TheReceiverShowsWhatArrived(t *testing.T) {
	f, _ := pressed(t)
	arrivals := f.arrivals("ws-2")
	if len(arrivals) != 1 {
		t.Fatalf("want one arrival, got %+v", arrivals)
	}
	got := arrivals[0]
	slices.Sort(got.Names)
	if got.SenderName != "Downloads" || got.Count != 2 || !slices.Equal(got.Names, []string{"invoice.pdf", "lease.pdf"}) || got.ArrivedAt.IsZero() {
		t.Fatalf("arrival: %+v", got)
	}
	if sender := f.arrivals("ws-1"); len(sender) != 0 {
		t.Fatalf("the sender has nothing arriving: %+v", sender)
	}
	// The receiver's state holds no path either.
	path, _ := f.s.store.scanStatePath("ws-2")
	raw, _ := os.ReadFile(path)
	for _, root := range f.roots {
		if strings.Contains(string(raw), root) {
			t.Fatal("the receiver's state holds a folder path")
		}
	}
}

// FR 38: Undo this send returns only the sent files and leaves the sender's Filed
// files alone; the sender's Undo all then reports them as already put back and
// still undoes the rest.
func TestUndoArrival_ThenUndoAll(t *testing.T) {
	f, result := pressed(t)
	arrival := f.arrivals("ws-2")[0]
	undo, err := f.s.UndoArrival(t.Context(), "local", "ws-2", arrival.ID)
	if err != nil {
		t.Fatalf("undo this send: %v", err)
	}
	if undo.Back != 2 || undo.NotBack != 0 {
		t.Fatalf("undo this send: %+v", undo)
	}
	for _, name := range []string{"lease.pdf", "invoice.pdf"} {
		if !f.at(f.downloads, name) || f.at(f.documents, name) {
			t.Fatalf("%s should be back in Downloads only", name)
		}
	}
	if !f.at(f.downloads, "Filed", "Images", "photo.png") {
		t.Fatal("the sender's Filed file must not be touched by Undo this send")
	}
	if left := f.arrivals("ws-2"); len(left) != 0 {
		t.Fatalf("an undone send leaves no strip: %+v", left)
	}
	receipt, _ := f.s.ReadFiling(t.Context(), "local", "ws-1", result.FilingID, 0)
	if !receipt.UndoAvailable {
		t.Fatal("Undo all must still be available for the Filed file")
	}
	all, err := f.s.UndoFiling(t.Context(), "local", "ws-1", result.FilingID)
	if err != nil {
		t.Fatalf("undo all: %v", err)
	}
	already := 0
	for _, outcome := range all.Outcomes {
		if outcome.Result == UndoOutcomeAlreadyUndone {
			already++
		}
	}
	if all.Back != 3 || already != 2 || !f.at(f.downloads, "photo.png") {
		t.Fatalf("Undo all after Undo this send: %+v", all)
	}
}

// FR 38: Undo all first, then Undo this send finds nothing left to move.
func TestUndoArrival_AfterUndoAllMovesNothing(t *testing.T) {
	f, result := pressed(t)
	arrival := f.arrivals("ws-2")[0]
	if _, err := f.s.UndoFiling(t.Context(), "local", "ws-1", result.FilingID); err != nil {
		t.Fatal(err)
	}
	if left := f.arrivals("ws-2"); len(left) != 0 {
		t.Fatalf("with nothing left at the top level there is no strip: %+v", left)
	}
	undo, err := f.s.UndoArrival(t.Context(), "local", "ws-2", arrival.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range undo.Outcomes {
		if outcome.Result != UndoOutcomeAlreadyUndone {
			t.Fatalf("every file was already back: %+v", undo)
		}
	}
	if !f.at(f.downloads, "lease.pdf") || !f.at(f.downloads, "photo.png") {
		t.Fatal("files moved")
	}
}

// FR 38: a file the receiver has since filed is left where it is and reported.
func TestUndoArrival_AFileTheReceiverFiledIsLeftAndReported(t *testing.T) {
	f, _ := pressed(t)
	arrival := f.arrivals("ws-2")[0]
	if err := os.MkdirAll(filepath.Join(f.documents, "Taxes"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.documents, "lease.pdf"), filepath.Join(f.documents, "Taxes", "lease.pdf")); err != nil {
		t.Fatal(err)
	}
	if got := f.arrivals("ws-2"); len(got) != 1 || got[0].Count != 1 {
		t.Fatalf("the strip counts only what is still loose: %+v", got)
	}
	undo, err := f.s.UndoArrival(t.Context(), "local", "ws-2", arrival.ID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.Back != 1 || undo.NotBack != 1 {
		t.Fatalf("undo: %+v", undo)
	}
	for _, outcome := range undo.Outcomes {
		if outcome.Name == "lease.pdf" && (outcome.Result != UndoOutcomeFailed || !strings.Contains(outcome.Message, "top level")) {
			t.Fatalf("lease.pdf must be reported as left in Documents: %+v", outcome)
		}
	}
	if !f.at(f.documents, "Taxes", "lease.pdf") || !f.at(f.downloads, "invoice.pdf") {
		t.Fatal("the filed file must stay, the loose one comes back")
	}
}

// FR 39: dismissing hides the strip and moves nothing.
func TestArrivals_DismissHidesTheStrip(t *testing.T) {
	f, _ := pressed(t)
	arrival := f.arrivals("ws-2")[0]
	if err := f.s.DismissArrival("local", "ws-2", arrival.ID); err != nil {
		t.Fatal(err)
	}
	if left := f.arrivals("ws-2"); len(left) != 0 {
		t.Fatalf("dismissed: %+v", left)
	}
	if !f.at(f.documents, "lease.pdf") {
		t.Fatal("dismissing moved a file")
	}
	if err := f.s.DismissArrival("local", "ws-2", "arrival-missing"); !errors.Is(err, ErrArrivalNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err := f.s.UndoArrival(t.Context(), "local", "ws-1", arrival.ID); !errors.Is(err, ErrArrivalNotFound) {
		t.Fatalf("the sender cannot undo an arrival it does not hold, got %v", err)
	}
}

// An arrival record is validated like the rest of the scan state.
func TestArrivals_RecordIsValidated(t *testing.T) {
	f, _ := pressed(t)
	state, _ := f.s.store.LoadScanState("ws-2")
	arrival := state.Arrivals[0]
	for name, mutate := range map[string]func(*SendArrival){
		"from itself": func(a *SendArrival) { a.SenderWorkspaceID = "ws-2" },
		"no files":    func(a *SendArrival) { a.Files = nil },
		"a path for name": func(a *SendArrival) {
			a.Files = []ArrivedFile{{ActionID: "x", After: Fingerprint{Name: "a/b.pdf", FileID: "1:2"}}}
		},
		"bad id": func(a *SendArrival) { a.ID = "../x" },
	} {
		bad := arrival
		mutate(&bad)
		if err := bad.validate("ws-2"); err == nil {
			t.Errorf("%s: want refused", name)
		}
	}
}
