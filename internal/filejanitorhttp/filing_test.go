package filejanitorhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renameMover performs the move the way a working filesystem connector would.
type renameMover struct{}

func (renameMover) Move(_ context.Context, _, source, destination string) error {
	return os.Rename(source, destination)
}

// pressBody builds the one File request for the plan the picture shows now.
func pressBody(t *testing.T, h *Handler, batchID, key string, mutate func(map[string]any)) string {
	t.Helper()
	rec, body := serve(t, h, http.MethodGet, planTarget("file-janitor", batchID, ""), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body.String())
	}
	plan := body["plan"].(map[string]any)
	request := map[string]any{
		"expected_revision":       plan["revision"],
		"expected_group_revision": plan["group_revision"],
		"plan_digest":             plan["plan_digest"],
		"trash_acknowledged":      plan["trash_count"],
		"client_key":              key,
	}
	if mutate != nil {
		mutate(request)
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fileTarget(prefix, batchID string) string {
	return pathFor(prefix, "ws-1", "/batches/"+batchID+"/file")
}

const httpKey = "http-press-0123456789"

// R36, R41: one POST files the picture's plan; a retry on either prefix reports
// the first result; nothing private is in the answer.
func TestFileReviewPlanHTTP_FilesThePictureAndAnswersARetryOnEitherPrefix(t *testing.T) {
	h, root := pictureHandler(t, mixedPictureFiles(3))
	h.service.SetMover(renameMover{})
	_, picture, _ := readPicture(t, h, "file-janitor")
	batchID := picture["batch_id"].(string)
	request := pressBody(t, h, batchID, httpKey, nil)

	rec, body := serve(t, h, http.MethodPost, fileTarget("file-janitor", batchID), request)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	filing := body["filing"].(map[string]any)
	if filing["filed"] != float64(3) || filing["failed"] != float64(0) || len(filing["outcomes"].([]any)) != 3 || filing["replayed"] != nil {
		t.Fatalf("filing: %v", filing)
	}
	for _, private := range []string{root, "fingerprint", "token_hash", "scope"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatalf("the answer leaked %q", private)
		}
	}
	for i := range 3 {
		name := fmt.Sprintf("image-%04d.png", i)
		if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should have moved: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, "Filed", "Images", name)); err != nil {
			t.Fatalf("%s is not in Filed/Images: %v", name, err)
		}
	}

	// The same key on the other prefix answers from the journal: same filing,
	// flagged as a replay, and no file is touched again.
	replayRec, replayBody := serve(t, h, http.MethodPost, fileTarget("downloads-janitor", batchID), request)
	if replayRec.Code != http.StatusOK {
		t.Fatalf("replay: %d %s", replayRec.Code, replayRec.Body.String())
	}
	replay := replayBody["filing"].(map[string]any)
	if replay["replayed"] != true || replay["filing_id"] != filing["filing_id"] || replay["filed"] != float64(3) {
		t.Fatalf("replay: %v", replay)
	}
}

