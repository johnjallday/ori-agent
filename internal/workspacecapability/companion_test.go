package workspacecapability

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestFileJanitorCompanion_IsDeclaredReadOnlyAndOptional pins the companion
// contract (FR-35-FR-42).
func TestFileJanitorCompanion_IsDeclaredReadOnlyAndOptional(t *testing.T) {
	def := FileJanitorDefinition()
	companion := def.Companion
	if companion == nil {
		t.Fatal("File Janitor declares no companion")
	}

	if !companion.ReadOnly {
		t.Fatal("the Curator must be declared read-only")
	}
	if !companion.IncludedByBlueprint {
		t.Fatal("the blueprint includes a Curator by default (FR-35)")
	}
	if !companion.OfferedOnInPlaceInstall {
		t.Fatal("an in-place install offers the Curator separately (FR-36)")
	}
	// Folder-neutral default: the Downloads preset overrides it with its own
	// name, everything else gets this one (FR-40).
	if companion.DefaultDisplayName != "File Curator" {
		t.Fatalf("default companion name = %q, want File Curator", companion.DefaultDisplayName)
	}
}

// TestCompanionDescriptor_GrantsNoTools is the FR-42 structural guarantee.
//
// The companion's read-only allowlist is compiled into the binding logic
// (downloadsjanitor.JanitorReadTools), deliberately NOT described here. If a
// descriptor could name tools, a capability definition — and eventually a
// manifest that selects one — would become a place where a grant is written,
// which is exactly the authority this data must never carry.
func TestCompanionDescriptor_GrantsNoTools(t *testing.T) {
	typ := reflect.TypeOf(CompanionDescriptor{})
	for i := range typ.NumField() {
		lowered := strings.ToLower(typ.Field(i).Name)
		for _, banned := range []string{"tool", "allow", "permission", "grant", "scope", "capabilit"} {
			if strings.Contains(lowered, banned) {
				t.Errorf("CompanionDescriptor.%s looks like a tool grant; the allowlist must stay compiled (FR-42)", typ.Field(i).Name)
			}
		}
	}
}

// TestFileJanitorSkill_ExplainsWhatTheCuratorCannotDo checks the shipped skill
// actually states its limits (FR-41, FR-42).
//
// The skill is the Curator's own instructions, so a Curator that believes it
// can approve or file things is a Curator that will tell the user it did. The
// wording may evolve; the claims it must make should not silently disappear.
func TestFileJanitorSkill_ExplainsWhatTheCuratorCannotDo(t *testing.T) {
	path := filepath.Join("..", "..", ".agents", "skills", "file-janitor", "SKILL.md")
	data, err := os.ReadFile(path) // #nosec G304 -- fixed repository path
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	// Collapse whitespace before matching. The skill is prose wrapped at a
	// column, so a phrase can straddle a newline; asserting on raw text would
	// make this test fail on a reflow that changed nothing it cares about.
	body := strings.Join(strings.Fields(strings.ToLower(string(data))), " ")

	// It must disclaim every operation the service alone may perform.
	for _, claim := range []string{"approve", "trash", "restore", "undo", "scan", "revoke"} {
		if !strings.Contains(body, claim) {
			t.Errorf("the skill never mentions %q; the Curator must know it cannot do it", claim)
		}
	}
	if !strings.Contains(body, "cannot") {
		t.Error("the skill never states a limit")
	}

	// Metadata-first privacy posture.
	if !strings.Contains(body, "do not read file contents") {
		t.Error("the skill does not state the metadata-only default")
	}

	// Prompt-injection boundary: filenames are attacker-controlled text.
	if !strings.Contains(body, "never instructions") && !strings.Contains(body, "not instructions") {
		t.Error("the skill does not tell the Curator to treat filenames as data")
	}
	if !strings.Contains(body, "ignore previous instructions") {
		t.Error("the skill gives no concrete example of a hostile filename")
	}

	// It must not promise the user an action it cannot take.
	for _, forbidden := range []string{"i will file", "i have filed", "i moved"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the skill contains a claim the Curator cannot honour: %q", forbidden)
		}
	}
}
