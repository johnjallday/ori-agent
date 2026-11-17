package filejanitorhttp

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func filingsTarget(prefix, suffix string) string {
	return pathFor(prefix, "ws-1", "/filings"+suffix)
}

// pressedHandler files three images in one press and returns the filing's ID.
func pressedHandler(t *testing.T) (*Handler, string, string) {
	t.Helper()
	h, root := pictureHandler(t, mixedPictureFiles(3))
	h.service.SetMover(renameMover{})
	_, picture, _ := readPicture(t, h, "file-janitor")
	batchID := picture["batch_id"].(string)
	rec, body := serve(t, h, http.MethodPost, fileTarget("file-janitor", batchID), pressBody(t, h, batchID, httpKey, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("press: %d %s", rec.Code, rec.Body.String())
	}
	return h, root, body["filing"].(map[string]any)["filing_id"].(string)
}

// R43, R44: the receipt is the same on both prefixes, comes from what happened, and
// carries nothing private. The list feeds History's "Last filing" line.
func TestFilingsHTTP_ReceiptAndListAgreeOnBothPrefixes(t *testing.T) {
	h, root, filingID := pressedHandler(t)
	var canonical map[string]any
	for _, prefix := range routePrefixPair {
		rec, body := serve(t, h, http.MethodGet, filingsTarget(prefix.prefix, "/"+filingID), "")
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", prefix.name, rec.Code, rec.Body.String())
		}
		if canonical != nil && !reflect.DeepEqual(canonical, body) {
			t.Fatal("canonical and legacy receipts diverged")
		}
		canonical = body
		receipt := body["receipt"].(map[string]any)
		if receipt["filed"] != float64(3) || receipt["state"] != "done" || receipt["undo_available"] != true || receipt["not_moved_total"] != float64(0) {
			t.Fatalf("%s: receipt %v", prefix.name, receipt)
		}
		destinations := receipt["by_destination"].([]any)
		if len(destinations) != 1 || destinations[0].(map[string]any)["destination"] != "Filed/Images" || destinations[0].(map[string]any)["count"] != float64(3) {
			t.Fatalf("%s: one row per destination: %v", prefix.name, destinations)
		}
		for _, private := range []string{root, "fingerprint", "token", "scope", "trash_restore_token"} {
			if strings.Contains(rec.Body.String(), private) {
				t.Fatalf("%s: the receipt leaked %q", prefix.name, private)
			}
		}
		listRec, listBody := serve(t, h, http.MethodGet, filingsTarget(prefix.prefix, "?limit=1"), "")
		filings := listBody["filings"].([]any)
		if listRec.Code != http.StatusOK || len(filings) != 1 || filings[0].(map[string]any)["filing_id"] != filingID || filings[0].(map[string]any)["undo_available"] != true {
			t.Fatalf("%s: list %d %v", prefix.name, listRec.Code, listBody)
		}
	}
}

// R47, R50: Undo all puts every file back, on either prefix; a second press (here on
// the other prefix, as a second window would) reports the first result and moves
// nothing; the receipt then says so and no longer offers it.
func TestFilingsHTTP_UndoAllRunsOnceAndAnswersASecondPress(t *testing.T) {
	h, root, filingID := pressedHandler(t)
	undo := func(prefix string) map[string]any {
		t.Helper()
		rec, body := serve(t, h, http.MethodPost, filingsTarget(prefix, "/"+filingID+"/undo"), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", prefix, rec.Code, rec.Body.String())
		}
		return body["undo"].(map[string]any)
	}
	first := undo("file-janitor")
	if first["back"] != float64(3) || first["not_back"] != float64(0) || first["replayed"] != nil || len(first["outcomes"].([]any)) != 3 {
		t.Fatalf("first: %v", first)
	}
	for i := range 3 {
		name := "image-000" + string(rune('0'+i)) + ".png"
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s should be back: %v", name, err)
		}
	}
	second := undo("downloads-janitor")
	if second["replayed"] != true || second["back"] != float64(3) || second["filing_id"] != filingID {
		t.Fatalf("second: %v", second)
	}
	_, body := serve(t, h, http.MethodGet, filingsTarget("file-janitor", "/"+filingID), "")
	receipt := body["receipt"].(map[string]any)
	undone := receipt["undo"].(map[string]any)
	if receipt["undo_available"] != false || undone["state"] != "done" || undone["back"] != float64(3) {
		t.Fatalf("receipt after Undo all: %v", receipt)
	}
}

// R52: forget the restored files, then (a separate request) scan: nothing is
// forgotten before Undo all, and the answer says how many were.
func TestFilingsHTTP_ForgetRestoredAfterUndoAll(t *testing.T) {
	h, _, filingID := pressedHandler(t)
	rec, body := serve(t, h, http.MethodPost, filingsTarget("file-janitor", "/"+filingID+"/forget-restored"), "")
	if rec.Code != http.StatusOK || body["forgotten"] != float64(0) {
		t.Fatalf("before Undo all: %d %v", rec.Code, body)
	}
	serve(t, h, http.MethodPost, filingsTarget("file-janitor", "/"+filingID+"/undo"), "")
	for _, prefix := range routePrefixPair {
		rec, body = serve(t, h, http.MethodPost, filingsTarget(prefix.prefix, "/"+filingID+"/forget-restored"), "")
		if rec.Code != http.StatusOK || body["forgotten"] != float64(3) {
			t.Fatalf("%s: %d %v", prefix.name, rec.Code, body)
		}
	}
}

func TestFilingsHTTP_RefusesWhatItCannotAnswerHonestly(t *testing.T) {
	h, _, filingID := pressedHandler(t)
	for name, tc := range map[string]struct {
		method, suffix, body string
		want                 int
	}{
		"an unknown filing receipt":  {http.MethodGet, "/filing-does-not-exist", "", http.StatusNotFound},
		"a malformed filing id":      {http.MethodGet, "/..%2Fetc", "", http.StatusNotFound},
		"a bad page start":           {http.MethodGet, "/" + filingID + "?offset=-1", "", http.StatusBadRequest},
		"a bad list limit":           {http.MethodGet, "?limit=0", "", http.StatusBadRequest},
		"a list limit that is large": {http.MethodGet, "?limit=999", "", http.StatusBadRequest},
		"undo of an unknown filing":  {http.MethodPost, "/filing-does-not-exist/undo", "", http.StatusConflict},
		"undo with a body":           {http.MethodPost, "/" + filingID + "/undo", `{"all":true}`, http.StatusBadRequest},
		"forget with a body":         {http.MethodPost, "/" + filingID + "/forget-restored", `{"all":true}`, http.StatusBadRequest},
		"forget for an unknown one":  {http.MethodPost, "/filing-does-not-exist/forget-restored", "", http.StatusConflict},
	} {
		rec, _ := serve(t, h, tc.method, filingsTarget("file-janitor", tc.suffix), tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s: got %d (%s), want %d", name, rec.Code, rec.Body.String(), tc.want)
		}
	}
	// Nothing above claimed or changed anything: the real Undo all still works.
	rec, body := serve(t, h, http.MethodPost, filingsTarget("file-janitor", "/"+filingID+"/undo"), "")
	if rec.Code != http.StatusOK || body["undo"].(map[string]any)["back"] != float64(3) {
		t.Fatalf("after refusals: %d %s", rec.Code, rec.Body.String())
	}
}