// R36: a plan that changed since the picture drew moves nothing, creates no
// folder, and hands back the fresh plan.
func TestFileReviewPlanHTTP_AChangedPlanMovesNothingAndReturnsTheFreshOne(t *testing.T) {
	h, root := pictureHandler(t, mixedPictureFiles(2))
	h.service.SetMover(renameMover{})
	_, picture, _ := readPicture(t, h, "file-janitor")
	batchID := picture["batch_id"].(string)
	request := pressBody(t, h, batchID, httpKey, nil)
	if err := os.WriteFile(filepath.Join(root, "image-0000.png"), []byte("changed after the picture drew"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec, body := serve(t, h, http.MethodPost, fileTarget("file-janitor", batchID), request)
	if rec.Code != http.StatusConflict || body["code"] != "plan_changed" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	plan := body["details"].(map[string]any)["plan"].(map[string]any)
	changed := plan["changed"].(map[string]any)
	if changed["total"] != float64(1) || changed["items"].([]any)[0].(map[string]any)["name"] != "image-0000.png" {
		t.Fatalf("the fresh plan must name the file that changed: %v", changed)
	}
	if _, err := os.Stat(filepath.Join(root, "image-0001.png")); err != nil {
		t.Fatalf("a refused press moved a file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed", "Images")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused press created a destination folder: %v", err)
	}
	// Pressing the refreshed plan files what is left.
	again, againBody := serve(t, h, http.MethodPost, fileTarget("file-janitor", batchID), pressBody(t, h, batchID, "http-press-second-01", nil))
	if again.Code != http.StatusOK || againBody["filing"].(map[string]any)["filed"] != float64(1) {
		t.Fatalf("%d %s", again.Code, again.Body.String())
	}
}

func TestFileReviewPlanHTTP_TrashNeedsItsCount(t *testing.T) {
	h, root := pictureHandler(t, mixedPictureFiles(2))
	h.service.SetMover(renameMover{})
	_, picture, _ := readPicture(t, h, "file-janitor")
	batchID := picture["batch_id"].(string)
	_, drafted := serve(t, h, http.MethodGet, pathFor("file-janitor", "ws-1", "/batches/"+batchID+"/draft"), "")
	draft := drafted["draft"].(map[string]any)
	choice := draft["choices"].([]any)[0].(map[string]any)["candidate_id"].(string)
	edit, _ := json.Marshal(map[string]any{
		"expected_revision": draft["revision"], "expected_group_revision": draft["group_revision"],
		"action": "mark_trash", "candidate_ids": []string{choice},
	})
	if rec, _ := serve(t, h, http.MethodPost, pathFor("file-janitor", "ws-1", "/batches/"+batchID+"/draft"), string(edit)); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	request := pressBody(t, h, batchID, httpKey, func(r map[string]any) { r["trash_acknowledged"] = 0 })
	rec, body := serve(t, h, http.MethodPost, fileTarget("file-janitor", batchID), request)
	if rec.Code != http.StatusConflict || body["code"] != "trash_not_acknowledged" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if plan := body["details"].(map[string]any)["plan"].(map[string]any); plan["trash_count"] != float64(1) {
		t.Fatalf("the plan must say how many are going to the Trash: %v", plan)
	}
	for i := range 2 {
		if _, err := os.Stat(filepath.Join(root, fmt.Sprintf("image-%04d.png", i))); err != nil {
			t.Fatalf("a press without the count moved a file: %v", err)
		}
	}
}

func TestFileReviewPlanHTTP_RefusesWhatItCannotAccept(t *testing.T) {
	h, _ := pictureHandler(t, mixedPictureFiles(1))
	h.service.SetMover(renameMover{})
	_, picture, _ := readPicture(t, h, "file-janitor")
	batchID := picture["batch_id"].(string)
	good := pressBody(t, h, batchID, httpKey, nil)

	for _, tc := range []struct {
		name, batch, body string
		code              int
		errorCode         string
	}{
		{"an unknown field", batchID, strings.Replace(good, `"client_key"`, `"path":"/etc","client_key"`, 1), http.StatusBadRequest, ""},
		{"two requests in one", batchID, good + good, http.StatusBadRequest, ""},
		{"not JSON", batchID, `{"expected_revision":`, http.StatusBadRequest, ""},
		{"too large", batchID, `{"client_key":"` + strings.Repeat("k", 5000) + `"}`, http.StatusBadRequest, ""},
		{"a retry key that is not a key", batchID, pressBody(t, h, batchID, "short", nil), http.StatusBadRequest, ""},
		{"latest is not a concrete review", "latest", good, http.StatusConflict, "review_unavailable"},
		{"an unknown review", "batch-does-not-exist", good, http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := serve(t, h, http.MethodPost, fileTarget("file-janitor", tc.batch), tc.body)
			if rec.Code != tc.code || (tc.errorCode != "" && body["code"] != tc.errorCode) {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
