package filejanitorhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// These tests state the HTTP rules of the "Picture review" contract in
// docs/architecture/file-janitor-group-review.md. They were written before the
// code and shown failing; they now guard it.

type pictureFile struct {
	name string
	size int
}

// pictureHandler is groupHandler for a mixed folder: the sizes matter for the
// pair hints, and the names cover every group kind the picture draws.
func pictureHandler(t *testing.T, files []pictureFile) (*Handler, string) {
	t.Helper()
	h, root := configuredHandler(t)
	ws, err := h.lookup.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace}}
	ws.AgentInstances = nil
	for _, file := range files {
		agedFile(t, root, file.name, file.size)
	}
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/scan", ""); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	return h, root
}

func mixedPictureFiles(images int) []pictureFile {
	files := make([]pictureFile, 0, images+9)
	for i := range images {
		files = append(files, pictureFile{fmt.Sprintf("image-%04d.png", i), 16})
	}
	return append(files,
		pictureFile{"passport-a.pdf", 20}, pictureFile{"passport-b.pdf", 21},
		pictureFile{"Budget.pdf", 30}, pictureFile{"Budget (2).pdf", 30}, pictureFile{"Budget-v2.pdf", 31},
		pictureFile{"Song.wav", 40}, pictureFile{"Song.als", 41}, pictureFile{"solo.mp3", 42},
		pictureFile{"mystery.qqq", 5},
	)
}

func pictureStatePath(h *Handler) string {
	return filepath.Join(h.lookup.(*fakeStore).root, "ws-1", "downloads-janitor", "scan-state.json")
}

