//go:build darwin || linux

package filejanitor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sendFilingFixture is Downloads (ws-1) and Documents (ws-2) with Filing set up on
// sibling folders, a scan of Downloads with documents and images waiting, and the
// real conditional mover (sends) beside a real category mover (Filed).
type sendFilingFixture struct {
	*connectionFixture
	batch      JanitorBatch
	downloads  string
	documents  string
	connection ConnectionView
}

func newSendFilingFixture(t *testing.T, connect bool, names ...string) *sendFilingFixture {
	t.Helper()
	c := newConnectionFixture(t)
	f := &sendFilingFixture{connectionFixture: c, downloads: c.roots["ws-1"], documents: c.roots["ws-2"]}
	if len(names) == 0 {
		names = []string{"lease.pdf", "invoice.pdf", "photo.png"}
	}
	for _, name := range names {
		agedFile(t, f.downloads, name, 10)
	}
	c.s.SetMover(&realMover{})
	batch, created, err := c.s.ScanNow("ws-1", ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("scan: %v %v", created, err)
	}
	f.batch = batch
	if connect {
		f.connection = c.mustConnect("ws-1", "ws-2", "documents")
	}
	return f
}

func (f *sendFilingFixture) plan() ReviewPlan {
	f.t.Helper()
	plan, err := f.s.PlanReview(f.t.Context(), "local", "ws-1", f.batch.ID, ReviewPlanQuery{})
	if err != nil {
		f.t.Fatalf("plan: %v", err)
	}
	return plan
}

func (f *sendFilingFixture) picture() ReviewPicture {
	f.t.Helper()
	picture, err := f.s.ReadReviewPicture(f.t.Context(), "local", "ws-1", f.batch.ID)
	if err != nil {
		f.t.Fatalf("picture: %v", err)
	}
	return picture
}

func (f *sendFilingFixture) press(plan ReviewPlan, key string) (FilingResult, error) {
	return f.s.FileReviewPlan(f.t.Context(), "local", "ws-1", f.batch.ID, FileRequest{
		ExpectedRevision: plan.Revision, ExpectedGroupRevision: plan.GroupRevision,
		PlanDigest: plan.PlanDigest, TrashAcknowledged: plan.TrashCount, ClientKey: key,
	})
}

