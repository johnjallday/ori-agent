package reaper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRegisteredActionsReadsSCRRowsWithoutWritingKeyboardConfig(t *testing.T) {
	input := strings.Join([]string{
		`ACT 0 0 "not a script"`,
		`SCR 4 0 RS1ee9bb229dabffe151848d7efa3c10f748e1a1cf "Custom: lyrics.lua" Cockos/lyrics.lua`,
		`SCR 4 32060 RS7d3c_1ee9bb229dabffe151848d7efa3c10f748e1a1cf "Custom: MIDI lyrics.lua" Cockos/lyrics.lua`,
		`SCR broken`,
		`SCR 4 0 bad/id "Custom: unsafe.lua" unsafe.lua`,
	}, "\n")
	actions, err := ParseRegisteredActions(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 {
		t.Fatalf("registered actions = %+v", actions)
	}
	if actions[0].ID != "_RS1ee9bb229dabffe151848d7efa3c10f748e1a1cf" || actions[0].Label != "lyrics.lua" {
		t.Fatalf("first action = %+v", actions[0])
	}
	if actions[1].ID != "_RS7d3c_1ee9bb229dabffe151848d7efa3c10f748e1a1cf" || actions[1].Source != ActionSourceRegistered || !actions[1].Mutates || !actions[1].NeedsConfirmation {
		t.Fatalf("second action = %+v", actions[1])
	}
}

func TestCatalogCombinesBuiltinsAndRegisteredScriptsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reaper-kb.ini")
	content := []byte(`SCR 4 0 RS59bf9c1cf8a2bb77dd8133adf4398b84de85b840 "Custom: ori-reaper-runner.lua" ori-reaper-runner.lua` + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := NewCatalogWithKeyboardConfig(path)
	actions, err := catalog.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != len(BuiltinActions())+1 || actions[len(actions)-1].Label != "ori-reaper-runner.lua" {
		t.Fatalf("catalog = %+v", actions)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(content) {
		t.Fatal("catalog read changed reaper-kb.ini")
	}
}

func TestValidRawCommandIDAcceptsOnlyDocumentedGrammar(t *testing.T) {
	valid := []string{"1007", "0", "_RSdeadBEEF"}
	invalid := []string{"", "-1", "+1007", "_RS", "RSdeadBEEF", "_RSface_feed", "1007/TRANSPORT", "1007?x=1", "12345678901"}
	for _, id := range valid {
		if !ValidRawCommandID(id) {
			t.Errorf("expected valid raw id %q", id)
		}
	}
	for _, id := range invalid {
		if ValidRawCommandID(id) {
			t.Errorf("expected invalid raw id %q", id)
		}
	}
}
