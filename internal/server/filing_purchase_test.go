package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func filingPurchaseFixture(t *testing.T) (*ServerBuilder, http.Handler) {
	t.Helper()
	t.Setenv("ORI_ECONOMY_ENABLED", "true")
	b, h := newDailyBriefTestServer(t)
	if rec := postFilingJSON(h, "/api/settings/workspace-root", []byte(`{"workspace_root":""}`)); rec.Code != http.StatusOK {
		t.Fatalf("confirm workspace root: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postMeetAssistantHire(t, h); rec.Code != http.StatusCreated {
		t.Fatalf("hire: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postFilingJSON(h, "/api/personal-hq/setup", []byte(`{"name":"Home Base"}`)); rec.Code >= 300 {
		t.Fatalf("build HQ: %d %s", rec.Code, rec.Body.String())
	}
	return b, h
}

func postFilingJSON(h http.Handler, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://localhost"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func filingConfirmBody(t *testing.T, cost int64, creative bool) []byte {
	t.Helper()
	body, err := json.Marshal(filingConfirmation{RequestID: uuid.NewString(), Cost: &cost, Creative: &creative})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func decodeFilingResult(t *testing.T, w *httptest.ResponseRecorder, status int, outcome string) economy.ExpansionQuote {
	t.Helper()
	if w.Code != status {
		t.Fatalf("purchase response = %d: %s; want %d", w.Code, w.Body.String(), status)
	}
	var r filingPurchaseResult
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || r.Outcome != outcome {
		t.Fatalf("purchase outcome = %s, %v; want %s", w.Body.String(), err, outcome)
	}
	return r.Quote
}

const filingPurchasePath = "/api/economy/filing-expansion"

func TestFilingPurchaseAtExactPriceReplaysAfterLostResponse(t *testing.T) {
	b, h := filingPurchaseFixture(t)
	downloads := filepath.Join(os.Getenv("HOME"), "Downloads")
	if err := os.Mkdir(downloads, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(downloads, "leave-me.pdf"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Different browser IDs must join the same purchase, even at balance 0.
	one, two := filingConfirmBody(t, 7, false), filingConfirmBody(t, 7, false)
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, body := range [][]byte{one, two} {
		go func() { responses <- postFilingJSON(h, filingPurchasePath, body) }()
	}
	first := decodeFilingResult(t, <-responses, http.StatusOK, "delivered")
	second := decodeFilingResult(t, <-responses, http.StatusOK, "delivered")
	if first.State != economy.ExpansionBuilt || len(first.Destinations) != 1 || len(second.Destinations) != 1 || first.Destinations[0] != second.Destinations[0] ||
		first.Charged == nil || *first.Charged != 7 || first.Waived == nil || *first.Waived != 0 || second.Balance == nil || *second.Balance != 0 {
		t.Fatalf("responses: %+v %+v", first, second)
	}
	claim, err := b.filingClaimStore().FindFilingClaim(t.Context(), userprofile.LocalUserID, "")
	if err != nil || claim == nil || !claim.Registered || claim.State != economy.FilingDelivered {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	assertFilingLedgerRows(t, b, 1, 0)
	ws, err := b.workspaceFileStore.Get(claim.WorkspaceID)
	if err != nil || ws.OwnerUserID != userprofile.LocalUserID || ws.IsAnchored() || !ws.HasInstalledCapability("file-janitor") || len(ws.AgentInstances) != 1 ||
		len(ws.PendingDirectoryRequirements()) != 1 || len(ws.ScheduledTasks) != 0 || len(ws.MissionRuns) != 0 {
		t.Fatalf("workspace team or setup not correct: %v", err)
	}
	privateRoot, err := b.workspaceFileStore.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range ws.DirectoryReferences {
		if ref.Path != privateRoot {
			t.Fatalf("purchase linked an external folder: %+v", ref)
		}
	}
	if batches, err := b.fileJanitorService.ListBatches(ws.ID); err != nil || len(batches) != 0 {
		t.Fatalf("purchase started a scan: %+v %v", batches, err)
	}
	janitor, err := b.fileJanitorService.Status(claim.WorkspaceID)
	if err != nil || janitor.Settings.IsSetUp() || janitor.Settings.RootPath != "" || !janitor.Settings.AutomationApprovedAt.IsZero() {
		t.Fatalf("purchase touched folder authority: %+v %v", janitor, err)
	}
	entries, err := os.ReadDir(downloads)
	if err != nil || len(entries) != 1 || entries[0].Name() != "leave-me.pdf" {
		t.Fatalf("purchase touched Downloads: %v %v", entries, err)
	}
	if b.progressionEngine.HasCompleted(progression.ShowFolderQuestID) || b.progressionEngine.HasCompleted(progression.FolderFirstLookQuestID) {
		t.Fatal("purchase completed a folder mission")
	}
	// Simulate losing the delivered marker after completed creation and before
	// the final reply. Startup must recover the same paid workspace at zero.
	if _, err := b.sessionStore.DB().ExecContext(t.Context(), `UPDATE economy_filing_claims SET state='building' WHERE id=?`, claim.ID); err != nil {
		t.Fatal(err)
	}
	if view, _ := readFilingExpansion(t, h); view.State != economy.ExpansionPending {
		t.Fatalf("GET reconciled a claim instead of reading it: %+v", view)
	}
	b.recoverFilingPurchase()
	third := decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, one), http.StatusOK, "delivered")
	if len(third.Destinations) != 1 || third.Destinations[0] != first.Destinations[0] || craftBalance(t, b) != 0 {
		t.Fatalf("startup created again or spent again: %+v", third)
	}
	if err := b.sessionStore.DeleteWorkspace(t.Context(), claim.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	removed := decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, two), http.StatusOK, "delivered")
	if removed.State != economy.ExpansionRemoved || len(removed.Destinations) != 0 || craftBalance(t, b) != 0 {
		t.Fatalf("removed destination was resold: %+v", removed)
	}
	assertFilingLedgerRows(t, b, 1, 0)
}

func TestFilingPurchaseDoesNotBypassDisabledEconomyOrSpendMissingCraft(t *testing.T) {
	b, h := filingPurchaseFixture(t)
	confirm := filingConfirmBody(t, 7, false)
	foreign := httptest.NewRequest(http.MethodPost, "http://localhost"+filingPurchasePath, bytes.NewReader(confirm))
	foreign.Header.Set("Content-Type", "application/json")
	foreign.Header.Set("Origin", "https://outside.example")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, foreign)
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d %s", response.Code, response.Body.String())
	}
	t.Setenv("ORI_ECONOMY_ENABLED", "false")
	decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, confirm), http.StatusConflict, string(economy.ExpansionDisabled))
	_, missions := readFilingExpansion(t, h)
	assertFreeFolderMission(t, missions)
	t.Setenv("ORI_ECONOMY_ENABLED", "true")
	if _, err := b.filingClaimStore().Debit(t.Context(), economy.Entry{Resource: economy.ResourceCraft, Amount: 5,
		Reason: economy.ReasonBuild, RefKind: economy.RefKindBuild, RefID: "fixture-spend"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	q := decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, confirm), http.StatusConflict, string(economy.ExpansionInsufficient))
	if q.Missing != 5 || q.Balance == nil || *q.Balance != 2 || q.ManualEarnURL == "" {
		t.Fatalf("short balance: %+v", q)
	}
	if _, err := b.sessionHandler.CreateFromTemplate(t.Context(), "Free Filing", fileJanitorBlueprintID); err != nil {
		t.Fatal(err)
	}
	if craftBalance(t, b) != 2 {
		t.Fatal("normal creation was priced")
	}
	assertFilingLedgerRows(t, b, 0, 0)
}

