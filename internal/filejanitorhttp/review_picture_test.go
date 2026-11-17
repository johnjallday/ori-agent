package filejanitorhttp

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func pictureFilesTarget(prefix, query string) string {
	return pathFor(prefix, "ws-1", "/batches/latest/picture/files?"+query)
}

func pictureFileIDs(t *testing.T, body map[string]any) []string {
	t.Helper()
	page, _ := body["page"].(map[string]any)
	files, _ := page["files"].([]any)
	ids := make([]string, 0, len(files))
	for _, item := range files {
		file := item.(map[string]any)
		if file["placement"] != "filed" || file["name"] == "" {
			t.Fatalf("a filed file must say where it is and what it is called: %v", file)
		}
		ids = append(ids, file["id"].(string))
	}
	return ids
}

// R4: "and N more" opens the whole list for the folder, 50 at a time, on both
// prefixes, pinned to the revisions the person was looking at.
func TestReviewPictureFilesHTTP_PagesTheWholeFolder(t *testing.T) {
	h, _ := groupHandler(t, 206)
	_, picture, _ := readPicture(t, h, "file-janitor")
	pin := "&revision=" + picture["revision"].(string) + "&draft_revision=" + picture["draft_revision"].(string)

	for _, prefix := range routePrefixPair {
		seen := map[string]bool{}
		for _, tc := range []struct {
			query string
			want  int
		}{
			{"section=filed&key=images", 50},
			{"section=filed&key=images&limit=200", 200},
			{"section=filed&key=images&limit=200&offset=200", 6},
			{"section=filed&key=images&offset=9223372036854775807", 0},
		} {
			rec, body := serve(t, h, http.MethodGet, pictureFilesTarget(prefix.prefix, tc.query+pin), "")
			if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s %s: %d %s", prefix.name, tc.query, rec.Code, rec.Body.String())
			}
			ids := pictureFileIDs(t, body)
			if len(ids) != tc.want || body["page"].(map[string]any)["total"] != float64(206) {
				t.Fatalf("%s %s: got %d files, total %v", prefix.name, tc.query, len(ids), body["page"].(map[string]any)["total"])
			}
			if tc.want == 200 || tc.want == 6 {
				for _, id := range ids {
					if seen[id] {
						t.Fatalf("%s: a file appears on two pages", prefix.name)
					}
					seen[id] = true
				}
			}
		}
		if len(seen) != 206 {
			t.Fatalf("%s: paging reached %d of 206 files", prefix.name, len(seen))
		}
	}
}

func TestReviewPictureFilesHTTP_RefusesWhatItCannotPageHonestly(t *testing.T) {
	h, _ := groupHandler(t, 3)
	_, picture, _ := readPicture(t, h, "file-janitor")
	revision := picture["revision"].(string)
	// A revision that is certainly different, whatever its last digit is.
	otherDigit := "0"
	if strings.HasSuffix(revision, "0") {
		otherDigit = "1"
	}
	different := revision[:63] + otherDigit

	for _, tc := range []struct {
		name, query string
		code        int
		errorCode   string
	}{
		{"scan changed since the page was drawn", "section=filed&key=images&revision=" + different, http.StatusConflict, "review_changed"},
		{"choices changed since the page was drawn", "section=filed&key=images&draft_revision=stale", http.StatusConflict, "review_changed"},
		{"unknown section", "section=everything", http.StatusBadRequest, "invalid_request"},
		{"unknown folder", "section=filed&key=nowhere", http.StatusBadRequest, "invalid_request"},
		{"a folder key that is not a group", "section=loose&key=../../etc", http.StatusBadRequest, "invalid_request"},
		{"negative offset", "section=filed&key=images&offset=-1", http.StatusBadRequest, "invalid_pagination"},
		{"limit zero", "section=filed&key=images&limit=0", http.StatusBadRequest, "invalid_pagination"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := serve(t, h, http.MethodGet, pictureFilesTarget("file-janitor", tc.query), "")
			if rec.Code != tc.code || body["code"] != tc.errorCode {
				t.Fatalf("%s: %d %s", tc.query, rec.Code, rec.Body.String())
			}
		})
	}
}