func (f *sendFilingFixture) edit(action string, configure func(*ReviewDraftEdit)) (ReviewDraftView, error) {
	f.t.Helper()
	before, err := f.s.ReadReviewDraft(f.t.Context(), "local", "ws-1", f.batch.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	request := ReviewDraftEdit{ExpectedRevision: before.Revision, ExpectedGroupRevision: before.GroupRevision, Action: action}
	if configure != nil {
		configure(&request)
	}
	return f.s.EditReviewDraft("local", "ws-1", f.batch.ID, request)
}

func (f *sendFilingFixture) at(root string, name ...string) bool {
	_, err := os.Lstat(filepath.Join(append([]string{root}, name...)...))
	return err == nil
}

func (f *sendFilingFixture) read(path string) string {
	f.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// FR 20 to 22, FR 24: with a connection, the confident documents are drawn as
// leaving and ticked on open; images stay a Filed card.
func TestSendPicture_LeavingCardIsTickedOnOpenAndFiledKeepsTheRest(t *testing.T) {
	f := newSendFilingFixture(t, true)
	picture := f.picture()
	if len(picture.Leaving) != 1 {
		t.Fatalf("want one Leaving card, got %+v", picture.Leaving)
	}
	card := picture.Leaving[0]
	if card.ReceiverName != "Documents" || card.Count != 2 || card.GroupState != "ticked" || card.NotSentCount != 0 || len(card.Files) != 2 {
		t.Fatalf("Leaving card: %+v", card)
	}
	for _, folder := range picture.Folders {
		if folder.Category == CategoryDocuments {
			t.Fatalf("documents must not also be drawn in Filed: %+v", folder)
		}
	}
	placed := card.Count + card.NotSentCount
	for _, folder := range picture.Folders {
		placed += folder.Count + folder.UnfiledCount
	}
	for _, row := range picture.Loose {
		placed += row.LooseCount
	}
	if placed+picture.LeftOut.Count+picture.Trash.Count+picture.CouldNotFile.Count != picture.Waiting {
		t.Fatalf("the picture must add up to what is waiting: %d placed of %d", placed, picture.Waiting)
	}
}

// FR 20 and success measure 3: with no connection, or a connection turned off, the
// picture is exactly what it was before sends existed.
func TestSendPicture_NoConnectionAndAnEndedOneDrawTheSamePicture(t *testing.T) {
	f := newSendFilingFixture(t, false)
	before, _ := json.Marshal(f.picture())
	beforePlan := f.plan()
	view := f.mustConnect("ws-1", "ws-2", "documents")
	if _, err := f.s.TurnOffConnection("local", "ws-1", view.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(f.picture())
	if string(before) != string(after) {
		t.Fatalf("an ended connection changed the picture:\nbefore %s\nafter  %s", before, after)
	}
	if strings.Contains(string(after), `"leaving"`) {
		t.Fatal("with no connection the picture has no Leaving field at all")
	}
	if afterPlan := f.plan(); afterPlan.PlanDigest != beforePlan.PlanDigest || afterPlan.Revision != beforePlan.Revision {
		t.Fatal("an ended connection changed the plan")
	}
}

// FR 23, FR 26: keeping the documents here moves them into Filed for this review;
// the plan's totals follow, and sending them again puts them back on the card.
func TestSendChoices_KeepThenSendAgain(t *testing.T) {
	f := newSendFilingFixture(t, true)
	plan := f.plan()
	if plan.SendCount != 2 || plan.MoveCount != 1 || plan.Total != 3 || len(plan.Sends) != 1 || plan.Sends[0].Count != 2 {
		t.Fatalf("plan with a connection: %+v", plan)
	}
	if _, err := f.edit("keep_connection", func(r *ReviewDraftEdit) { r.ConnectionID = f.connection.ID }); err != nil {
		t.Fatalf("keep: %v", err)
	}
	kept := f.plan()
	if kept.SendCount != 0 || kept.MoveCount != 3 || len(kept.Sends) != 0 {
		t.Fatalf("plan after keep: %+v", kept)
	}
	picture := f.picture()
	if picture.Leaving[0].Count != 0 || picture.Leaving[0].KeptCount != 2 {
		t.Fatalf("Leaving card after keep: %+v", picture.Leaving[0])
	}
	if kept.Revision == plan.Revision || kept.PlanDigest == plan.PlanDigest {
		t.Fatal("keeping must change the revision and the digest")
	}
	if _, err := f.edit("send_connection", func(r *ReviewDraftEdit) { r.ConnectionID = f.connection.ID }); err != nil {
		t.Fatalf("send again: %v", err)
	}
	if again := f.plan(); again.SendCount != 2 || again.MoveCount != 1 {
		t.Fatalf("plan after sending again: %+v", again)
	}
}

// FR 28: one file can be taken out of the send.
func TestSendChoices_OneFileTakenOutStaysLoose(t *testing.T) {
	f := newSendFilingFixture(t, true)
	lease := candidateIDNamed(t, f, "lease.pdf")
	if _, err := f.edit("exclude_files", func(r *ReviewDraftEdit) { r.CandidateIDs = []string{lease} }); err != nil {
		t.Fatal(err)
	}
	plan := f.plan()
	if plan.SendCount != 1 {
		t.Fatalf("one file left the send: %+v", plan)
	}
	if card := f.picture().Leaving[0]; card.GroupState != "partial" || card.Count != 1 {
		t.Fatalf("a card with one file taken out is partial: %+v", card)
	}
}

func candidateIDNamed(t *testing.T, f *sendFilingFixture, name string) string {
	t.Helper()
	state, err := f.s.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range state.CandidatesFor(f.batch.ID) {
		if candidate.Name == name {
			return candidate.ID
		}
	}
	t.Fatalf("no candidate %s", name)
	return ""
}

// The one press, the receipt and Undo all across both folders (FR 13, 16, 17, 29
// to 33; success measure 1).
func TestSendPress_SendsToTheTopLevelThenUndoAllBringsEverythingBack(t *testing.T) {
	f := newSendFilingFixture(t, true)
	if err := os.MkdirAll(filepath.Join(f.documents, "Taxes"), 0o750); err != nil {
		t.Fatal(err)
	}
	result, err := f.press(f.plan(), "press-sends-0123456789")
	if err != nil {
		t.Fatalf("press: %v", err)
	}
	if result.Filed != 3 || result.Sent != 2 || result.Failed != 0 || result.Stale != 0 {
		t.Fatalf("result: %+v", result)
	}
	for _, name := range []string{"lease.pdf", "invoice.pdf"} {
		if f.at(f.downloads, name) || !f.at(f.documents, name) || f.at(f.documents, "Taxes", name) || f.at(f.downloads, "Filed", "Documents", name) {
			t.Fatalf("%s must be at the top level of Documents only", name)
		}
	}
	if !f.at(f.downloads, "Filed", "Images", "photo.png") {
		t.Fatal("photo.png goes to Downloads/Filed/Images")
	}

	receipt, err := f.s.ReadFiling(t.Context(), "local", "ws-1", result.FilingID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Filed != 3 || receipt.Sent != 2 || len(receipt.Sends) != 1 || receipt.Sends[0].ReceiverName != "Documents" || receipt.Sends[0].Count != 2 {
		t.Fatalf("receipt: %+v", receipt)
	}
	for _, destination := range receipt.ByDestination {
		if destination.Destination == "." || destination.Category == CategoryDocuments {
			t.Fatalf("a send must never appear as a Filed destination: %+v", receipt.ByDestination)
		}
	}
	summaries, err := f.s.ListFilings(t.Context(), "local", "ws-1", 1)
	if err != nil || len(summaries) != 1 || summaries[0].Sent != 2 || !summaries[0].UndoAvailable {
		t.Fatalf("History's last filing: %v %+v", err, summaries)
	}

	// No absolute path in either workspace's journal.
	for _, id := range []string{"ws-1", "ws-2"} {
		path, err := f.s.store.scanStatePath(id)
		if err != nil {
			t.Fatal(err)
		}
		if raw, err := os.ReadFile(path); err == nil {
			for _, root := range f.roots {
				if strings.Contains(string(raw), root) {
					t.Fatalf("%s's scan state holds a folder path", id)
				}
			}
		}
	}

	undo, err := f.s.UndoFiling(t.Context(), "local", "ws-1", result.FilingID)
	if err != nil {
		t.Fatalf("undo all: %v", err)
	}
	if undo.Back != 3 || undo.NotBack != 0 {
		t.Fatalf("undo all: %+v", undo)
	}
	for _, name := range []string{"lease.pdf", "invoice.pdf", "photo.png"} {
		if !f.at(f.downloads, name) {
			t.Fatalf("%s is not back in Downloads", name)
		}
	}
	if f.at(f.documents, "lease.pdf") || f.at(f.documents, "invoice.pdf") {
		t.Fatal("Documents still holds a sent file after Undo all")
	}
}

// FR 14, FR 25, FR 32: a name already used at the receiver leaves the file here,
// the plan counts it before the press, and the receipt says why. Nothing is
// replaced and Ori picks no other name.
func TestSendPress_AUsedNameLeavesBothFilesAlone(t *testing.T) {
	f := newSendFilingFixture(t, true)
	if err := os.WriteFile(filepath.Join(f.documents, "lease.pdf"), []byte("the receiver's own lease"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := f.plan()
	if len(plan.Sends) != 1 || plan.Sends[0].NameUsed != 1 {
		t.Fatalf("the plan must count the used name: %+v", plan.Sends)
	}
	result, err := f.press(plan, "press-used-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if result.Sent != 1 || result.Stale != 1 {
		t.Fatalf("result: %+v", result)
	}
	if !f.at(f.downloads, "lease.pdf") || f.read(filepath.Join(f.documents, "lease.pdf")) != "the receiver's own lease" {
		t.Fatal("a used name must leave both files as they were")
	}
	entries, _ := os.ReadDir(f.documents)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "lease") && entry.Name() != "lease.pdf" {
			t.Fatalf("Ori picked another name: %s", entry.Name())
		}
	}
	receipt, _ := f.s.ReadFiling(t.Context(), "local", "ws-1", result.FilingID, 0)
	found := false
	for _, line := range receipt.NotMoved {
		found = found || (line.Name == "lease.pdf" && strings.Contains(line.Reason, "already used in Documents"))
	}
	if !found {
		t.Fatalf("the receipt must say why lease.pdf stayed: %+v", receipt.NotMoved)
	}
}

// FR 29, success measure 2: a connection turned off after the picture was drawn
// changes the plan, and the press moves nothing on either side.
func TestSendPress_NothingMovesWhenTheConnectionWasTurnedOff(t *testing.T) {
	f := newSendFilingFixture(t, true)
	plan := f.plan()
	if _, err := f.s.TurnOffConnection("local", "ws-2", f.connection.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.press(plan, "press-off-0123456789")
	if !errors.Is(err, ErrReviewPlanChanged) {
		t.Fatalf("want the plan changed, got %v", err)
	}
	for _, name := range []string{"lease.pdf", "invoice.pdf", "photo.png"} {
		if !f.at(f.downloads, name) || f.at(f.documents, name) {
			t.Fatalf("%s moved after the connection was turned off", name)
		}
	}
}

// FR 29: the receiver pointed at another folder after the picture was drawn.
func TestSendPress_NothingMovesWhenTheReceiverWasRelinked(t *testing.T) {
	f := newSendFilingFixture(t, true)
	plan := f.plan()
	other := filepath.Join(f.base, "Papers")
	if err := os.Mkdir(other, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Relink(nil, RelinkRequest{WorkspaceID: "ws-2", Path: other}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.press(plan, "press-relink-0123456789"); !errors.Is(err, ErrReviewPlanChanged) {
		t.Fatalf("want the plan changed, got %v", err)
	}
	if f.at(other, "lease.pdf") || f.at(f.documents, "lease.pdf") || !f.at(f.downloads, "lease.pdf") {
		t.Fatal("a file moved after the receiver was relinked")
	}
}

// FR 34: Undo all after the receiver's Filing was removed reports the sent files
// as not put back and still undoes the Filed ones, touching nothing in the
// receiver's folder.
func TestSendUndoAll_ReceiverGoneStillUndoesTheFiledFiles(t *testing.T) {
	f := newSendFilingFixture(t, true)
	result, err := f.press(f.plan(), "press-gone-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RevokeAccess(nil, "ws-2"); err != nil {
		t.Fatal(err)
	}
	undo, err := f.s.UndoFiling(t.Context(), "local", "ws-1", result.FilingID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.Back != 1 || undo.NotBack != 2 {
		t.Fatalf("undo: %+v", undo)
	}
	for _, outcome := range undo.Outcomes {
		if outcome.Operation == OperationSend && (outcome.Location != LocationSent || !strings.Contains(outcome.Message, "Documents no longer has Filing")) {
			t.Fatalf("a sent file must be reported as still in Documents: %+v", outcome)
		}
	}
	if !f.at(f.downloads, "photo.png") || !f.at(f.documents, "lease.pdf") || !f.at(f.documents, "invoice.pdf") {
		t.Fatal("the Filed file is back and the sent files are untouched")
	}
}

// FR 33: Undo all works after the connection was turned off, while both
// workspaces still manage the same folders.
func TestSendUndoAll_WorksAfterTheConnectionWasTurnedOff(t *testing.T) {
	f := newSendFilingFixture(t, true)
	result, err := f.press(f.plan(), "press-later-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.TurnOffConnection("local", "ws-1", f.connection.ID); err != nil {
		t.Fatal(err)
	}
	undo, err := f.s.UndoFiling(t.Context(), "local", "ws-1", result.FilingID)
	if err != nil || undo.Back != 3 {
		t.Fatalf("undo after turn-off: %v %+v", err, undo)
	}
}

// The per-file Undo in History refuses a send without touching it: sends are put
// back with both folders held.
func TestSendUndo_PerFileUndoRefusesASendAndLeavesItUndoable(t *testing.T) {
	f := newSendFilingFixture(t, true)
	result, err := f.press(f.plan(), "press-single-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	var sendID string
	for _, outcome := range result.Outcomes {
		if outcome.Operation == OperationSend {
			sendID = outcome.ActionID
		}
	}
	if _, err := f.s.Undo(t.Context(), "ws-1", sendID, "local"); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("want refused, got %v", err)
	}
	state, _ := f.s.store.LoadScanState("ws-1")
	action, _ := state.Action(sendID)
	if !action.Undoable() {
		t.Fatalf("the refusal must leave the send undoable: %+v", action)
	}
}

// An older build that rewrites the draft drops connection_id: the file is then
// kept in Filed, never sent. And the legacy preview refuses a draft with a send.
func TestSendChoices_ALostMarkKeepsTheFileAndTheLegacyPreviewRefusesASend(t *testing.T) {
	f := newSendFilingFixture(t, true)
	if _, err := f.edit("send_connection", func(r *ReviewDraftEdit) { r.ConnectionID = f.connection.ID }); err != nil {
		t.Fatal(err)
	}
	state, _ := f.s.store.LoadScanState("ws-1")
	if _, _, err := f.s.reviewApprovalBinding(state, "ws-1", "local", f.batch.ID, "x", nil); !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("the legacy preview must refuse a draft with a send, got %v", err)
	}
	if _, err := f.s.store.UpdateScanState("ws-1", func(scan *ScanState) error {
		for i := range scan.ReviewDrafts {
			for j := range scan.ReviewDrafts[i].Choices {
				scan.ReviewDrafts[i].Choices[j].ConnectionID = "" // what an older rewrite does
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if plan := f.plan(); plan.SendCount != 0 || plan.MoveCount != 3 {
		t.Fatalf("a lost mark must keep the files in Filed: %+v", plan)
	}
}

// FR 27: a question row answered "Send to Documents".
func TestSendChoices_AQuestionRowCanBeSent(t *testing.T) {
	f := newSendFilingFixture(t, true, "mystery.qqq", "photo.png")
	picture := f.picture()
	if len(picture.Loose) == 0 {
		t.Skip("this fixture produced no question row")
	}
	row := picture.Loose[0]
	if _, err := f.edit("send_group", func(r *ReviewDraftEdit) { r.GroupID, r.ConnectionID = row.GroupID, f.connection.ID }); err != nil {
		t.Fatalf("send a row: %v", err)
	}
	plan := f.plan()
	if plan.SendCount != row.Count {
		t.Fatalf("the row's files go to Documents: %+v", plan)
	}
	after := f.picture()
	if after.Leaving[0].Count != row.Count || after.Loose[0].SentCount != row.Count {
		t.Fatalf("picture after sending a row: leaving %+v row %+v", after.Leaving[0], after.Loose[0])
	}
}

// A send edit naming a connection that is not on is a conflict, and every other
// action refuses a connection ID.
func TestSendChoices_EditsAreClosed(t *testing.T) {
	f := newSendFilingFixture(t, true)
	if _, err := f.edit("send_connection", func(r *ReviewDraftEdit) { r.ConnectionID = "connection-not-on" }); !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("want a conflict, got %v", err)
	}
	if _, err := f.edit("select_filing_groups", func(r *ReviewDraftEdit) { r.ConnectionID = f.connection.ID }); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("want invalid, got %v", err)
	}
	if _, err := f.edit("send_connection", func(r *ReviewDraftEdit) {
		r.ConnectionID, r.CandidateIDs = f.connection.ID, []string{candidateIDNamed(t, f, "lease.pdf")}
	}); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("send_connection takes no file list, got %v", err)
	}
}

// The picture and the plan check write nothing, on either side: reading them
// with a connection on leaves both workspaces' saved state and settings as they
// were, and creates nothing in the receiver's folder.
func TestSendReads_ChangeNothingInEitherWorkspace(t *testing.T) {
	f := newSendFilingFixture(t, true)
	snapshot := func() map[string]string {
		out := map[string]string{}
		for _, id := range []string{"ws-1", "ws-2"} {
			dir, err := f.s.store.StateDir(id)
			if err != nil {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				out[id+"/"+entry.Name()] = f.read(filepath.Join(dir, entry.Name()))
			}
		}
		entries, _ := os.ReadDir(f.documents)
		for _, entry := range entries {
			out["documents/"+entry.Name()] = "present"
		}
		return out
	}
	before := snapshot()
	_ = f.picture()
	_ = f.plan()
	if _, err := f.s.ReadReviewDraft(t.Context(), "local", "ws-1", f.batch.ID); err != nil {
		t.Fatal(err)
	}
	after := snapshot()
	if len(before) != len(after) {
		t.Fatalf("a read added or removed a file: %d -> %d", len(before), len(after))
	}
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("a read changed %s", key)
		}
	}
}

// FR 19: sends count inside the one limit of files per press.
func TestSendPlan_SendsCountInsideThePressLimit(t *testing.T) {
	f := newSendFilingFixture(t, true)
	plan := f.plan()
	if plan.Total != plan.MoveCount+plan.SendCount+plan.TrashCount {
		t.Fatalf("total must count sends: %+v", plan)
	}
	state, _ := f.s.store.LoadScanState("ws-1")
	computed, err := f.s.computeReviewPlan("local", "ws-1", f.batch.ID, state, f.s.liveSendRoutes("local", "ws-1"))
	if err != nil {
		t.Fatal(err)
	}
	if computed.plan.OverLimit {
		t.Fatal("three files are under the limit")
	}
}
