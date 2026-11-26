//go:build darwin || linux

package filejanitor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// listingWorkspaceStore adds the optional List capability the connection code
// uses to find the other side.
type listingWorkspaceStore struct{ *fakeWorkspaceStore }

func (l listingWorkspaceStore) List() ([]string, error) {
	ids := make([]string, 0, len(l.workspaces))
	for id := range l.workspaces {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// connectionFixture is three workspaces of one owner: Downloads (ws-1) and
// Documents (ws-2) with Filing set up on sibling folders on one disk, and Music
// (ws-3) with no Filing.
type connectionFixture struct {
	t          *testing.T
	s          *Service
	workspaces *fakeWorkspaceStore
	base       string
	roots      map[string]string
}

func newConnectionFixture(t *testing.T) *connectionFixture {
	t.Helper()
	store, _ := newTestStore(t)
	fake := newFakeWorkspaceStore("ws-1", "ws-2", "ws-3")
	f := &connectionFixture{t: t, s: NewService(store, listingWorkspaceStore{fake}), workspaces: fake, base: tempDirCanonical(t), roots: map[string]string{}}
	names := map[string]string{"ws-1": "Downloads", "ws-2": "Documents", "ws-3": "Music"}
	for id, name := range names {
		ws := fake.workspaces[id]
		ws.Name = name
		ws.OwnerUserID = "local"
	}
	f.setUp("ws-1", "Downloads")
	f.setUp("ws-2", "Documents")
	mover := &platformMover{}
	for id, root := range f.roots {
		info, err := os.Lstat(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := mover.Supported(root, dirWitness(info)); err != nil {
			t.Skipf("exclusive rename is not supported for %s: %v", id, err)
		}
	}
	return f
}

// setUp gives a workspace Filing on a new folder named dir.
func (f *connectionFixture) setUp(workspaceID, dir string) {
	f.t.Helper()
	root := filepath.Join(f.base, dir)
	if err := os.MkdirAll(root, 0o750); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.s.ConfirmSetup(SetupRequest{WorkspaceID: workspaceID, Path: root}); err != nil {
		f.t.Fatalf("ConfirmSetup %s: %v", workspaceID, err)
	}
	ws := f.workspaces.workspaces[workspaceID]
	ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace}}
	f.roots[workspaceID] = root
}

func (f *connectionFixture) connect(sender, receiver string, kinds ...string) (Connections, error) {
	return f.s.ConnectWorkspace("local", sender, ConnectRequest{ReceiverWorkspaceID: receiver, Kinds: kinds})
}

func (f *connectionFixture) list(workspaceID string) Connections {
	f.t.Helper()
	got, err := f.s.ListConnections("local", workspaceID)
	if err != nil {
		f.t.Fatalf("ListConnections %s: %v", workspaceID, err)
	}
	return got
}

func (f *connectionFixture) mustConnect(sender, receiver string, kinds ...string) ConnectionView {
	f.t.Helper()
	got, err := f.connect(sender, receiver, kinds...)
	if err != nil {
		f.t.Fatalf("connect %s -> %s: %v", sender, receiver, err)
	}
	for _, view := range got.SendsTo {
		if view.ReceiverWorkspaceID == receiver {
			return view
		}
	}
	f.t.Fatalf("connection %s -> %s not listed: %+v", sender, receiver, got)
	return ConnectionView{}
}

// refusal requires a connection_refused error whose sentence contains want.
func refusal(t *testing.T, err error, want string) {
	t.Helper()
	var setupError *SetupError
	if !errors.As(err, &setupError) || setupError.Code != CodeConnectionRefused {
		t.Fatalf("want a connection refusal, got %v", err)
	}
	if !strings.Contains(setupError.Message, want) {
		t.Fatalf("refusal %q does not say %q", setupError.Message, want)
	}
	if strings.Contains(strings.ToLower(setupError.Message), "linked") {
		t.Fatalf("refusal uses the word linked: %q", setupError.Message)
	}
}

func dirWitness(info os.FileInfo) string { return directoryid.Witness(info) }

func TestConnect_TurnsOnOneWayAndBothSidesSayIt(t *testing.T) {
	f := newConnectionFixture(t)
	before, _ := f.s.store.LoadSettings("ws-1")
	if len(before.SendsTo) != 0 {
		t.Fatal("no connection may exist before the person turns one on (FR 3)")
	}

	view := f.mustConnect("ws-1", "ws-2", "documents")
	if !view.On || view.Ended != "" || view.SenderName != "Downloads" || view.ReceiverName != "Documents" || !slices.Equal(view.Kinds, []Category{CategoryDocuments}) {
		t.Fatalf("sender's view: %+v", view)
	}
	receiver := f.list("ws-2")
	if len(receiver.ReceivesFrom) != 1 || !receiver.ReceivesFrom[0].On || receiver.ReceivesFrom[0].SenderName != "Downloads" || len(receiver.SendsTo) != 0 {
		t.Fatalf("receiver's view: %+v", receiver)
	}
	// One way: the receiver does not send back.
	if sender := f.list("ws-1"); len(sender.ReceivesFrom) != 0 {
		t.Fatalf("the sender must not appear to receive: %+v", sender.ReceivesFrom)
	}
	// No path in either answer, or in what is stored about the other folder.
	for _, id := range []string{"ws-1", "ws-2"} {
		encoded, _ := json.Marshal(f.list(id))
		for _, root := range f.roots {
			if strings.Contains(string(encoded), root) {
				t.Fatalf("%s's connections answer carries a folder path: %s", id, encoded)
			}
		}
	}
	stored, _ := f.s.store.LoadSettings("ws-1")
	encoded, _ := json.Marshal(stored.SendsTo)
	if strings.Contains(string(encoded), f.roots["ws-2"]) {
		t.Fatalf("the connection stores the receiver's path: %s", encoded)
	}
}

func TestConnect_OffersOnlyWorkspacesWithFilingAndPreTicksOnlyAGuidedDocumentsOne(t *testing.T) {
	f := newConnectionFixture(t)
	got := f.list("ws-1")
	if len(got.Receivers) != 1 || got.Receivers[0].WorkspaceID != "ws-2" || got.Receivers[0].GuidedDocuments {
		t.Fatalf("only Documents has Filing, and it was not made from the guided suggestion: %+v", got.Receivers)
	}
	f.workspaces.workspaces["ws-2"].SharedData = map[string]any{"guided_purpose": "documents"}
	if got := f.list("ws-1"); !got.Receivers[0].GuidedDocuments {
		t.Fatalf("a guided Documents workspace pre-ticks Documents: %+v", got.Receivers)
	}
}

func TestConnect_RefusesWithOnePlainSentence(t *testing.T) {
	t.Run("no kind ticked", func(t *testing.T) {
		f := newConnectionFixture(t)
		_, err := f.connect("ws-1", "ws-2")
		refusal(t, err, "Tick at least one kind")
	})
	t.Run("an unknown kind", func(t *testing.T) {
		f := newConnectionFixture(t)
		_, err := f.connect("ws-1", "ws-2", "documents", "secrets")
		refusal(t, err, "Choose kinds from the list")
	})
	t.Run("itself", func(t *testing.T) {
		f := newConnectionFixture(t)
		_, err := f.connect("ws-1", "ws-1", "documents")
		refusal(t, err, "another workspace")
	})
	t.Run("a receiver with no Filing", func(t *testing.T) {
		f := newConnectionFixture(t)
		_, err := f.connect("ws-1", "ws-3", "documents")
		refusal(t, err, "Music has no Filing set up on its folder")
	})
	t.Run("a receiver whose Filing is paused", func(t *testing.T) {
		f := newConnectionFixture(t)
		if _, err := f.s.store.UpdateSettings("ws-2", func(s *JanitorSettings) error {
			s.AutomationApprovedAt, s.Paused = time.Now(), true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		_, err := f.connect("ws-1", "ws-2", "documents")
		refusal(t, err, "Documents's Filing is paused")
		if got := f.list("ws-1"); got.Receivers[0].Refusal == "" {
			t.Fatalf("the card should already say why: %+v", got.Receivers)
		}
	})
	t.Run("a receiver that lost folder access", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.workspaces.workspaces["ws-2"].MCPBindings = nil
		_, err := f.connect("ws-1", "ws-2", "documents")
		refusal(t, err, "lost access to its folder")
	})
	t.Run("folders on different disks", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.s.SetConditionalMover(sendSupportStub{})
		f.s.SetConditionalMover(crossDeviceStub{})
		_, err := f.connect("ws-1", "ws-2", "documents")
		refusal(t, err, "are on different disks")
	})
	t.Run("a file system the mover refuses", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.s.SetConditionalMover(sendSupportStub{refuse: f.roots["ws-2"]})
		_, err := f.connect("ws-1", "ws-2", "documents")
		refusal(t, err, "without risking replacing another")
	})
	t.Run("the same receiver twice", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		_, err := f.connect("ws-1", "ws-2", "data")
		refusal(t, err, "Downloads already sends to Documents")
	})
	t.Run("the reverse of a connection that is on", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		_, err := f.connect("ws-2", "ws-1", "images")
		refusal(t, err, "sends one way only")
		if got := f.list("ws-2"); got.Receivers[0].Refusal == "" {
			t.Fatalf("the card should already say why: %+v", got.Receivers)
		}
	})
	t.Run("a kind another connection carries", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		f.setUp("ws-3", "Music")
		_, err := f.connect("ws-1", "ws-3", "audio", "documents")
		refusal(t, err, "Documents files already go to Documents")
		f.mustConnect("ws-1", "ws-3", "audio") // a kind nobody carries is fine
	})
	t.Run("nothing is written when refused", func(t *testing.T) {
		f := newConnectionFixture(t)
		_, _ = f.connect("ws-1", "ws-3", "documents")
		if settings, _ := f.s.store.LoadSettings("ws-1"); len(settings.SendsTo) != 0 {
			t.Fatalf("a refused connection was stored: %+v", settings.SendsTo)
		}
	})
}

