package downloadsjanitorhttp

import (
	"fmt"
	"net/http"
	"testing"
)

// largeBatch sets up a workspace whose inbox holds `count` eligible files and
// scans it, returning the handler.
//
// The names are deterministic and typed so the classifier's answer — and
// therefore the per-filter counts — are the same on every run. A fixture whose
// categories drifted would make the paging assertions flaky for a reason that
// has nothing to do with paging.
func largeBatch(t *testing.T, count int) *Handler {
	t.Helper()
	h, root := configuredHandler(t)
	for i := range count {
		agedFile(t, root, fmt.Sprintf("report-%03d.pdf", i), 16)
	}
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/scan", ""); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %s", rec.Body.String())
	}
	return h
}

func candidateIDs(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, _ := body["candidates"].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		candidate, _ := item.(map[string]any)
		id, _ := candidate["id"].(string)
		out = append(out, id)
	}
	return out
}

// A batch of 500 files must not arrive in one response. The page is bounded and
// the counts still describe the whole batch — a surface that reported "50
// files" because fifty fit on a page would be lying about how much work is
// waiting (FR-109, FR-150).
func TestGetBatch_PagesCandidatesWithoutLosingTheCounts(t *testing.T) {
	const total = 500
	h := largeBatch(t, total)

	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/batches/latest", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	page := candidateIDs(t, body)
	if len(page) != defaultCandidateLimit {
		t.Fatalf("page size = %d, want the default %d", len(page), defaultCandidateLimit)
	}
	if body["total"] != float64(total) {
		t.Errorf("total = %v, want %d — counts describe the batch, not the page", body["total"], total)
	}
	if body["filtered_total"] != float64(total) {
		t.Errorf("filtered_total = %v, want %d", body["filtered_total"], total)
	}
	counts, _ := body["counts"].(map[string]any)
	if counts["all"] != float64(total) {
		t.Errorf("counts[all] = %v, want %d", counts["all"], total)
	}
	if counts["pending"] != float64(total) {
		t.Errorf("counts[pending] = %v, want %d", counts["pending"], total)
	}
}

// Paging is stable and non-overlapping: the same request twice returns the same
// rows, and consecutive pages do not repeat a file. Unstable order would let a
// user approve a row they never saw.
func TestGetBatch_PagingIsStableAndDisjoint(t *testing.T) {
	h := largeBatch(t, 120)
	base := "/api/workspaces/ws-1/downloads-janitor/batches/latest"

	_, first := serve(t, h, http.MethodGet, base+"?limit=20&offset=0", "")
	_, firstAgain := serve(t, h, http.MethodGet, base+"?limit=20&offset=0", "")
	_, second := serve(t, h, http.MethodGet, base+"?limit=20&offset=20", "")

	firstIDs := candidateIDs(t, first)
	if len(firstIDs) != 20 {
		t.Fatalf("page size = %d, want 20", len(firstIDs))
	}
	repeated := candidateIDs(t, firstAgain)
	for i := range firstIDs {
		if firstIDs[i] != repeated[i] {
			t.Fatalf("the same request returned a different order at %d", i)
		}
	}

	seen := map[string]bool{}
	for _, id := range firstIDs {
		seen[id] = true
	}
	for _, id := range candidateIDs(t, second) {
		if seen[id] {
			t.Fatalf("candidate %s appeared on two consecutive pages", id)
		}
	}
}