func TestFilingPurchaseCreativeAndStalePrice(t *testing.T) {
	b, h := filingPurchaseFixture(t)
	b.configManager.SetEconomySettings(true, economy.DefaultDailyEnergyTokens)
	old := filingConfirmBody(t, 7, false)
	decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, old), http.StatusConflict, "stale_price")
	waived := filingConfirmBody(t, 0, true)
	b.configManager.SetEconomySettings(false, economy.DefaultDailyEnergyTokens)
	decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, waived), http.StatusConflict, "stale_price")
	assertFilingLedgerRows(t, b, 0, 0)
	b.configManager.SetEconomySettings(true, economy.DefaultDailyEnergyTokens)
	q := decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, waived), http.StatusOK, "delivered")
	if q.Charged == nil || *q.Charged != 0 || q.Waived == nil || *q.Waived != 7 || craftBalance(t, b) != 7 {
		t.Fatalf("Creative purchase: %+v", q)
	}
	b.configManager.SetEconomySettings(false, economy.DefaultDailyEnergyTokens)
	q = decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, old), http.StatusOK, "delivered")
	if q.Charged == nil || *q.Charged != 0 || craftBalance(t, b) != 7 {
		t.Fatalf("Creative replay charged: %+v", q)
	}
	assertFilingLedgerRows(t, b, 1, 0)
}

func TestFilingPurchaseRefusesExistingFreeFilingAndFolderInput(t *testing.T) {
	b, h := filingPurchaseFixture(t)
	body := filingConfirmBody(t, 7, false)
	withPath := strings.TrimSuffix(string(body), "}") + `,"folder":"~/Downloads"}`
	if w := postFilingJSON(h, filingPurchasePath, []byte(withPath)); w.Code != http.StatusBadRequest {
		t.Fatalf("client tried to supply a folder: %d %s", w.Code, w.Body.String())
	}
	if _, err := b.sessionHandler.CreateFromTemplate(t.Context(), "Existing Filing", fileJanitorBlueprintID); err != nil {
		t.Fatal(err)
	}
	q := decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, body), http.StatusConflict, string(economy.ExpansionBuilt))
	if craftBalance(t, b) != 7 || len(q.Destinations) != 1 || q.Destinations[0].Name != "Existing Filing" {
		t.Fatalf("existing workspace not respected: %+v", q)
	}
	assertFilingLedgerRows(t, b, 0, 0)
}