func readPicture(t *testing.T, h *Handler, prefix string) (*http.Response, map[string]any, string) {
	t.Helper()
	rec, body := serve(t, h, http.MethodGet, pathFor(prefix, "ws-1", "/batches/latest/picture"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("picture (%s): %d %s", prefix, rec.Code, rec.Body.String())
	}
	picture, _ := body["picture"].(map[string]any)
	if picture == nil {
		t.Fatalf("picture (%s): no picture in %s", prefix, rec.Body.String())
	}
	return rec.Result(), picture, rec.Body.String()
}

// R10, R12: opening the review shows the confident folders ticked, on both route
// prefixes, and the reads write nothing and create no folder.
func TestReviewDraftHTTP_TickedOnOpenReadsNeverWrite(t *testing.T) {
	h, root := pictureHandler(t, mixedPictureFiles(3))
	statePath := pictureStatePath(h)
	before, err := os.ReadFile(statePath) // #nosec G304 -- test-owned state file under a temp dir
	if err != nil {
		t.Fatal(err)
	}
	_, review := serve(t, h, http.MethodGet, pathFor("file-janitor", "ws-1", "/batches/latest/groups"), "")
	batchID := review["review"].(map[string]any)["batch_id"].(string)

	var canonical map[string]any
	for _, prefix := range routePrefixPair {
		rec, body := serve(t, h, http.MethodGet, pathFor(prefix.prefix, "ws-1", "/batches/"+batchID+"/draft"), "")
		draft, _ := body["draft"].(map[string]any)
		if rec.Code != http.StatusOK || draft["state"] != "default" || len(draft["choices"].([]any)) != 3 {
			t.Fatalf("%s: an untouched scan must open with its three images ticked: %d %s", prefix.name, rec.Code, rec.Body.String())
		}
		for _, choice := range draft["choices"].([]any) {
			if choice.(map[string]any)["category"] != "images" {
				t.Fatalf("%s: only the confident folder is ticked: %v", prefix.name, choice)
			}
		}
		if canonical != nil && !reflect.DeepEqual(canonical, body) {
			t.Fatal("canonical and legacy draft reads diverged")
		}
		canonical = body
	}

	after, err := os.ReadFile(statePath) // #nosec G304 -- test-owned state file under a temp dir
	if err != nil || string(before) != string(after) {
		t.Fatal("opening the review changed the saved scan state", err)
	}
	// Setup creates the Filed folder itself; opening must not add a category
	// folder under it.
	if _, err := os.Stat(filepath.Join(root, "Filed", "Images")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("opening the review created a destination folder: %v", err)
	}
}

// The picture read is a read like the others: it writes nothing.
func TestReviewPictureHTTP_ReadsNeverWrite(t *testing.T) {
	h, root := pictureHandler(t, mixedPictureFiles(3))
	statePath := pictureStatePath(h)
	before, err := os.ReadFile(statePath) // #nosec G304 -- test-owned state file under a temp dir
	if err != nil {
		t.Fatal(err)
	}
	readPicture(t, h, "file-janitor")
	readPicture(t, h, "downloads-janitor")
	after, err := os.ReadFile(statePath) // #nosec G304 -- test-owned state file under a temp dir
	if err != nil || string(before) != string(after) {
		t.Fatal("reading the picture changed the saved scan state", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed", "Images")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading the picture created a destination folder: %v", err)
	}
}

// R4, R8, R64, R69: the first screenful is one bounded read. A 206-file folder
// card holds five names and the whole count; every waiting file is in exactly one
// place; nothing private or unbounded is sent; both prefixes answer the same.
func TestReviewPictureHTTP_FirstScreenfulBoundedNoPaths(t *testing.T) {
	h, root := pictureHandler(t, mixedPictureFiles(206))
	var canonical map[string]any
	for _, prefix := range routePrefixPair {
		resp, picture, raw := readPicture(t, h, prefix.prefix)
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: picture can be cached by shared intermediaries", prefix.name)
		}
		if canonical != nil && !reflect.DeepEqual(canonical, picture) {
			t.Fatal("canonical and legacy picture reads diverged")
		}
		canonical = picture
		for _, private := range []string{root, "fingerprint", "token", "scope_digest", "candidate_ids", "approval"} {
			if strings.Contains(raw, private) {
				t.Fatalf("%s: picture leaked %q", prefix.name, private)
			}
		}
		if len(raw) > 30000 {
			t.Fatalf("%s: picture is %d bytes; the first screenful must be bounded", prefix.name, len(raw))
		}
		if id, _ := picture["batch_id"].(string); id == "" || id == "latest" {
			t.Fatalf("%s: picture must name the concrete batch, got %q", prefix.name, id)
		}
		if picture["draft_state"] != "default" || picture["waiting"] != float64(206+9) {
			t.Fatalf("%s: state or waiting count wrong: %v %v", prefix.name, picture["draft_state"], picture["waiting"])
		}

		folders, _ := picture["folders"].([]any)
		if len(folders) != 1 {
			t.Fatalf("%s: expected one folder card (images), got %d", prefix.name, len(folders))
		}
		card := folders[0].(map[string]any)
		if card["category"] != "images" || card["count"] != float64(206) || len(card["files"].([]any)) != 5 {
			t.Fatalf("%s: a 206-file folder card keeps its whole count and five names: %+v", prefix.name, card)
		}

		total := 0
		for _, item := range folders {
			folder := item.(map[string]any)
			total += int(folder["count"].(float64)) + int(numberOr(folder["unfiled_count"]))
		}
		loose, _ := picture["loose"].([]any)
		for _, item := range loose {
			row := item.(map[string]any)
			if n := len(asSlice(row["singles"])); n > 12 {
				t.Fatalf("%s: loose row sends %d single files, limit 12", prefix.name, n)
			}
			if n := len(asSlice(row["pairs"])); n > 3 {
				t.Fatalf("%s: loose row sends %d pairs, limit 3", prefix.name, n)
			}
			if n := len(asSlice(row["bundles"])); n > 3 {
				t.Fatalf("%s: loose row sends %d bundles, limit 3", prefix.name, n)
			}
			// A row's whole count is what is still loose plus what was placed in a
			// folder or marked for Trash; those two are drawn elsewhere.
			if row["count"] != row["loose_count"].(float64)+numberOr(row["placed_count"])+numberOr(row["trash_count"]) {
				t.Fatalf("%s: a loose row does not add up: %v", prefix.name, row)
			}
			total += int(row["loose_count"].(float64))
		}
		for _, key := range []string{"left_out", "trash", "could_not_file"} {
			if section, ok := picture[key].(map[string]any); ok {
				total += int(numberOr(section["count"]))
			}
		}
		if total != 206+9 {
			t.Fatalf("%s: sections add up to %d, want every waiting file exactly once (%d)", prefix.name, total, 206+9)
		}
	}
}