func TestConnection_EndsWhenEitherSideChangesAndBothSidesSaySo(t *testing.T) {
	ended := func(t *testing.T, f *connectionFixture, want string) {
		t.Helper()
		sender, receiver := f.list("ws-1"), f.list("ws-2")
		if len(sender.SendsTo) != 1 || sender.SendsTo[0].On || !strings.Contains(sender.SendsTo[0].Ended, want) {
			t.Fatalf("sender should say the connection ended (%q): %+v", want, sender.SendsTo)
		}
		if len(receiver.ReceivesFrom) != 1 || receiver.ReceivesFrom[0].On || receiver.ReceivesFrom[0].Ended != sender.SendsTo[0].Ended {
			t.Fatalf("receiver should say the same: %+v", receiver.ReceivesFrom)
		}
	}
	t.Run("the receiver points at another folder", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		other := filepath.Join(f.base, "Papers")
		if err := os.Mkdir(other, 0o750); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.Relink(nil, RelinkRequest{WorkspaceID: "ws-2", Path: other}); err != nil {
			t.Fatal(err)
		}
		ended(t, f, "Documents now points at a different folder")
	})
	t.Run("the sender points at another folder", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		other := filepath.Join(f.base, "Inbox")
		if err := os.Mkdir(other, 0o750); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.Relink(nil, RelinkRequest{WorkspaceID: "ws-1", Path: other}); err != nil {
			t.Fatal(err)
		}
		ended(t, f, "Downloads now points at a different folder")
	})
	t.Run("the receiver's folder is replaced by another folder of the same name", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		if err := os.Rename(f.roots["ws-2"], f.roots["ws-2"]+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(f.roots["ws-2"], 0o750); err != nil {
			t.Fatal(err)
		}
		ended(t, f, "moved or replaced")
	})
	t.Run("the receiver's Filing is removed", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		if _, err := f.s.RevokeAccess(nil, "ws-2"); err != nil {
			t.Fatal(err)
		}
		ended(t, f, "Documents no longer has Filing set up")
	})
	t.Run("the receiver is deleted", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		delete(f.workspaces.workspaces, "ws-2")
		sender := f.list("ws-1")
		if len(sender.SendsTo) != 1 || sender.SendsTo[0].On || !strings.Contains(sender.SendsTo[0].Ended, "no longer has Filing set up") {
			t.Fatalf("a deleted receiver ends the connection: %+v", sender.SendsTo)
		}
	})
	t.Run("the sender's Filing is removed", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		if _, err := f.s.RevokeAccess(nil, "ws-1"); err != nil {
			t.Fatal(err)
		}
		if settings, _ := f.s.store.LoadSettings("ws-1"); len(settings.SendsTo) != 0 {
			t.Fatalf("removing the sender's Filing ends its connections: %+v", settings.SendsTo)
		}
		if got := f.list("ws-2"); len(got.ReceivesFrom) != 0 {
			t.Fatalf("the receiver still lists it: %+v", got.ReceivesFrom)
		}
	})
	t.Run("an ended connection is replaced by turning it on again", func(t *testing.T) {
		f := newConnectionFixture(t)
		f.mustConnect("ws-1", "ws-2", "documents")
		other := filepath.Join(f.base, "Papers")
		if err := os.Mkdir(other, 0o750); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.Relink(nil, RelinkRequest{WorkspaceID: "ws-2", Path: other}); err != nil {
			t.Fatal(err)
		}
		view := f.mustConnect("ws-1", "ws-2", "documents")
		if !view.On {
			t.Fatalf("turned on again: %+v", view)
		}
		if got := f.list("ws-1"); len(got.SendsTo) != 1 {
			t.Fatalf("the ended record should be replaced, not kept beside the new one: %+v", got.SendsTo)
		}
	})
}