func TestFilingStartupReversesOnlyProvenNonDelivery(t *testing.T) {
	b, h := filingPurchaseFixture(t)
	plan, err := b.sessionHandler.ReviewFileJanitorCreation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	first := economy.FilingClaim{ID: uuid.NewString(), WorkspaceID: uuid.NewString(), RequestID: uuid.NewString(), Owner: userprofile.LocalUserID, Cost: 7, BaseCost: 7, PlanJSON: string(data)}
	if err := b.filingClaimStore().ReserveFiling(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if craftBalance(t, b) != 0 {
		t.Fatal("interrupted reservation did not hold the price")
	}
	b.recoverFilingPurchase()
	b.recoverFilingPurchase()
	assertFilingLedgerRows(t, b, 1, 1)
	replay := []byte(`{"request_id":"` + first.RequestID + `","cost":7,"creative":false}`)
	decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, replay), http.StatusConflict, "refunded")
	if craftBalance(t, b) != 7 {
		t.Fatal("not refunded, or replay re-debited")
	}
	partial := first
	partial.ID, partial.WorkspaceID, partial.RequestID = uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := b.filingClaimStore().ReserveFiling(t.Context(), partial); err != nil {
		t.Fatal(err)
	}
	if err := b.sessionStore.CreateWorkspace(t.Context(), &session.Workspace{ID: partial.WorkspaceID, OwnerUserID: partial.Owner, Name: "Filing partial"}); err != nil {
		t.Fatal(err)
	}
	b.recoverFilingPurchase()
	decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, []byte(`{"check_only":true}`)), http.StatusAccepted, "pending")
	if err := b.sessionStore.DeleteWorkspace(t.Context(), partial.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	b.recoverFilingPurchase()
	if q, _ := readFilingExpansion(t, h); q.State != economy.ExpansionPending || craftBalance(t, b) != 0 {
		t.Fatalf("ambiguous registration was refunded or resold: %+v", q)
	}
	assertFilingLedgerRows(t, b, 2, 1)
}

func TestFilingPurchaseRecoversARealInterruptedReceiptWithoutCreatingAgain(t *testing.T) {
	b, h := filingPurchaseFixture(t)
	ctx := t.Context()
	if _, err := b.sessionStore.DB().ExecContext(ctx, `CREATE TRIGGER fail_receipt BEFORE UPDATE OF state ON economy_filing_claims
		WHEN NEW.state='delivered' BEGIN SELECT RAISE(ABORT,'test interrupted receipt'); END`); err != nil {
		t.Fatal(err)
	}
	body := filingConfirmBody(t, 7, false)
	q := decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, body), http.StatusAccepted, "pending")
	if q.State != economy.ExpansionPending || len(q.Destinations) != 0 || craftBalance(t, b) != 0 {
		t.Fatalf("incomplete receipt was not retained: %+v", q)
	}
	rows, err := b.sessionStore.ListWorkspaces(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("registered workspaces: %+v %v", rows, err)
	}
	if _, err := b.sessionStore.DB().ExecContext(ctx, `DROP TRIGGER fail_receipt`); err != nil {
		t.Fatal(err)
	}
	q = decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, []byte(`{"check_only":true}`)), http.StatusOK, "delivered")
	if len(q.Destinations) != 1 || craftBalance(t, b) != 0 {
		t.Fatalf("could not recover original purchase: %+v", q)
	}
	assertFilingLedgerRows(t, b, 1, 0)
}

func TestFilingPurchaseRestoresCraftOnFailedRegistration(t *testing.T) {
	b, h := filingPurchaseFixture(t)
	ctx := t.Context()
	if _, err := b.sessionStore.DB().ExecContext(ctx, `CREATE TRIGGER fail_registration BEFORE INSERT ON workspaces
		WHEN NEW.name='Filing' BEGIN SELECT RAISE(ABORT,'test blocked registration'); END`); err != nil {
		t.Fatal(err)
	}
	body := filingConfirmBody(t, 7, false)
	q := decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, body), http.StatusConflict, "refunded")
	if q.State != economy.ExpansionAffordable || craftBalance(t, b) != 7 {
		t.Fatalf("failed creation did not refund: %+v", q)
	}
	if _, err := b.sessionStore.DB().ExecContext(ctx, `DROP TRIGGER fail_registration`); err != nil {
		t.Fatal(err)
	}
	decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, body), http.StatusConflict, "refunded")
	assertFilingLedgerRows(t, b, 1, 1)
	decodeFilingResult(t, postFilingJSON(h, filingPurchasePath, filingConfirmBody(t, 7, false)), http.StatusOK, "delivered")
	assertFilingLedgerRows(t, b, 2, 1)
}

func assertFilingLedgerRows(t *testing.T, b *ServerBuilder, purchases, refunds int) {
	t.Helper()
	for ref, want := range map[string]int{economy.RefKindFilingPurchase: purchases, economy.RefKindFilingRefund: refunds} {
		var n int
		err := b.sessionStore.DB().QueryRowContext(t.Context(), `SELECT count(*) FROM economy_ledger WHERE ref_kind=?`, ref).Scan(&n)
		if err != nil || n != want {
			t.Fatalf("%s: rows = %d, %v; want %d", ref, n, err, want)
		}
	}
}