// R2: the third lead-line sentence must be true for this scan, so the server
// states whether any file was sorted by reading inside it.
func TestReviewPictureHTTP_ContentReadFact(t *testing.T) {
	h, _ := pictureHandler(t, []pictureFile{{"one.png", 16}, {"two.pdf", 16}})
	_, picture, _ := readPicture(t, h, "file-janitor")
	if picture["content_read"] != "none" {
		t.Fatalf("a metadata scan must report none, got %v", picture["content_read"])
	}
	setClassifier := func(value string) {
		statePath := pictureStatePath(h)
		data, err := os.ReadFile(statePath) // #nosec G304 -- test-owned state file under a temp dir
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		candidates, _ := raw["candidates"].([]any)
		for i, item := range candidates {
			candidate := item.(map[string]any)
			if i == 0 {
				if value == "" {
					delete(candidate, "classifier")
				} else {
					candidate["classifier"] = value
				}
			}
		}
		out, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setClassifier("content")
	if _, picture, _ = readPicture(t, h, "file-janitor"); picture["content_read"] != "some" {
		t.Fatalf("a scan with one content-sorted file must report some, got %v", picture["content_read"])
	}
	setClassifier("")
	if _, picture, _ = readPicture(t, h, "file-janitor"); picture["content_read"] != "unknown" {
		t.Fatalf("a candidate with no provenance must report unknown, got %v", picture["content_read"])
	}
}

// Decision (f): pairs and bundles are identified by opaque, stable IDs and a
// kind of name ending, with no name in any ID and no change to membership.
func TestReviewGroupMembersHTTP_PairAndBundleIDs(t *testing.T) {
	h, _ := pictureHandler(t, mixedPictureFiles(0))
	_, review := serve(t, h, http.MethodGet, pathFor("file-janitor", "ws-1", "/batches/latest/groups"), "")
	batchID := review["review"].(map[string]any)["batch_id"].(string)
	families := regexp.MustCompile(`^fam-[0-9a-f]{24}$`)
	bundles := regexp.MustCompile(`^bun-[0-9a-f]{24}$`)

	byName := map[string]map[string]any{}
	for _, group := range review["review"].(map[string]any)["groups"].([]any) {
		id := group.(map[string]any)["id"].(string)
		rec, body := serve(t, h, http.MethodGet, pathFor("file-janitor", "ws-1", "/batches/"+batchID+"/groups/"+id+"/members?limit=200"), "")
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		for _, item := range body["candidates"].([]any) {
			member := item.(map[string]any)
			byName[member["name"].(string)] = member
		}
	}
	budget, copyOf, v2 := byName["Budget.pdf"], byName["Budget (2).pdf"], byName["Budget-v2.pdf"]
	if budget == nil || copyOf == nil || v2 == nil {
		t.Fatalf("setup: the Budget family is not in the group reads: %v", byName)
	}
	family, _ := budget["family_id"].(string)
	if !families.MatchString(family) || copyOf["family_id"] != family || v2["family_id"] != family {
		t.Fatalf("the three Budget files must share one opaque family ID: %v %v %v", budget["family_id"], copyOf["family_id"], v2["family_id"])
	}
	if budget["ending"] != "none" || copyOf["ending"] != "number" || v2["ending"] != "v" {
		t.Fatalf("endings: %v %v %v", budget["ending"], copyOf["ending"], v2["ending"])
	}
	wav, als := byName["Song.wav"], byName["Song.als"]
	bundle, _ := wav["bundle_id"].(string)
	if !bundles.MatchString(bundle) || als["bundle_id"] != bundle {
		t.Fatalf("Song.* must share one opaque bundle ID: %v %v", wav["bundle_id"], als["bundle_id"])
	}
	if solo := byName["solo.mp3"]; solo["bundle_id"] != nil && solo["bundle_id"] != "" {
		t.Fatalf("a song with no matching file must not get a bundle: %v", solo["bundle_id"])
	}
	for _, id := range []string{family, bundle} {
		for _, name := range []string{"budget", "song"} {
			if strings.Contains(strings.ToLower(id), name) {
				t.Fatalf("identifier %q carries a file name", id)
			}
		}
	}
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func numberOr(v any) float64 {
	n, _ := v.(float64)
	return n
}