// An offset past the end is an empty page, not an error and not a wrap to the
// start: a client that keeps paging must be able to stop.
func TestGetBatch_OffsetPastTheEndIsAnEmptyPage(t *testing.T) {
	h := largeBatch(t, 10)

	rec, body := serve(t, h, http.MethodGet,
		"/api/workspaces/ws-1/downloads-janitor/batches/latest?offset=999", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := len(candidateIDs(t, body)); got != 0 {
		t.Fatalf("candidates = %d, want an empty page", got)
	}
	if body["total"] != float64(10) {
		t.Errorf("total = %v, want 10 even past the end", body["total"])
	}
}

// The filter vocabulary is fixed server-side. Anything outside it is rejected
// rather than silently treated as "all", which would reveal more rows than the
// client asked for (FR-141).
func TestGetBatch_RejectsAnUnknownFilter(t *testing.T) {
	h := largeBatch(t, 3)

	rec, body := serve(t, h, http.MethodGet,
		"/api/workspaces/ws-1/downloads-janitor/batches/latest?filter=everything", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown filter", rec.Code)
	}
	if body["code"] != "invalid_filter" {
		t.Errorf("code = %v, want invalid_filter", body["code"])
	}
}

// "all" and the empty string mean the same thing, so a client may send either.
func TestGetBatch_AllFilterIsTheEmptyFilter(t *testing.T) {
	h := largeBatch(t, 5)
	base := "/api/workspaces/ws-1/downloads-janitor/batches/latest"

	_, explicit := serve(t, h, http.MethodGet, base+"?filter=all", "")
	_, implicit := serve(t, h, http.MethodGet, base, "")
	if len(candidateIDs(t, explicit)) != len(candidateIDs(t, implicit)) {
		t.Fatal("filter=all must match no filter at all")
	}
}

// A malformed page request is rejected rather than reinterpreted. A limit the
// server quietly substitutes is one where client and server disagree about
// which rows were shown — and that disagreement ends in an approval built from
// rows the user never saw.
func TestGetBatch_RejectsMalformedPagination(t *testing.T) {
	h := largeBatch(t, 3)
	base := "/api/workspaces/ws-1/downloads-janitor/batches/latest"

	for _, query := range []string{"?limit=0", "?limit=-5", "?limit=abc", "?offset=-1", "?offset=x"} {
		t.Run(query, func(t *testing.T) {
			rec, body := serve(t, h, http.MethodGet, base+query, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for %q", rec.Code, query)
			}
			if body["code"] != "invalid_pagination" {
				t.Errorf("code = %v, want invalid_pagination", body["code"])
			}
		})
	}
}

// An oversized limit is clamped rather than refused: asking for more than the
// cap is a reasonable client being optimistic, not a malformed request. The cap
// is what keeps the response bounded.
func TestGetBatch_ClampsAnOversizedLimit(t *testing.T) {
	h := largeBatch(t, 400)

	rec, body := serve(t, h, http.MethodGet,
		"/api/workspaces/ws-1/downloads-janitor/batches/latest?limit=100000", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if body["limit"] != float64(maxCandidateLimit) {
		t.Errorf("limit = %v, want it clamped to %d", body["limit"], maxCandidateLimit)
	}
	if got := len(candidateIDs(t, body)); got > maxCandidateLimit {
		t.Errorf("returned %d candidates, above the cap of %d", got, maxCandidateLimit)
	}
}

// Filtering narrows the page without changing what the batch contains, so the
// user can always see how much they have filtered out.
func TestGetBatch_FilterNarrowsRowsButNotTheBatchTotal(t *testing.T) {
	h := largeBatch(t, 12)
	base := "/api/workspaces/ws-1/downloads-janitor/batches/latest"

	_, all := serve(t, h, http.MethodGet, base, "")
	ids := candidateIDs(t, all)
	if len(ids) == 0 {
		t.Fatal("expected candidates")
	}

	// Skip one file, then ask for the skipped filter.
	payload := fmt.Sprintf(`{"decisions":[{"candidate_id":%q,"decision":"skip"}]}`, ids[0])
	if rec, _ := serve(t, h, http.MethodPost,
		"/api/workspaces/ws-1/downloads-janitor/decisions", payload); rec.Code != http.StatusOK {
		t.Fatalf("skip failed: %s", rec.Body.String())
	}

	rec, body := serve(t, h, http.MethodGet, base+"?filter=skipped", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := len(candidateIDs(t, body)); got != 1 {
		t.Fatalf("skipped rows = %d, want 1", got)
	}
	if body["filtered_total"] != float64(1) {
		t.Errorf("filtered_total = %v, want 1", body["filtered_total"])
	}
	if body["total"] != float64(12) {
		t.Errorf("total = %v, want the whole batch (12)", body["total"])
	}
	counts, _ := body["counts"].(map[string]any)
	if counts["skipped"] != float64(1) {
		t.Errorf("counts[skipped] = %v, want 1", counts["skipped"])
	}
	if counts["pending"] != float64(11) {
		t.Errorf("counts[pending] = %v, want 11", counts["pending"])
	}
}
