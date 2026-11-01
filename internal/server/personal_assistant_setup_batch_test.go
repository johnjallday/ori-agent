package server

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func approved(id, name string) approvedFile {
	return approvedFile{WorkspaceID: "janitor-ws", RootID: "root-generation-1", ActionID: id, Name: name, CompletedAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
}

func learningTexts(learnings []setupBatchLearning) []string {
	var texts []string
	for _, learning := range learnings {
		texts = append(texts, learning.Proposal.Text)
	}
	return texts
}

func TestNameRuns_OnlyCapitalisedLettersThatAreNotGenericWords(t *testing.T) {
	for base, want := range map[string]string{
		"Acme_Invoice_2026-03":                    "Acme",
		"Dana_Lee_Contract_signed":                "Dana Lee",
		"Dana Lee - Proposal v2":                  "Dana Lee",
		"invoice-acme-march":                      "",
		"Chase Statement January balance 4521.88": "Chase",
		"IMG_4521":                         "",
		"Screenshot 2026-11-02 at 9.41.22": "",
		"Mix Master Final":                 "",
		"AB_Cd":                            "",
		"Sam_Ortiz_Records_Q3_Budget":      "Sam Ortiz Records",
		"Anna_Maria_Rossi_Bianchi_Verdi":   "Anna Maria Rossi|Bianchi Verdi",
		"Müller_Café_Rechnung":             "Müller Café Rechnung",
		"":                                 "",
	} {
		if got := strings.Join(nameRuns(base), "|"); got != want {
			t.Errorf("nameRuns(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestRecurringNames_NeedTwoDifferentFilesAndKeepNoAmountOrAddress(t *testing.T) {
	files := []approvedFile{
		approved("a1", "Chase_Statement_2026-01_balance-4521.88"),
		approved("a2", "Chase_Statement_2026-02_balance-3102.10"),
		approved("a3", "Dana_Lee_Contract_signed"),
		approved("a4", "Dana Lee - Proposal v2"),
		approved("a5", "Zenith_Report_12_Main_Street"),
		approved("a6", "Acme_Acme_Acme"), // one file saying it three times is one file
	}
	learnings := setupBatchLearnings(files)
	texts := learningTexts(learnings)
	want := []string{
		"You deal with Chase (seen in 2 files you filed).",
		"You deal with Dana Lee (seen in 2 files you filed).",
	}
	if strings.Join(texts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("learnings = %q, want %q", texts, want)
	}
	// Nothing but the names themselves was kept: no amount, date, street or
	// extension, and no file name at all in the evidence.
	var shown []string
	for _, learning := range learnings {
		shown = append(shown, learning.Proposal.Text, learning.Proposal.SubjectID, learning.Proposal.Value)
		for _, evidence := range learning.Proposal.Evidence {
			shown = append(shown, evidence.SourceID, evidence.Summary)
		}
	}
	dump := strings.Join(shown, "\n")
	for _, leaked := range []string{"4521", "3102", "88", "2026", "Main", "Street", ".pdf", "balance", "Statement", "signed"} {
		if strings.Contains(dump, leaked) {
			t.Errorf("%q reached a learning:\n%s", leaked, dump)
		}
	}
	for _, learning := range learnings {
		proposal := learning.Proposal
		if proposal.SourceKind != personalassistant.SetupBatchSourceKind || proposal.Category != "people" || len(proposal.Evidence) != 2 {
			t.Errorf("proposal = %+v", proposal)
		}
		for _, evidence := range proposal.Evidence {
			if evidence.SourceKind != personalassistant.SetupBatchSourceKind || !learning.Supports[evidence.SourceID] {
				t.Errorf("evidence %+v is not a support", evidence)
			}
			if workspaceID, actionID, ok := personalassistant.SetupBatchEvidenceParts(evidence.SourceID); !ok || workspaceID != "janitor-ws" || actionID == "" {
				t.Errorf("evidence id %q does not name its workspace", evidence.SourceID)
			}
			if !strings.HasPrefix(evidence.Summary, "User-approved filing of a file whose name includes ") {
				t.Errorf("summary = %q", evidence.Summary)
			}
		}
	}
	// Chase recurs once: one file alone proposes nothing.
	if got := setupBatchLearnings(files[:1]); len(got) != 0 {
		t.Fatalf("one file proposed %v", learningTexts(got))
	}
}

func TestRecurringNames_AreBoundedStableAndKeepEachWorkspaceApart(t *testing.T) {
	var files []approvedFile
	for i := range 8 {
		name := fmt.Sprintf("Company%c", 'A'+i)
		files = append(files, approved(fmt.Sprintf("x%d-1", i), name+"_one"), approved(fmt.Sprintf("x%d-2", i), name+"_two"))
	}
	learnings := setupBatchLearnings(files)
	if len(learnings) != maxSetupBatchLearnings {
		t.Fatalf("one pass proposed %d, want at most %d", len(learnings), maxSetupBatchLearnings)
	}
	again := setupBatchLearnings(files)
	if strings.Join(learningTexts(learnings), "|") != strings.Join(learningTexts(again), "|") {
		t.Fatal("the same files gave a different order")
	}

	// A name that recurs only across two Janitor workspaces is in neither: each
	// journal is its own source, and its evidence names its own workspace.
	other := approved("y1", "Globex_Report")
	other.WorkspaceID = "other-ws"
	across := setupBatchLearnings([]approvedFile{approved("y0", "Globex_Report"), other})
	if len(across) != 0 {
		t.Fatalf("a name across workspaces proposed %v", learningTexts(across))
	}
}

// A name is the whole of a run in one file and may be the front of a longer run in
// another: the demo's Dana_Lee_Contract and Dana_Lee_Proposal are one person.
func TestRecurringNames_AWholeNameMatchesTheFrontOfALongerOne(t *testing.T) {
	learned := func(names ...string) []string {
		var files []approvedFile
		for index, name := range names {
			files = append(files, approved(fmt.Sprintf("n%d", index), name))
		}
		return learningTexts(setupBatchLearnings(files))
	}
	for _, scenario := range []struct {
		name  string
		files []string
		want  string
	}{
		{"a whole run and the front of a longer one", []string{"Dana_Lee_Contract", "Dana_Lee_Alpha_Notes"}, "You deal with Dana Lee (seen in 2 files you filed)."},
		{"stopwords end the run", []string{"Dana_Lee_Contract", "Dana_Lee_Proposal"}, "You deal with Dana Lee (seen in 2 files you filed)."},
		{"a whole run beside two longer ones", []string{"Dana_Lee", "Dana_Lee_Alpha", "Dana_Lee_Beta"}, "You deal with Dana Lee (seen in 3 files you filed)."},
		{"a shared first name is not a person", []string{"Anna_Maria_Rossi", "Anna_Bianchi"}, ""},
		{"only longer runs that differ", []string{"Dana_Lee_Alpha", "Dana_Lee_Beta"}, ""},
		{"the longer name covers the shorter", []string{"Dana_Lee", "Dana_Lee", "Dana_Lee_Alpha"}, "You deal with Dana Lee (seen in 3 files you filed)."},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := strings.Join(learned(scenario.files...), "|")
			if got != scenario.want {
				t.Fatalf("learned %q from %v, want %q", got, scenario.files, scenario.want)
			}
		})
	}
}

func TestRefreshedSetupText_OnlyOwnWordingAboutTheSameNameWithAHigherCount(t *testing.T) {
	const two = "You deal with Chase (seen in 2 files you filed)."
	for name, scenario := range map[string]struct {
		current, proposed string
		want              bool
	}{
		"a higher count":      {two, "You deal with Chase (seen in 3 files you filed).", true},
		"the same count":      {two, two, false},
		"a lower count":       {"You deal with Chase (seen in 3 files you filed).", two, false},
		"another name":        {two, "You deal with Acme (seen in 3 files you filed).", false},
		"a user's own words":  {"Chase is my bank.", "You deal with Chase (seen in 3 files you filed).", false},
		"a user edit of ours": {"You deal with Chase, my bank (seen in 2 files you filed).", "You deal with Chase (seen in 3 files you filed).", false},
		"the case of a name":  {two, "You deal with CHASE (seen in 4 files you filed).", true},
		"not a number":        {two, "You deal with Chase (seen in many files you filed).", false},
	} {
		if got := refreshedSetupText(scenario.current, scenario.proposed); got != scenario.want {
			t.Errorf("%s: refreshedSetupText = %v, want %v", name, got, scenario.want)
		}
	}
}

// Moves are applied one at a time, so a name is first proposed from two files.
// The waiting learning's count follows the later ones; an answered learning is
// never revised, and neither is one whose wording the user changed.
func TestSetupBatchProducer_ARecurringNameKeepsItsCountCurrentWhileItWaits(t *testing.T) {
	f := newDraftServerFixture(t)
	learning := f.builder.personalAssistantLearning
	ctx := t.Context()
	producer := setupBatchProducer{reader: &janitorKnowledgeReader{}, learning: learning}
	propose := func(count int) personalassistant.KnowledgeItem {
		t.Helper()
		var files []approvedFile
		for index := range count {
			files = append(files, approved(fmt.Sprintf("c%d", index), fmt.Sprintf("Chase_Statement_Month%c", 'a'+index)))
		}
		learnings := setupBatchLearnings(files)
		if len(learnings) != 1 {
			t.Fatalf("learnings = %v", learningTexts(learnings))
		}
		item, created, err := learning.Propose(ctx, "local", learnings[0].Proposal)
		if err != nil {
			t.Fatal(err)
		}
		if !created {
			item = producer.refreshCount(ctx, "local", item, learnings[0].Proposal.Text)
		}
		return item
	}
	text := func() string {
		t.Helper()
		view, err := personalassistant.NewSetupLearningService(learning).List(ctx, "local")
		if err != nil || len(view.Items) != 1 {
			t.Fatalf("view = %+v, %v", view, err)
		}
		return view.Items[0].Text
	}

	first := propose(2)
	if got := text(); got != "You deal with Chase (seen in 2 files you filed)." {
		t.Fatalf("first = %q", got)
	}
	if again := propose(2); again.Version != first.Version {
		t.Fatalf("the same count revised the learning: %d → %d", first.Version, again.Version)
	}
	refreshed := propose(3)
	if got := text(); got != "You deal with Chase (seen in 3 files you filed)." || refreshed.Version <= first.Version {
		t.Fatalf("after a third file: %q (version %d)", got, refreshed.Version)
	}
	// The original two pieces of evidence still stand.
	view, _ := personalassistant.NewSetupLearningService(learning).List(ctx, "local")
	if len(view.Items[0].Evidence) != 2 {
		t.Fatalf("evidence = %v", view.Items[0].Evidence)
	}
	// Once answered it is left as the user answered it.
	if _, err := learning.SuppressCandidate(ctx, "local", refreshed.ID, refreshed.Version, personalassistant.KnowledgeRejected); err != nil {
		t.Fatal(err)
	}
	if _, _, err := learning.Propose(ctx, "local", setupBatchLearnings([]approvedFile{approved("d0", "Chase_one"), approved("d1", "Chase_two"), approved("d2", "Chase_three"), approved("d3", "Chase_four")})[0].Proposal); !errors.Is(err, personalassistant.ErrKnowledgeSuppressed) {
		t.Fatalf("a rejected learning came back: %v", err)
	}
}

func TestRecurringNames_NeverCarryASecretLikeName(t *testing.T) {
	const token = "AbCdEfGhIjKlMnOpQrStUvWxYz"
	files := []approvedFile{
		approved("s1", "Ghp_"+token+"_one"), approved("s2", "Ghp_"+token+"_two"),
		approved("s3", "Sk_Ant_Api_"+token), approved("s4", "Sk_Ant_Api_"+token),
	}
	for _, text := range learningTexts(setupBatchLearnings(files)) {
		if strings.Contains(text, token) {
			t.Errorf("a secret-looking name reached %q", text)
		}
	}
}

// The producer reads the same approved, applied, not-undone moves the filing
// preferences do, through the one predicate.
func TestJanitorSupportAction_IsTheOneGateForEveryKnowledgeReader(t *testing.T) {
	at := time.Now().UTC()
	status := janitorKnowledgeTestStatus(at)
	good := janitorKnowledgeTestAction("good", at)
	now := at.Add(time.Minute)
	if !janitorSupportAction(good, "janitor-ws", "local", status.Settings, now) {
		t.Fatal("an approved applied move was refused")
	}
	for name, edit := range map[string]func(*filejanitor.FileAction){
		"undone":         func(a *filejanitor.FileAction) { a.Undo = filejanitor.UndoDone },
		"failed":         func(a *filejanitor.FileAction) { a.Result = filejanitor.ResultFailed },
		"another root":   func(a *filejanitor.FileAction) { a.RootID = "previous-root" },
		"not approved":   func(a *filejanitor.FileAction) { a.ApprovedAt = time.Time{} },
		"someone else's": func(a *filejanitor.FileAction) { a.ApprovedBy = "stranger" },
		"a trashing":     func(a *filejanitor.FileAction) { a.Operation = filejanitor.OperationTrash },
		"too old":        func(a *filejanitor.FileAction) { a.CompletedAt = at.Add(-100 * 24 * time.Hour) },
	} {
		action := good
		edit(&action)
		if janitorSupportAction(action, "janitor-ws", "local", status.Settings, now) {
			t.Errorf("%s move was accepted", name)
		}
	}
}
