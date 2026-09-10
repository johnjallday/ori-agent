package projecttemplates

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func userQuestTemplateFixture(t *testing.T) (string, Template) {
	t.Helper()
	library := t.TempDir()
	template, err := ImportFolder(library, filepath.Join("testdata", "user-setup-quest-eligible"), "Eligible local project")
	if err != nil {
		t.Fatal(err)
	}
	if !template.UserSetupQuestEligibility.Eligible {
		t.Fatalf("fixture is not eligible: %+v", template.UserSetupQuestEligibility)
	}
	return library, template
}

func TestUserSetupQuestSaveReloadAndRemove(t *testing.T) {
	library, template := userQuestTemplateFixture(t)
	draft := DefaultUserSetupQuestDraft()
	draft.Title = "  My local setup  "
	draft.Steps[0].Title = "  Verify the integration  "

	saved, err := UpdateUserSetupQuest(library, template.ID, UserSetupQuestEdit{
		ExpectedRevision: template.UserSetupQuestRevision, Draft: &draft,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.UserSetupQuest == nil || saved.UserSetupQuest.Declaration.Title != "My local setup" ||
		saved.UserSetupQuest.Declaration.Steps[0].Title != "Verify the integration" {
		t.Fatalf("saved declaration = %+v", saved.UserSetupQuest)
	}
	if saved.UserSetupQuest.Declaration.OwnerPluginID != "" || saved.PluginOwner != nil || saved.SetupQuestID != "" {
		t.Fatalf("user quest fabricated plugin ownership: template=%+v quest=%+v", saved.PluginOwner, saved.UserSetupQuest.Declaration)
	}
	if saved.UserSetupQuest.Declaration.ExpectedBlueprintID != template.ID ||
		saved.UserSetupQuest.Declaration.ExpectedAssistantProgramID != template.AssistantProgram.ID {
		t.Fatalf("host target references were not derived: %+v", saved.UserSetupQuest.Declaration)
	}
	firstAttachment := saved.UserSetupQuest.AttachmentID
	firstID := saved.UserSetupQuest.Declaration.ID
	firstRevision := saved.UserSetupQuestRevision

	reloaded, err := FindLibraryTemplate(library, template.ID)
	if err != nil || reloaded.UserSetupQuestRevision != firstRevision || reloaded.UserSetupQuest.AttachmentID != firstAttachment {
		t.Fatalf("reload = %+v err=%v", reloaded.UserSetupQuest, err)
	}
	editedDraft := UserSetupQuestDraftFor(reloaded.UserSetupQuest)
	editedDraft.Description = "Edited before the first run."
	edited, err := UpdateUserSetupQuest(library, template.ID, UserSetupQuestEdit{
		ExpectedRevision: firstRevision, Draft: &editedDraft,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if edited.UserSetupQuest.AttachmentID != firstAttachment || edited.UserSetupQuest.Declaration.ID != firstID ||
		edited.UserSetupQuestRevision == firstRevision {
		t.Fatalf("edit replaced stable identity or revision did not change: %+v", edited.UserSetupQuest)
	}

	removed, err := UpdateUserSetupQuest(library, template.ID, UserSetupQuestEdit{
		ExpectedRevision: edited.UserSetupQuestRevision, Remove: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed.UserSetupQuest != nil || removed.UserSetupQuestRevision != UserSetupQuestRevision(nil) {
		t.Fatalf("explicit null removal did not restore absent state: %+v", removed)
	}
}

func TestUserSetupQuestSaveIsOptimisticAtomicAndNoOpStable(t *testing.T) {
	library, template := userQuestTemplateFixture(t)
	draft := DefaultUserSetupQuestDraft()
	saved, err := UpdateUserSetupQuest(library, template.ID, UserSetupQuestEdit{ExpectedRevision: template.UserSetupQuestRevision, Draft: &draft}, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(saved.Path, ManifestFileName)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateUserSetupQuest(library, template.ID, UserSetupQuestEdit{ExpectedRevision: template.UserSetupQuestRevision, Draft: &draft}, nil); !errors.Is(err, ErrUserSetupQuestStale) {
		t.Fatalf("stale save error = %v", err)
	}
	afterStale, _ := os.ReadFile(manifestPath)
	if string(afterStale) != string(before) {
		t.Fatal("stale save partially changed template.json")
	}

	currentDraft := UserSetupQuestDraftFor(saved.UserSetupQuest)
	noOp, err := UpdateUserSetupQuest(library, template.ID, UserSetupQuestEdit{ExpectedRevision: saved.UserSetupQuestRevision, Draft: &currentDraft}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if noOp.UserSetupQuestRevision != saved.UserSetupQuestRevision || noOp.UserSetupQuest.AttachmentID != saved.UserSetupQuest.AttachmentID {
		t.Fatalf("normalized no-op changed identity: before=%+v after=%+v", saved.UserSetupQuest, noOp.UserSetupQuest)
	}
	matches, _ := filepath.Glob(filepath.Join(saved.Path, ".template-manifest-*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("atomic writer left temporary files: %v", matches)
	}
}

func TestUserSetupQuestConcurrentSavesSerializeAndGuardFailureRollsBack(t *testing.T) {
	library, template := userQuestTemplateFixture(t)
	first := DefaultUserSetupQuestDraft()
	first.Title = "First writer"
	second := DefaultUserSetupQuestDraft()
	second.Title = "Second writer"

	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan error, 2)
	for _, draft := range []*UserSetupQuestDraft{&first, &second} {
		go func(value *UserSetupQuestDraft) {
			defer wait.Done()
			_, err := UpdateUserSetupQuest(library, template.ID, UserSetupQuestEdit{
				ExpectedRevision: template.UserSetupQuestRevision, Draft: value,
			}, nil)
			results <- err
		}(draft)
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrUserSetupQuestStale):
			conflicts++
		default:
			t.Fatalf("concurrent save error=%v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}

	current, err := FindLibraryTemplate(library, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(current.Path, ManifestFileName))
	updatedDraft := UserSetupQuestDraftFor(current.UserSetupQuest)
	updatedDraft.Description = "must not publish"
	guardFailure := errors.New("binding store unavailable")
	_, err = UpdateUserSetupQuest(library, current.ID, UserSetupQuestEdit{
		ExpectedRevision: current.UserSetupQuestRevision, Draft: &updatedDraft,
	}, func(Template, Template) error { return guardFailure })
	if !errors.Is(err, guardFailure) {
		t.Fatalf("guard error=%v", err)
	}
	after, _ := os.ReadFile(filepath.Join(current.Path, ManifestFileName))
	if string(after) != string(before) {
		t.Fatal("guard failure partially published template.json")
	}
}

func TestUserSetupQuestRuntimeAndManifestMutationsShareOneLibraryLock(t *testing.T) {
	library, template := userQuestTemplateFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- WithLibraryMutationLock(library, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	updateDone := make(chan error, 1)
	go func() {
		draft := DefaultUserSetupQuestDraft()
		_, err := UpdateUserSetupQuest(library, template.ID, UserSetupQuestEdit{
			ExpectedRevision: template.UserSetupQuestRevision, Draft: &draft,
		}, nil)
		updateDone <- err
	}()
	select {
	case err := <-updateDone:
		t.Fatalf("manifest mutation escaped runtime lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	if err := <-updateDone; err != nil {
		t.Fatal(err)
	}
}

func TestUserSetupQuestStrictParserRejectsExecutableUnknownAndFixedStepDrift(t *testing.T) {
	_, template := userQuestTemplateFixture(t)
	draft := DefaultUserSetupQuestDraft()
	quest, err := NewUserSetupQuest(template, nil, draft)
	if err != nil {
		t.Fatal(err)
	}
	valid, _ := json.Marshal(quest)
	for name, mutate := range map[string]func(map[string]any){
		"unknown route":  func(raw map[string]any) { raw["route"] = "/run" },
		"copy URL":       func(raw map[string]any) { raw["description"] = "Open https://example.invalid/run" },
		"foreign source": func(raw map[string]any) { raw["source"] = "plugin" },
		"owner plugin":   func(raw map[string]any) { raw["owner_plugin_id"] = "reaper-plugin" },
		"missing step": func(raw map[string]any) {
			raw["steps"] = raw["steps"].([]any)[:4]
		},
		"reordered steps": func(raw map[string]any) {
			steps := raw["steps"].([]any)
			steps[0], steps[1] = steps[1], steps[0]
		},
		"executable step": func(raw map[string]any) {
			raw["steps"].([]any)[0].(map[string]any)["command"] = "run"
		},
	} {
		t.Run(name, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal(valid, &raw); err != nil {
				t.Fatal(err)
			}
			mutate(raw)
			encoded, _ := json.Marshal(raw)
			if _, err := ParseUserSetupQuest(encoded); !errors.Is(err, ErrInvalidUserSetupQuest) {
				t.Fatalf("error = %v", err)
			}
		})
	}

	oversized := append([]byte(`{"attachment_id":"uqatt_0123456789abcdef01234567","padding":"`), []byte(strings.Repeat("x", maxUserSetupQuestBytes))...)
	oversized = append(oversized, []byte(`"}`)...)
	if _, err := ParseUserSetupQuest(oversized); !errors.Is(err, ErrInvalidUserSetupQuest) {
		t.Fatalf("oversized error = %v", err)
	}

	badDraft := draft
	badDraft.Steps = append([]UserSetupQuestStepDraft(nil), draft.Steps...)
	badDraft.Steps[0].Kind = specialist.SetupStepSummary
	if _, err := NewUserSetupQuest(template, nil, badDraft); !errors.Is(err, ErrInvalidUserSetupQuest) {
		t.Fatalf("bad fixed order error = %v", err)
	}
}

func TestUserSetupQuestEligibilityReportsEachMissingOwner(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Template)
		code string
	}{
		{"connection", func(template *Template) { template.ProjectConnection = nil }, "project_connection_required"},
		{"program", func(template *Template) { template.AssistantProgram = nil }, "assistant_program_required"},
		{"runtime", func(template *Template) { template.RuntimeRequirements = nil }, "file_only_runtime_required"},
		{"wizard", func(template *Template) { template.SetupWizard = nil }, "runtime_mode_wizard_required"},
		{"skeleton", func(template *Template) {
			template.ProjectConnection.SupportedModes = []ProjectConnectionMode{ProjectConnectionNewProject}
			template.HasSkeleton = false
		}, "constructible_project_required"},
	}
	_, base := userQuestTemplateFixture(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			template := base
			tc.edit(&template)
			result := EvaluateUserSetupQuestEligibility(template)
			if result.Eligible || !eligibilityHas(result, tc.code) {
				t.Fatalf("eligibility = %+v, want %q", result, tc.code)
			}
		})
	}
}

func TestUserSetupQuestDuplicateAndImportRemapAllStableIdentities(t *testing.T) {
	library, template := userQuestTemplateFixture(t)
	draft := DefaultUserSetupQuestDraft()
	template, err := UpdateUserSetupQuest(library, template.ID, UserSetupQuestEdit{
		ExpectedRevision: template.UserSetupQuestRevision, Draft: &draft,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := Duplicate(library, template.ID, "Fresh copy")
	if err != nil {
		t.Fatal(err)
	}
	assertRemappedUserQuest(t, template, duplicate)

	exported := filepath.Join(t.TempDir(), template.ID)
	if err := copyFolderVerbatim(template.Path, exported); err != nil {
		t.Fatal(err)
	}
	importedLibrary := t.TempDir()
	imported, err := ImportFolder(importedLibrary, exported, "Imported fresh copy")
	if err != nil {
		t.Fatal(err)
	}
	assertRemappedUserQuest(t, template, imported)

	var raw map[string]any
	manifestPath := filepath.Join(exported, ManifestFileName)
	data, _ := os.ReadFile(manifestPath)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["user_setup_quest"].(map[string]any)["command"] = "run"
	data, _ = json.Marshal(raw)
	if err := os.WriteFile(manifestPath, data, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportFolder(t.TempDir(), exported, "Malicious import"); !errors.Is(err, ErrInvalidUserSetupQuest) {
		t.Fatalf("malformed import error=%v", err)
	}
}

func assertRemappedUserQuest(t *testing.T, source, target Template) {
	t.Helper()
	if target.UserSetupQuest == nil || target.UserSetupQuestError != "" {
		t.Fatalf("target quest=%+v error=%q", target.UserSetupQuest, target.UserSetupQuestError)
	}
	if target.UserSetupQuest.AttachmentID == source.UserSetupQuest.AttachmentID ||
		target.UserSetupQuest.Declaration.ID == source.UserSetupQuest.Declaration.ID ||
		target.UserSetupQuest.Declaration.ExpectedBlueprintID != target.ID ||
		target.UserSetupQuest.Declaration.ExpectedAssistantProgramID != target.AssistantProgram.ID {
		t.Fatalf("identities were not remapped: source=%+v target=%+v", source.UserSetupQuest, target.UserSetupQuest)
	}
	for index, step := range target.UserSetupQuest.Declaration.Steps {
		if step.ID == source.UserSetupQuest.Declaration.Steps[index].ID {
			t.Fatalf("step %d identity was copied: %q", index, step.ID)
		}
		if step.Kind != source.UserSetupQuest.Declaration.Steps[index].Kind || step.Title != source.UserSetupQuest.Declaration.Steps[index].Title {
			t.Fatalf("step %d copy changed editable content: %+v", index, step)
		}
	}
}

func TestUserSetupQuestExecutionDigestExcludesDisplayOnlyMetadata(t *testing.T) {
	_, template := userQuestTemplateFixture(t)
	draft := DefaultUserSetupQuestDraft()
	quest, err := NewUserSetupQuest(template, nil, draft)
	if err != nil {
		t.Fatal(err)
	}
	template.UserSetupQuest = quest
	before := UserSetupQuestExecutionDigest(template)
	display := template
	display.Name, display.Description, display.Icon, display.Tags = "Renamed", "Changed", "🎵", []string{"new"}
	if got := UserSetupQuestExecutionDigest(display); got != before {
		t.Fatalf("display-only metadata changed execution digest: %s != %s", got, before)
	}
	protected := template
	protected.AssistantProgram = cloneAssistantProgramForTest(template.AssistantProgram)
	protected.AssistantProgram.StationName = "Changed Home"
	if got := UserSetupQuestExecutionDigest(protected); got == before {
		t.Fatal("Assistant Program change did not change execution digest")
	}
}

func cloneAssistantProgramForTest(source *workspace.AssistantProgramDeclaration) *workspace.AssistantProgramDeclaration {
	return workspace.CloneAssistantProgramDeclaration(source)
}

func eligibilityHas(value UserSetupQuestEligibility, code string) bool {
	for _, item := range value.Missing {
		if item.Code == code {
			return true
		}
	}
	return false
}