func TestTurnOff_FromEitherSideMovesNoFile(t *testing.T) {
	t.Run("from the sender", func(t *testing.T) {
		f := newConnectionFixture(t)
		view := f.mustConnect("ws-1", "ws-2", "documents")
		got, err := f.s.TurnOffConnection("local", "ws-1", view.ID)
		if err != nil || len(got.SendsTo) != 0 {
			t.Fatalf("turn off: %v %+v", err, got)
		}
		if receiver := f.list("ws-2"); len(receiver.ReceivesFrom) != 0 {
			t.Fatalf("the receiver still lists it: %+v", receiver.ReceivesFrom)
		}
	})
	t.Run("from the receiver", func(t *testing.T) {
		f := newConnectionFixture(t)
		view := f.mustConnect("ws-1", "ws-2", "documents")
		got, err := f.s.TurnOffConnection("local", "ws-2", view.ID)
		if err != nil || len(got.ReceivesFrom) != 0 {
			t.Fatalf("turn off from the receiver: %v %+v", err, got)
		}
		if settings, _ := f.s.store.LoadSettings("ws-1"); len(settings.SendsTo) != 0 {
			t.Fatalf("the sender's record survived: %+v", settings.SendsTo)
		}
	})
	t.Run("twice is not an error", func(t *testing.T) {
		f := newConnectionFixture(t)
		view := f.mustConnect("ws-1", "ws-2", "documents")
		if _, err := f.s.TurnOffConnection("local", "ws-1", view.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.TurnOffConnection("local", "ws-1", view.ID); err != nil {
			t.Fatalf("a second turn off: %v", err)
		}
	})
	t.Run("a third workspace cannot turn it off", func(t *testing.T) {
		f := newConnectionFixture(t)
		view := f.mustConnect("ws-1", "ws-2", "documents")
		if _, err := f.s.TurnOffConnection("local", "ws-3", view.ID); err != nil {
			t.Fatal(err)
		}
		if settings, _ := f.s.store.LoadSettings("ws-1"); len(settings.SendsTo) != 1 {
			t.Fatalf("a workspace that is neither side removed the connection: %+v", settings.SendsTo)
		}
	})
	t.Run("another owner cannot turn it off", func(t *testing.T) {
		f := newConnectionFixture(t)
		view := f.mustConnect("ws-1", "ws-2", "documents")
		if _, err := f.s.TurnOffConnection("someone-else", "ws-2", view.ID); err != nil {
			t.Fatal(err)
		}
		if settings, _ := f.s.store.LoadSettings("ws-1"); len(settings.SendsTo) != 1 {
			t.Fatalf("another owner removed the connection: %+v", settings.SendsTo)
		}
	})
	t.Run("no file moves", func(t *testing.T) {
		f := newConnectionFixture(t)
		view := f.mustConnect("ws-1", "ws-2", "documents")
		writeFile(t, filepath.Join(f.roots["ws-1"], "lease.pdf"), 10)
		writeFile(t, filepath.Join(f.roots["ws-2"], "arrived.pdf"), 10)
		if _, err := f.s.TurnOffConnection("local", "ws-2", view.ID); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{filepath.Join(f.roots["ws-1"], "lease.pdf"), filepath.Join(f.roots["ws-2"], "arrived.pdf")} {
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("%s moved: %v", path, err)
			}
		}
	})
}

