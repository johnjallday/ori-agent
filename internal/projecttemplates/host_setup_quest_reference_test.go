package projecttemplates_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/hostquests"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// FR 17: host quests and built-in templates reference each other exactly. A
// host quest must target a built-in template that names it back, and no
// built-in may name a quest the host catalog does not compile.
func TestHostSetupQuestsAndBuiltinTemplatesReferenceEachOther(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	templates, err := projecttemplates.ListLibrary(libDir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	builtins := make(map[string]projecttemplates.Template)
	for _, template := range templates {
		if template.Builtin {
			builtins[template.ID] = template
		}
	}

	quests := hostquests.All()
	if len(quests) == 0 {
		t.Fatal("no host setup quests are compiled in")
	}
	hostQuestIDs := make(map[string]bool, len(quests))
	for _, quest := range quests {
		hostQuestIDs[quest.ID] = true
		template, ok := builtins[quest.ExpectedBlueprintID]
		if !ok {
			t.Errorf("host quest %q targets %q, which is not a built-in template", quest.ID, quest.ExpectedBlueprintID)
			continue
		}
		if template.SetupQuestID != quest.ID {
			t.Errorf("built-in template %q names setup_quest %q, want %q", template.ID, template.SetupQuestID, quest.ID)
		}
	}
	for id, template := range builtins {
		if template.SetupQuestID != "" && !hostQuestIDs[template.SetupQuestID] {
			t.Errorf("built-in template %q names setup_quest %q, which the host catalog does not compile", id, template.SetupQuestID)
		}
	}

	emailOps, ok := builtins["email-ops"]
	if !ok || emailOps.SetupQuestID != hostquests.EmailOpsSetupQuestID || emailOps.BuiltinVersion < 5 {
		t.Fatalf("email-ops must reference the host quest and bump builtin_version: %+v", emailOps)
	}
	if emailOps.UserSetupQuest != nil || emailOps.UserSetupQuestError != "" {
		t.Fatalf("a built-in setup_quest reference must not become a user quest: %+v", emailOps)
	}
}

// OQ4: the bump refreshes an existing install's library manifest in place.
func TestEmailOpsBuiltinVersionBumpRefreshesExistingLibraryCopy(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(libDir, "email-ops", projecttemplates.ManifestFileName)
	writeStaleEmailOpsManifest(t, manifest)
	stale, err := projecttemplates.FindLibraryTemplate(libDir, "email-ops")
	if err != nil || stale.SetupQuestID != "" {
		t.Fatalf("stale fixture = %+v, err = %v", stale, err)
	}
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatal(err)
	}
	fresh, err := projecttemplates.FindLibraryTemplate(libDir, "email-ops")
	if err != nil || fresh.SetupQuestID != hostquests.EmailOpsSetupQuestID {
		t.Fatalf("refreshed template = %+v, err = %v", fresh, err)
	}
}

// writeStaleEmailOpsManifest rewrites the library copy as the previous shipped
// version: builtin_version 4 and no setup_quest reference.
func writeStaleEmailOpsManifest(t *testing.T, manifest string) {
	t.Helper()
	data, err := os.ReadFile(manifest) // #nosec G304 -- manifest lives under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(string(data), `"builtin_version": 5,`, `"builtin_version": 4,`, 1)
	stale = strings.Replace(stale, "\n  \"setup_quest\": \"email_ops_setup\",", "", 1)
	if stale == string(data) || strings.Contains(stale, "setup_quest") {
		t.Fatal("could not build the stale email-ops manifest fixture")
	}
	if err := os.WriteFile(manifest, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
}
