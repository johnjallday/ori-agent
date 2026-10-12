package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func observationFixture(t *testing.T) (*folderDigestFixture, *FolderObservationService, foldercontext.Target) {
	t.Helper()
	f := newFolderDigestFixture(t)
	f.service.deps.Scan = func(root string) (folderdigest.Result, error) {
		return folderdigest.Scan(root, folderdigest.Options{Now: func() time.Time { return f.now }})
	}
	f.service.deps.ScanObservation = func(root string) (folderdigest.Result, error) {
		return folderdigest.Scan(root, folderdigest.Options{CaptureTree: true, Now: func() time.Time { return f.now }})
	}
	binding, err := f.store.Binding(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	return f, NewFolderObservationService(f.service), foldercontext.Target{UserID: "local", WorkspaceID: binding.HQWorkspaceID, AgentName: "Atlas", DraftID: "draft-a"}
}

func TestFolderObservation_LocalOnlyAndBounded(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()
	pending, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	// Unreadable files still yield names/kinds, proving no content read is
	// required. The scanner's dedicated suite also verifies this contract.
	if err := os.Chmod(filepath.Join(f.home, "Desktop", "todo.txt"), 0); err != nil {
		t.Fatal(err)
	}
	observation, err := service.Observe(ctx, target, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if observation.Files != 2 || observation.Folder != "Desktop" || observation.Validate() != nil {
		t.Fatalf("observation: %+v", observation)
	}
	after, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if before.Version != after.Version || after.Pending() == nil || after.Pending().ID != pending.ID {
		t.Fatal("observing changed the shared offer")
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{f.home, "folder_key", "identity"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("private metadata leaked: %s", forbidden)
		}
	}
	if observation.Tree == nil || len(observation.Tree.Nodes) != 2 || !strings.Contains(string(encoded), "todo.txt") || !strings.Contains(string(encoded), "photo.png") {
		t.Fatal("explicit attachment did not retain genuine bounded metadata names")
	}
	resolved, err := service.Resolve(ctx, target, observation.ID)
	if err != nil {
		t.Fatal(err)
	}
	resolved.Kinds[0].Name = "tampered"
	resolved.Tree.Nodes[0].Name = "tampered"
	resolved, err = service.Resolve(ctx, target, observation.ID)
	if err != nil || resolved.Kinds[0].Name == "tampered" || resolved.Tree.Nodes[0].Name == "tampered" {
		t.Fatal("caller mutated held observation")
	}
}

// The drawer's chooser is told why the dialog is missing in Home's words. A
// picker's reason is a code and must never be the note.
func TestFolderObservation_ChoicesExplainAMissingDialogInWords(t *testing.T) {
	f, service, _ := observationFixture(t)
	ctx := context.Background()
	codes := []string{FolderDialogUnavailableDesktopOff, FolderDialogUnavailablePlatform, "some_new_reason"}
	for _, reason := range codes {
		t.Run(reason, func(t *testing.T) {
			f.service.deps.Picker = offPicker{reason: reason}
			choices, err := service.Choices(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			home, err := f.service.Current(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			if choices.PickerAvailable || len(choices.Chips) != 3 || choices.PickerNote == "" || choices.PickerNote != home.PickerNote {
				t.Fatalf("choices=%+v home note=%q", choices, home.PickerNote)
			}
			encoded, err := json.Marshal(choices)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				PickerNote string `json:"picker_note"`
			}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			for _, code := range codes {
				if strings.Contains(payload.PickerNote, code) {
					t.Fatalf("picker_note carries the reason code %q: %q", code, payload.PickerNote)
				}
			}
			if !strings.HasPrefix(payload.PickerNote, "Pick a folder from the list; ") {
				t.Fatalf("picker_note = %q", payload.PickerNote)
			}
		})
	}

	// The sandboxed server's exact sentence, the one Home shows.
	f.service.deps.Picker = offPicker{reason: FolderDialogUnavailableDesktopOff}
	choices, err := service.Choices(ctx, "local")
	if err != nil || choices.PickerNote != "Pick a folder from the list; the folder dialog is switched off in this session." {
		t.Fatalf("note=%q err=%v", choices.PickerNote, err)
	}

	// A working dialog over the usual folders needs no note at all.
	f.service.deps.Picker = fakeFolderPicker{}
	choices, err = service.Choices(ctx, "local")
	if err != nil || !choices.PickerAvailable || choices.PickerNote != "" {
		t.Fatalf("choices=%+v err=%v", choices, err)
	}
}

// A typed path reaches the chooser as a selection token the server resolves.
// The token names the folder; nothing the page sends is ever used as a path.
func TestFolderObservation_SelectionTokenNamesTheFolder(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()
	music := filepath.Join(f.home, "Music", "REAPER Projects")
	if err := os.MkdirAll(filepath.Join(music, "Night Drive"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(music, "Night Drive", "Night Drive.rpp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var asked []string
	f.service.deps.ResolveSelection = func(token string) (string, error) {
		asked = append(asked, token)
		if token != "live-token" {
			return "", errors.New("unknown token")
		}
		return music, nil
	}

	observation, err := service.Observe(ctx, target, FolderObserveSelection, "live-token")
	if err != nil || observation == nil || observation.Folder != "REAPER Projects" {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), f.home) || strings.Contains(string(encoded), "live-token") {
		t.Fatalf("the observation carries the path or the token: %s", encoded)
	}

	var rootErr *FolderRootError
	for name, choice := range map[string]string{"an expired token": "gone", "a path in place of a token": music} {
		if _, err := service.Observe(ctx, target, FolderObserveSelection, choice); !errors.As(err, &rootErr) || rootErr.Message != folderSelectionLostMessage {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
	if len(asked) != 3 {
		t.Fatalf("resolver asked %q", asked)
	}
	if _, err := service.Observe(ctx, target, FolderObserveSelection, "  "); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("blank token err=%v", err)
	}
	if _, err := service.Observe(ctx, target, FolderObservePicker, "live-token"); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("a token in picker mode err=%v", err)
	}
	if _, err := service.Observe(ctx, target, "path", music); !errors.Is(err, ErrFolderSelection) {
		t.Fatalf("an unknown mode err=%v", err)
	}

	// With no resolver wired every selection is refused.
	f.service.deps.ResolveSelection = nil
	if _, err := service.Observe(ctx, target, FolderObserveSelection, "live-token"); !errors.As(err, &rootErr) {
		t.Fatalf("no resolver err=%v", err)
	}
}

func TestFolderObservation_ScopeBindingLifetimeAndRestart(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()
	observation, err := service.Observe(ctx, target, "chip", "documents")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*foldercontext.Target){
		func(t *foldercontext.Target) { t.UserID = "other" },
		func(t *foldercontext.Target) { t.WorkspaceID = "other" },
		func(t *foldercontext.Target) { t.AgentName = "other" },
		func(t *foldercontext.Target) { t.DraftID = "other" },
		func(t *foldercontext.Target) { t.ConversationID, t.DraftID = "other", "" },
	} {
		foreign := target
		change(&foreign)
		if _, err := service.Resolve(ctx, foreign, observation.ID); !errors.Is(err, ErrFolderSelection) {
			t.Fatalf("foreign selection: %v", err)
		}
	}
	service.BindSaved(target, observation.ID, "saved-a")
	if _, err := service.Resolve(ctx, target, observation.ID); !errors.Is(err, ErrFolderSelection) {
		t.Fatal("staged ref reused after bind")
	}
	target.ConversationID, target.DraftID = "saved-a", ""
	if _, err := service.Resolve(ctx, target, observation.ID); err != nil {
		t.Fatal(err)
	}
	if reason := NewFolderObservationService(f.service).Status(ctx, target, *observation); reason != FolderContinuationLost {
		t.Fatalf("restart: %s", reason)
	}
	f.now = f.now.Add(foldercontext.SelectionTTL)
	if reason := service.Status(ctx, target, *observation); reason != FolderContinuationExpired {
		t.Fatalf("expiry: %s", reason)
	}
}

func TestFolderObservation_ReplacementAndCancellationPreservePrevious(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()
	observation, err := service.Observe(ctx, target, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	f.service.deps.Picker = fakeFolderPicker{}
	if cancelled, err := service.Observe(ctx, target, "picker", ""); err != nil || cancelled != nil {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	if _, err := service.Observe(ctx, target, "chip", "untrusted/path"); err == nil {
		t.Fatal("accepted arbitrary chip")
	}
	if _, err := service.Resolve(ctx, target, observation.ID); err != nil {
		t.Fatal("previous lost after cancellation:", err)
	}
	root := filepath.Join(f.home, "Desktop")
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if reason := service.Status(ctx, target, *observation); reason != FolderContinuationChanged {
		t.Fatalf("replacement: %s", reason)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root+"-old", root); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(ctx, target, observation.ID); err == nil {
		t.Fatal("followed replacement symlink")
	}
}

func TestFolderObservation_BudgetsPartialAndPruning(t *testing.T) {
	f, service, target := observationFixture(t)
	ctx := context.Background()
	f.service.deps.ScanObservation = func(root string) (folderdigest.Result, error) {
		return folderdigest.Scan(root, folderdigest.Options{CaptureTree: true, MaxEntries: 5, Now: func() time.Time { return f.now }})
	}
	for range folderSelectionsPerOwner {
		observation, err := service.Observe(ctx, target, "chip", "documents")
		if err != nil {
			t.Fatal(err)
		}
		if !observation.Coverage.Partial || observation.Entries > 5 {
			t.Fatal("missing partial coverage")
		}
	}
	if _, err := service.Observe(ctx, target, "chip", "documents"); !errors.Is(err, ErrFolderSelectionLimit) {
		t.Fatalf("limit: %v", err)
	}
	f.now = f.now.Add(foldercontext.SelectionTTL)
	if _, err := service.Observe(ctx, target, "chip", "documents"); err != nil {
		t.Fatal("expired staging not pruned:", err)
	}
}

func TestFolderObservation_ChangedDuringScanRefusesSnapshot(t *testing.T) {
	f, service, target := observationFixture(t)
	f.service.deps.ScanObservation = func(root string) (folderdigest.Result, error) {
		result, err := folderdigest.Scan(root, folderdigest.Options{CaptureTree: true, Now: func() time.Time { return f.now }})
		if err != nil {
			return result, err
		}
		if err := os.Rename(root, root+"-old"); err != nil {
			return result, err
		}
		return result, os.Mkdir(root, 0o750)
	}
	if _, err := service.Observe(context.Background(), target, "chip", "desktop"); !errors.Is(err, ErrFolderPathLost) {
		t.Fatalf("changed source accepted: %v", err)
	}
}