func TestSendConnections_SaveRulesAndLoadDropsAMalformedList(t *testing.T) {
	good := SendConnection{
		ID: "connection-a", ReceiverWorkspaceID: "ws-2", Kinds: []Category{CategoryDocuments},
		SenderRootID: "gen-1", ReceiverRootID: "gen-2", SenderRootDirID: "1:10", ReceiverRootDirID: "1:20",
		CreatedAt: time.Now(), CreatedBy: "local",
	}
	if err := validateSendConnections("ws-1", []SendConnection{good}); err != nil {
		t.Fatalf("a good record: %v", err)
	}
	bad := map[string]func(*SendConnection){
		"no kinds":            func(c *SendConnection) { c.Kinds = nil },
		"unknown kind":        func(c *SendConnection) { c.Kinds = []Category{"secrets"} },
		"a kind twice":        func(c *SendConnection) { c.Kinds = []Category{CategoryDocuments, CategoryDocuments} },
		"to itself":           func(c *SendConnection) { c.ReceiverWorkspaceID = "WS-1" },
		"no folder identity":  func(c *SendConnection) { c.ReceiverRootDirID = "" },
		"a path for an ID":    func(c *SendConnection) { c.ReceiverRootDirID = "/Users/me/Documents" },
		"a bad generation":    func(c *SendConnection) { c.SenderRootID = "../x" },
		"not a connection ID": func(c *SendConnection) { c.ID = "action-1" },
		"no creator":          func(c *SendConnection) { c.CreatedBy = "" },
	}
	for name, mutate := range bad {
		c := good
		c.Kinds = append([]Category(nil), good.Kinds...)
		mutate(&c)
		if err := validateSendConnections("ws-1", []SendConnection{c}); err == nil {
			t.Errorf("%s: want refused", name)
		}
	}
	second := good
	second.ID, second.ReceiverWorkspaceID = "connection-b", "ws-3"
	if err := validateSendConnections("ws-1", []SendConnection{good, second}); err == nil {
		t.Error("one kind to two receivers must be refused (FR 7)")
	}
	second.Kinds = []Category{CategoryImages}
	if err := validateSendConnections("ws-1", []SendConnection{good, second}); err != nil {
		t.Errorf("two receivers with different kinds: %v", err)
	}
	second.ReceiverWorkspaceID = "ws-2"
	if err := validateSendConnections("ws-1", []SendConnection{good, second}); err == nil {
		t.Error("two connections to one receiver must be refused")
	}

	settings := NewSettings("ws-1")
	settings.SendsTo = []SendConnection{good, second}
	if got := settings.Normalize().SendsTo; got != nil {
		t.Fatalf("a malformed list must load as no connections, got %+v", got)
	}
	settings.SendsTo = []SendConnection{good}
	if got := settings.Normalize().SendsTo; len(got) != 1 {
		t.Fatalf("a good list must survive loading, got %+v", got)
	}
	settings.SchemaVersion = SettingsSchemaVersion + 1
	if got := settings.Normalize().SendsTo; got != nil {
		t.Fatalf("a record from another settings schema must not keep its connections, got %+v", got)
	}
}

// crossDeviceStub accepts both folders but reports them on different disks.
type crossDeviceStub struct{ sendSupportStub }

func (crossDeviceStub) Supported(string, string) error { return ErrMoveCrossDevice }
