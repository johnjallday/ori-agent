package server

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The approved_files producer (first-week FR17). After the user approves files
// for filing, a company or person whose name recurs in at least two of those
// file names becomes one reviewable learning: "You deal with Acme (seen in 2
// files you filed)." It reads the File Janitor action journal's file names and
// nothing else: never a file's contents, an amount or an address, and a name is
// a run of capitalised letters only, so digits never reach a learning.

// approvedFile is what the producer keeps of one approved move: where it was
// journaled, which action, and the file's base name without its extension.
type approvedFile struct {
	WorkspaceID string
	RootID      string
	ActionID    string
	Name        string
	CompletedAt time.Time
}

// ReadApprovedFiles returns the approved, applied, not-undone moves in every
// Janitor workspace the assistant is allowed to read, through the same gate and
// the same predicate as the filing-preference suggestions.
func (r *janitorKnowledgeReader) ReadApprovedFiles(ctx context.Context, userID string) ([]approvedFile, error) {
	at := time.Now().UTC()
	if r != nil && r.now != nil {
		at = r.now().UTC()
	}
	var files []approvedFile
	err := r.visitJournals(ctx, userID, false, func(workspaceID, ownerID string, settings filejanitor.JanitorSettings, actions []filejanitor.FileAction) {
		seen := make(map[string]bool, len(actions))
		for _, action := range actions {
			if seen[action.ID] || !janitorSupportAction(action, workspaceID, ownerID, settings, at) {
				continue
			}
			seen[action.ID] = true
			name := strings.TrimSuffix(path.Base(action.SourceName), path.Ext(action.SourceName))
			files = append(files, approvedFile{
				WorkspaceID: workspaceID, RootID: settings.RootID, ActionID: action.ID, Name: name, CompletedAt: action.CompletedAt,
			})
		}
	})
	return files, err
}

// setupBatchLearning is one proposal and every approved action that supports it.
type setupBatchLearning struct {
	Proposal personalassistant.KnowledgeProposal
	Supports map[string]bool
}

// maxSetupBatchLearnings bounds what one pass proposes.
const maxSetupBatchLearnings = 5

// maxNameWords and maxNameRunes bound one recurring name.
const (
	maxNameWords = 3
	maxNameRunes = 60
)

// nameStopwords are the words a file name is made of that are not anyone's name.
var nameStopwords = func() map[string]bool {
	words := `a an and the of for with from to in on at by or
		invoice invoices receipt receipts statement statements report reports scan scans scanned screenshot screenshots screen shot
		copy final draft drafts document documents doc docs image images img photo photos picture pictures pic file files untitled new old
		bank payment payments order orders contract contracts agreement agreements tax taxes form forms letter letters note notes meeting
		meetings presentation summary budget resume cv download downloads mix mixes master masters demo version page pages scanned dsc pdf
		jpg jpeg png gif heic mov mp3 mp4 wav zip txt csv xlsx docx
		january february march april may june july august september october november december
		proposal proposals quote quotes estimate offer plan plans schedule agenda minutes notice application certificate license permit
		passport ticket tickets transcript diploma street avenue road drive lane boulevard suite apartment
		jan feb mar apr jun jul aug sep sept oct nov dec
		monday tuesday wednesday thursday friday saturday sunday mon tue tues wed thu thur thurs fri sat sun
		q1 q2 q3 q4 annual monthly weekly daily total balance account accounts credit debit card cards loan insurance policy`
	set := map[string]bool{}
	for _, word := range strings.Fields(words) {
		set[word] = true
	}
	return set
}()

// maxNameWordRunes and maxNameCaseChanges keep a credential-looking token from
// being read as a name: "McDonald" changes case twice, "AbCdEfGhIjKl" many times.
const (
	maxNameWordRunes   = 24
	maxNameCaseChanges = 2
)

// plausibleNameWord: letters only (the caller guarantees it), at least three, the
// first upper case, not a stopword, and not a run of alternating case.
func plausibleNameWord(token string) bool {
	runes := []rune(token)
	if len(runes) < 3 || len(runes) > maxNameWordRunes || !unicode.IsUpper(runes[0]) || nameStopwords[strings.ToLower(token)] {
		return false
	}
	changes := 0
	for index := 1; index < len(runes); index++ {
		if unicode.IsUpper(runes[index]) != unicode.IsUpper(runes[index-1]) {
			changes++
		}
	}
	return changes <= maxNameCaseChanges
}

