package filejanitorhttp

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func planTarget(prefix, batchID, query string) string {
	target := pathFor(prefix, "ws-1", "/batches/"+batchID+"/plan")
	if query != "" {
		target += "?" + query
	}
	return target
}

// R34: the plan check works the same on both prefixes, says what filing would do,
// and writes nothing and creates no folder.
func TestReviewPlanHTTP_PlansTheTickedFilesAndWritesNothing(t *testing.T) {
	h, root := pictureHandler(t, mixedPictureFiles(3))
	_, picture, _ := readPicture(t, h, "file-janitor")
	batchID := picture["batch_id"].(string)
	statePath := pictureStatePath(h)
	before, err := os.ReadFile(statePath) // #nosec G304 -- test-owned state file under a temp dir
	if err != nil {
		t.Fatal(err)
	}

	var canonical map[string]any
	for _, prefix := range routePrefixPair {
		rec, body := serve(t, h, http.MethodGet, planTarget(prefix.prefix, batchID, "destination=images&limit=2"), "")
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", prefix.name, rec.Code, rec.Body.String())
		}
		if canonical != nil && !reflect.DeepEqual(canonical, body) {
			t.Fatal("canonical and legacy plan reads diverged")
		}
		canonical = body
		for _, private := range []string{root, "fingerprint", "token", "scope"} {
			if strings.Contains(rec.Body.String(), private) {
				t.Fatalf("%s: the plan leaked %q", prefix.name, private)
			}
		}
		plan := body["plan"].(map[string]any)
		if plan["move_count"] != float64(3) || plan["trash_count"] != float64(0) || plan["over_limit"] != false {
			t.Fatalf("%s: counts: %v", prefix.name, plan)
		}
		if digest, _ := plan["plan_digest"].(string); len(digest) != 64 {
			t.Fatalf("%s: a plan must quote a digest, got %q", prefix.name, plan["plan_digest"])
		}
		if plan["items_total"] != float64(3) || len(plan["items"].([]any)) != 2 {
			t.Fatalf("%s: the images page: %v", prefix.name, plan)
		}
		destinations := plan["destinations"].([]any)
		if len(destinations) != 1 || destinations[0].(map[string]any)["destination"] != "Filed/Images" {
			t.Fatalf("%s: destinations: %v", prefix.name, destinations)
		}
	}
	after, err := os.ReadFile(statePath) // #nosec G304 -- test-owned state file under a temp dir
	if err != nil || string(before) != string(after) {
		t.Fatal("the plan check changed the saved scan state", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed", "Images")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plan check created a destination folder: %v", err)
	}
}

func TestReviewPlanHTTP_RefusesWhatItCannotPlanHonestly(t *testing.T) {
	h, _ := pictureHandler(t, mixedPictureFiles(2))
	_, picture, _ := readPicture(t, h, "file-janitor")
	batchID := picture["batch_id"].(string)

	for _, tc := range []struct {
		name, batch, query string
		code               int
		errorCode          string
	}{
		{"scan changed since the plan was asked for", batchID, "revision=" + strings.Repeat("0", 64), http.StatusConflict, "review_conflict"},
		{"choices changed since the plan was asked for", batchID, "group_revision=" + strings.Repeat("0", 64), http.StatusConflict, "review_conflict"},
		{"unknown destination", batchID, "destination=nowhere", http.StatusBadRequest, "invalid_request"},
		{"negative offset", batchID, "destination=images&offset=-1", http.StatusBadRequest, "invalid_pagination"},
		{"latest is not a concrete scan", "latest", "", http.StatusConflict, "review_unavailable"},
		{"unknown scan", "batch-does-not-exist", "", http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := serve(t, h, http.MethodGet, planTarget("file-janitor", tc.batch, tc.query), "")
			if rec.Code != tc.code || (tc.errorCode != "" && body["code"] != tc.errorCode) {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// The picture learns the final name of each file it shows with one `ids` read; a
// malformed, repeated or oversized list, or one mixed with a destination, is
// refused rather than guessed at.
func TestReviewPlanHTTP_IdsFilterAnswersForExactlyThoseFiles(t *testing.T) {
	h, _ := pictureHandler(t, mixedPictureFiles(3))
	_, picture, _ := readPicture(t, h, "file-janitor")
	batchID := picture["batch_id"].(string)
	rec, body := serve(t, h, http.MethodGet, planTarget("file-janitor", batchID, "destination=images"), "")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	all := body["plan"].(map[string]any)["items"].([]any)
	if len(all) < 2 {
		t.Fatalf("need two planned files, got %v", all)
	}
	first := all[0].(map[string]any)["candidate_id"].(string)
	second := all[1].(map[string]any)["candidate_id"].(string)

	var canonical map[string]any
	for _, prefix := range routePrefixPair {
		rec, body := serve(t, h, http.MethodGet, planTarget(prefix.prefix, batchID, "ids="+first+","+second), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", prefix.name, rec.Code, rec.Body.String())
		}
		if canonical != nil && !reflect.DeepEqual(canonical, body) {
			t.Fatal("canonical and legacy ids reads diverged")
		}
		canonical = body
		items := body["plan"].(map[string]any)["items"].([]any)
		if len(items) != 2 || items[0].(map[string]any)["candidate_id"] == items[1].(map[string]any)["candidate_id"] {
			t.Fatalf("%s: exactly the two asked-for files: %v", prefix.name, items)
		}
		for _, item := range items {
			if item.(map[string]any)["final_name"] == "" {
				t.Fatalf("%s: a planned file must name its final name: %v", prefix.name, item)
			}
		}
	}

	for name, query := range map[string]string{
		"a repeated id":            "ids=" + first + "," + first,
		"an id that is not an id":  "ids=../etc",
		"ids with a destination":   "destination=images&ids=" + first,
		"more ids than a page has": "ids=" + strings.Repeat("a,", 200) + "a",
	} {
		rec, body := serve(t, h, http.MethodGet, planTarget("file-janitor", batchID, query), "")
		if rec.Code != http.StatusBadRequest || body["code"] != "invalid_request" {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

// R39: more than one press may file reports counts and nothing else.
func TestReviewPlanHTTP_OverTheLimitIsCountsOnly(t *testing.T) {
	files := make([]pictureFile, 1001)
	for i := range files {
		files[i] = pictureFile{fmt.Sprintf("image-%04d.png", i), 16}
	}
	h, _ := pictureHandler(t, files)
	_, picture, _ := readPicture(t, h, "file-janitor")
	rec, body := serve(t, h, http.MethodGet, planTarget("file-janitor", picture["batch_id"].(string), "destination=images"), "")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	plan := body["plan"].(map[string]any)
	if plan["over_limit"] != true || plan["move_count"] != float64(1001) || plan["plan_digest"] != "" {
		t.Fatalf("over limit: %v", plan)
	}
	if len(plan["destinations"].([]any)) != 0 || plan["items"] != nil {
		t.Fatalf("an over-limit plan lists nothing: %v", plan)
	}
}