// A scan with nothing waiting is a known empty picture; a workspace whose folder
// is not set up is unavailable. An error is never drawn as a tidy, empty folder.
// R26, R31: the picture's possible-copies row carries pair cards and its music row
// carries bundle cards, each with an opaque identifier; a card pages by that
// identifier, on both prefixes, pinned to the revisions on screen.
func TestReviewPictureHTTP_PairsAndBundlesPageByIdentifier(t *testing.T) {
	h, _ := pictureHandler(t, mixedPictureFiles(0))
	var canonical map[string]any
	for _, prefix := range routePrefixPair {
		_, picture, raw := readPicture(t, h, prefix.prefix)
		if canonical != nil && !reflect.DeepEqual(canonical, picture) {
			t.Fatal("canonical and legacy pictures diverged")
		}
		canonical = picture
		rows := map[string]map[string]any{}
		for _, item := range picture["loose"].([]any) {
			row := item.(map[string]any)
			rows[row["key"].(string)] = row
		}
		pairs := rows["versions"]["pairs"].([]any)
		bundles := rows["audio"]["bundles"].([]any)
		if len(pairs) != 1 || len(bundles) != 1 {
			t.Fatalf("%s: one pair and one bundle expected: %v %v", prefix.name, pairs, bundles)
		}
		pair, bundle := pairs[0].(map[string]any), bundles[0].(map[string]any)
		if pair["count"] != float64(3) || bundle["count"] != float64(2) || bundle["name"] != "Song" {
			t.Fatalf("%s: pair %v bundle %v", prefix.name, pair, bundle)
		}
		first := pair["files"].([]any)[0].(map[string]any)
		if first["name"] != "Budget.pdf" || first["ending"] != "none" || !strings.HasPrefix(first["family_id"].(string), "fam-") {
			t.Fatalf("%s: the original comes first with its ending and family: %v", prefix.name, first)
		}
		for _, id := range []string{pair["family_id"].(string), bundle["bundle_id"].(string)} {
			if strings.Contains(strings.ToLower(id), "budget") || strings.Contains(strings.ToLower(id), "song") {
				t.Fatalf("%s: identifier %q names a file", prefix.name, id)
			}
		}
		_ = raw

		pin := "&revision=" + picture["revision"].(string) + "&draft_revision=" + picture["draft_revision"].(string)
		for section, key := range map[string]string{"family": pair["family_id"].(string), "bundle": bundle["bundle_id"].(string)} {
			rec, body := serve(t, h, http.MethodGet, pictureFilesTarget(prefix.prefix, "section="+section+"&key="+key+"&limit=50"+pin), "")
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s: %d %s", prefix.name, section, rec.Code, rec.Body.String())
			}
			page := body["page"].(map[string]any)
			want := float64(3)
			if section == "bundle" {
				want = 2
			}
			if page["total"] != want || len(page["files"].([]any)) != int(want) {
				t.Fatalf("%s %s: the whole card, %v", prefix.name, section, page)
			}
		}
		rec, _ := serve(t, h, http.MethodGet, pictureFilesTarget(prefix.prefix, "section=family&key=../etc&limit=50"+pin), "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: a malformed key must be refused, got %d", prefix.name, rec.Code)
		}
	}
}

func TestReviewPictureHTTP_KnownEmptyIsNotUnavailable(t *testing.T) {
	empty, _ := groupHandler(t, 0)
	_, picture, _ := readPicture(t, empty, "file-janitor")
	if picture["batch_id"] != "" || picture["waiting"] != float64(0) || len(picture["folders"].([]any)) != 0 || picture["content_read"] != "none" {
		t.Fatalf("a tidy folder is a known empty picture: %v", picture)
	}
	rec, _ := serve(t, empty, http.MethodGet, pictureFilesTarget("file-janitor", "section=filed&key=images"), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("paging a scan that does not exist: %d", rec.Code)
	}

	unconfigured, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	rec, body := serve(t, unconfigured, http.MethodGet, pathFor("file-janitor", "ws-1", "/batches/latest/picture"), "")
	if rec.Code != http.StatusConflict || body["code"] != "review_unavailable" {
		t.Fatalf("an unconfigured folder must be unavailable, not an empty picture: %d %s", rec.Code, rec.Body.String())
	}
}