// nameRuns splits one file name into the runs of capitalised words that could be
// a company or a person. Words are runs of letters; two words are in the same
// run only when exactly one space, underscore or hyphen lies between them, so a
// digit, a dot, " - " or a doubled separator ends a run ("Acme 2026 Corp" is two
// names, not one). A word that is short, lower case, generic or token-like ends
// a run too. "Acme_Invoice_2026-03" gives "Acme"; "invoice-acme" gives nothing.
func nameRuns(base string) []string {
	var runs, run []string
	flush := func() {
		if len(run) > 0 {
			runs = append(runs, strings.Join(run, " "))
		}
		run = nil
	}
	gap := ""
	inWord, word := false, strings.Builder{}
	endWord := func() {
		if word.Len() == 0 {
			return
		}
		token := word.String()
		word.Reset()
		if !plausibleNameWord(token) {
			flush()
			return
		}
		if len(run) > 0 && gap != " " && gap != "_" && gap != "-" {
			flush()
		}
		run = append(run, token)
		if len(run) == maxNameWords {
			flush()
		}
	}
	for _, r := range base {
		if unicode.IsLetter(r) {
			if !inWord {
				inWord = true
			}
			word.WriteRune(r)
			continue
		}
		if inWord {
			endWord()
			inWord, gap = false, ""
		}
		gap += string(r)
	}
	endWord()
	flush()
	return runs
}

// recurringNames finds the names that appear in at least two different files. A
// name counts once per file however often it repeats there.
//
// A name must stand alone as a whole run of capitalised words in at least one
// file, and may be part of a longer run in the others: "Dana_Lee_Contract" and
// "Dana_Lee_Proposal" both teach "Dana Lee", though one file's run is "Dana Lee"
// and the other's "Dana Lee Proposal". Requiring a whole run somewhere is what
// keeps a shared first name from joining two people ("Anna Maria Rossi" and
// "Anna Bianchi" teach nothing). A name that is part of a longer one that covers
// the same files is dropped in its favour.
func recurringNames(files []approvedFile) []recurringName {
	type found struct {
		display  string
		complete bool // the whole of some file's run
		files    map[string]approvedFile
	}
	byKey := map[string]*found{}
	for _, file := range files {
		fileKey := file.WorkspaceID + ":" + file.ActionID
		for _, run := range nameRuns(file.Name) {
			words := strings.Fields(run)
			for start := range words {
				for end := start + 1; end <= len(words); end++ {
					display := strings.Join(words[start:end], " ")
					key := strings.ToLower(display)
					entry := byKey[key]
					if entry == nil {
						entry = &found{display: display, files: map[string]approvedFile{}}
						byKey[key] = entry
					}
					entry.files[fileKey] = file
					if start == 0 && end == len(words) {
						entry.complete = true
					}
				}
			}
		}
	}
	covered := func(shorter, longer string, byKey map[string]*found) bool {
		if shorter == longer || !strings.Contains(" "+longer+" ", " "+shorter+" ") {
			return false
		}
		for id := range byKey[shorter].files {
			if _, both := byKey[longer].files[id]; !both {
				return false
			}
		}
		return true
	}
	var names []recurringName
	for key, entry := range byKey {
		if len(entry.files) < 2 || !entry.complete || utf8.RuneCountInString(entry.display) > maxNameRunes ||
			sensitive.ContainsSecretLikeText(entry.display) {
			continue
		}
		inLonger := false
		for other, longer := range byKey {
			if len(longer.files) >= 2 && longer.complete && covered(key, other, byKey) {
				inLonger = true
				break
			}
		}
		if inLonger {
			continue
		}
		name := recurringName{Key: key, Display: entry.display}
		for _, file := range entry.files {
			name.Files = append(name.Files, file)
		}
		sort.Slice(name.Files, func(i, j int) bool { return name.Files[i].ActionID < name.Files[j].ActionID })
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i].Files) != len(names[j].Files) {
			return len(names[i].Files) > len(names[j].Files)
		}
		return names[i].Key < names[j].Key
	})
	if len(names) > maxSetupBatchLearnings {
		names = names[:maxSetupBatchLearnings]
	}
	return names
}

type recurringName struct {
	Key     string
	Display string
	Files   []approvedFile
}

// setupBatchLearnings turns the recurring names into proposals. A name is
// proposed for the Janitor workspace its files were filed in; one that recurs
// only across two workspaces is not, since each journal is its own source.
func setupBatchLearnings(files []approvedFile) []setupBatchLearning {
	byWorkspace := map[string][]approvedFile{}
	for _, file := range files {
		byWorkspace[file.WorkspaceID] = append(byWorkspace[file.WorkspaceID], file)
	}
	workspaceIDs := make([]string, 0, len(byWorkspace))
	for id := range byWorkspace {
		workspaceIDs = append(workspaceIDs, id)
	}
	sort.Strings(workspaceIDs)
	var learnings []setupBatchLearning
	for _, workspaceID := range workspaceIDs {
		group := byWorkspace[workspaceID]
		for _, name := range recurringNames(group) {
			text := fmt.Sprintf("You deal with %s (seen in %d files you filed).", name.Display, len(name.Files))
			if _, err := workspace.ValidateMemoryText(text); err != nil || sensitive.ContainsSecretLikeText(text) {
				continue
			}
			learning := setupBatchLearning{Supports: map[string]bool{}}
			learning.Proposal = personalassistant.KnowledgeProposal{
				SourceKind: personalassistant.SetupBatchSourceKind, ScopeID: workspaceID + ":" + group[0].RootID,
				SubjectID: name.Key, Predicate: "deals with", Value: name.Key, Category: "people", Text: text,
			}
			for index, file := range name.Files {
				learning.Supports[personalassistant.SetupBatchEvidenceID(file.WorkspaceID, file.ActionID)] = true
				if index < 2 {
					learning.Proposal.Evidence = append(learning.Proposal.Evidence, personalassistant.KnowledgeEvidence{
						SourceKind: personalassistant.SetupBatchSourceKind,
						SourceID:   personalassistant.SetupBatchEvidenceID(file.WorkspaceID, file.ActionID),
						ObservedAt: file.CompletedAt,
						Summary:    "User-approved filing of a file whose name includes " + name.Display,
					})
				}
			}
			learnings = append(learnings, learning)
		}
	}
	return learnings
}

// setupBatchProducer raises the learnings into the review queue. It proposes and
// nothing else: it never approves, and never touches a file.
type setupBatchProducer struct {
	reader   *janitorKnowledgeReader
	learning *personalassistant.KnowledgeLearningService
}

// Check proposes what the approved files teach. A learning the user already
// answered stays answered (suppressed), and a full pool simply stops the pass.
func (p setupBatchProducer) Check(ctx context.Context, userID string) ([]personalassistant.KnowledgeItem, error) {
	if p.reader == nil || p.learning == nil {
		return nil, errJanitorKnowledgeSourceUnavailable
	}
	files, err := p.reader.ReadApprovedFiles(ctx, userID)
	if err != nil {
		return nil, err
	}
	var items []personalassistant.KnowledgeItem
	for _, learning := range setupBatchLearnings(files) {
		item, created, err := p.learning.Propose(ctx, userID, learning.Proposal)
		switch {
		case errors.Is(err, personalassistant.ErrKnowledgeSuppressed):
			continue
		case errors.Is(err, personalassistant.ErrKnowledgeQuota):
			return items, nil
		case err != nil:
			return items, err
		}
		// Moves arrive one at a time, so a name is first proposed from the first
		// two files that show it. When more follow, the waiting learning's count is
		// brought up to date rather than left saying 2.
		if !created {
			item = p.refreshCount(ctx, userID, item, learning.Proposal.Text)
		}
		items = append(items, item)
	}
	return items, nil
}

var setupBatchTextPattern = regexp.MustCompile(`^You deal with (.+) \(seen in (\d+) files you filed\)\.$`)

// refreshedSetupText says whether a waiting learning's text should become
// proposed: it is this producer's own wording about the same name, and the new
// count is higher. Anything else, a user's edit included, is left as it is.
func refreshedSetupText(current, proposed string) bool {
	old := setupBatchTextPattern.FindStringSubmatch(current)
	now := setupBatchTextPattern.FindStringSubmatch(proposed)
	if old == nil || now == nil || !strings.EqualFold(old[1], now[1]) {
		return false
	}
	before, errBefore := strconv.Atoi(old[2])
	after, errAfter := strconv.Atoi(now[2])
	return errBefore == nil && errAfter == nil && after > before
}

// refreshCount updates a candidate still waiting for an answer. An answered
// learning, or one the review refuses to revise, is returned unchanged.
func (p setupBatchProducer) refreshCount(ctx context.Context, userID string, item personalassistant.KnowledgeItem, proposed string) personalassistant.KnowledgeItem {
	if item.State != personalassistant.KnowledgeCandidate || item.Prepared != nil {
		return item
	}
	for _, revision := range item.Revisions {
		if revision.ID != item.CurrentRevisionID {
			continue
		}
		if refreshedSetupText(revision.Text, proposed) {
			if updated, err := p.learning.EditCandidate(ctx, userID, item.ID, item.Version, proposed); err == nil {
				return updated
			}
		}
		break
	}
	return item
}

// revalidateSetupBatch is the approval check: the learning must still be one the
// approved files teach, and both of its supporting files must still be approved,
// applied and not undone. An undo, or a file that left the journal, withdraws it.
func (r *janitorKnowledgeReader) revalidateSetupBatch(ctx context.Context, binding personalassistant.KnowledgeBinding, item personalassistant.KnowledgeItem) error {
	files, err := r.ReadApprovedFiles(ctx, binding.UserID)
	if err != nil {
		return err
	}
	var evidence []personalassistant.KnowledgeEvidence
	for _, revision := range item.Revisions {
		if revision.ID == item.CurrentRevisionID {
			evidence = revision.Evidence
			break
		}
	}
	if len(evidence) != 2 {
		return errJanitorKnowledgeSourceUnavailable
	}
	for _, learning := range setupBatchLearnings(files) {
		if personalassistant.KnowledgeSemanticKey(binding, learning.Proposal) != item.SemanticKey {
			continue
		}
		seen := map[string]bool{}
		for _, support := range evidence {
			if support.SourceKind != personalassistant.SetupBatchSourceKind || !learning.Supports[support.SourceID] || seen[support.SourceID] {
				return errJanitorKnowledgeSourceUnavailable
			}
			seen[support.SourceID] = true
		}
		return nil
	}
	return errJanitorKnowledgeSourceUnavailable
}
