package filejanitorhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func groupHandler(t *testing.T, count int) (*Handler, string) {
	t.Helper()
	h, root := configuredHandler(t)
	ws, err := h.lookup.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace}}
	ws.AgentInstances = nil
	for i := range count {
		agedFile(t, root, fmt.Sprintf("image-%04d.png", i), 16)
	}
	if count > 0 {
		if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/scan", ""); rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
	}
	return h, root
}

func readGroupCard(t *testing.T, h *Handler) (map[string]any, map[string]any) {
	t.Helper()
	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/file-janitor/batches/latest/groups", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("groups: %d %s", rec.Code, rec.Body.String())
	}
	review := body["review"].(map[string]any)
	groups := review["groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected single group: %+v", review)
	}
	return review, groups[0].(map[string]any)
}

func TestReviewGroupReads_BoundsPagingAndAliasParity(t *testing.T) {
	h, _ := groupHandler(t, 206)
	review, card := readGroupCard(t, h)
	if card["count"] != float64(206) || len(card["samples"].([]any)) != 3 {
		t.Fatalf("card counts are not whole-group: %+v", card)
	}
	counts := review["counts"].(map[string]any)
	if counts["candidates"] != float64(206) || counts["filing"] != float64(206) {
		t.Fatalf("lost full counts: %+v", counts)
	}
	var canonical map[string]any
	for _, prefix := range routePrefixPair {
		base := pathFor(prefix.prefix, "ws-1", "/batches/"+review["batch_id"].(string)+"/groups")
		rec, body := serve(t, h, http.MethodGet, base, "")
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		if canonical != nil && !reflect.DeepEqual(canonical, body) {
			t.Fatal("canonical and legacy group reads diverged")
		}
		canonical = body
		if strings.Contains(rec.Body.String(), "candidate_ids") || strings.Contains(rec.Body.String(), "fingerprint") || strings.Contains(rec.Body.String(), "approval") || rec.Body.Len() > 2200 {
			t.Fatalf("unbounded/private card DTO: %s", rec.Body.String())
		}
		members := base + "/" + card["id"].(string) + "/members"
		for _, tc := range []struct {
			query string
			want  int
		}{
			{"", 50}, {"?limit=999999", 200}, {"?limit=200&offset=200", 6}, {"?offset=9223372036854775807", 0},
		} {
			rec, body := serve(t, h, http.MethodGet, members+tc.query, "")
			if rec.Code != http.StatusOK || len(candidateIDs(t, body)) != tc.want || body["total"] != float64(206) {
				t.Fatalf("%s %s: %d %s", prefix.name, tc.query, rec.Code, rec.Body.String())
			}
		}
		_, first := serve(t, h, http.MethodGet, members+"?limit=200", "")
		_, second := serve(t, h, http.MethodGet, members+"?limit=200&offset=200", "")
		seen := map[string]bool{}
		for _, body := range []map[string]any{first, second} {
			for _, id := range candidateIDs(t, body) {
				if seen[id] {
					t.Fatal("duplicate member across pages")
				}
				seen[id] = true
			}
		}
		if len(seen) != 206 {
			t.Fatal("off-page members unreachable")
		}
	}
}

func TestReviewGroupReads_RejectInvalidPagesForeignIDsAndRevisions(t *testing.T) {
	h, _ := groupHandler(t, 2)
	review, card := readGroupCard(t, h)
	for _, prefix := range routePrefixPair {
		base := pathFor(prefix.prefix, "ws-1", "/batches/"+review["batch_id"].(string)+"/groups")
		members := base + "/" + card["id"].(string) + "/members"
		for _, query := range []string{"?limit=0", "?limit=-1", "?offset=-1", "?limit=no", "?offset=2.5", "?offset=999999999999999999999999999999"} {
			rec, _ := serve(t, h, http.MethodGet, members+query, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("malformed %s: %d", query, rec.Code)
			}
		}
		for _, suffix := range []string{"/batches/foreign/groups", "/batches/" + review["batch_id"].(string) + "/groups/foreign/members"} {
			rec, _ := serve(t, h, http.MethodGet, pathFor(prefix.prefix, "ws-1", suffix), "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("foreign ID not hidden: %d %s", rec.Code, rec.Body.String())
			}
		}
		rec, body := serve(t, h, http.MethodGet, members+"?revision=old", "")
		if rec.Code != http.StatusConflict || body["code"] != "review_changed" {
			t.Fatalf("stale read not rejected: %d %s", rec.Code, rec.Body.String())
		}
		for _, suffix := range []string{"/groups", "/groups/foreign/members", "/groups/foreign/members?limit=no"} {
			rec, _ := serve(t, h, http.MethodGet, pathFor(prefix.prefix, "ws-other", "/batches/latest"+suffix), "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("ownership guard bypass: %d %s", rec.Code, rec.Body.String())
			}
		}
	}
}

func TestReviewBatchNavigation_BoundedSummariesAndOffPageCandidate(t *testing.T) {
	h, _ := groupHandler(t, 206)
	for _, prefix := range routePrefixPair {
		base := pathFor(prefix.prefix, "ws-1", "/batches")
		rec, _ := serve(t, h, http.MethodGet, base+"?summary_only=true", "")
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "candidate_ids") || strings.Contains(rec.Body.String(), "image-") {
			t.Fatalf("batch chooser leaked an inventory: %d %s", rec.Code, rec.Body.String())
		}
		_, last := serve(t, h, http.MethodGet, base+"/latest?limit=1&offset=205", "")
		id := candidateIDs(t, last)[0]
		rec, found := serve(t, h, http.MethodGet, base+"/latest?candidate_id="+id, "")
		if rec.Code != http.StatusOK || found["offset"] != float64(200) || found["linked_candidate_found"] != true || len(candidateIDs(t, found)) != 6 {
			t.Fatalf("off-page link did not locate its bounded page: %d %s", rec.Code, rec.Body.String())
		}
		_, missing := serve(t, h, http.MethodGet, base+"/latest?candidate_id=foreign", "")
		if missing["linked_candidate_found"] != false || len(candidateIDs(t, missing)) != 50 {
			t.Fatal("missing deep link must retain the working bounded review")
		}
	}
}

func TestReviewGroupReads_ReadOnlyAndUnavailableNotEmpty(t *testing.T) {
	h, root := groupHandler(t, 2)
	store := h.lookup.(*fakeStore)
	statePath := store.root + "/ws-1/downloads-janitor/scan-state.json"
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-away"); err != nil {
		t.Fatal(err)
	}
	review, card := readGroupCard(t, h)
	target := "/api/workspaces/ws-1/file-janitor/batches/" + review["batch_id"].(string) + "/groups/" + card["id"].(string) + "/members"
	rec, _ := serve(t, h, http.MethodGet, target, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), root) {
		t.Fatalf("saved-only member read: %d %s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(statePath)
	if err != nil || string(before) != string(after) {
		t.Fatal("GET persisted draft/approval/scan/candidate changes", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"broken":`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/file-janitor/batches/latest/groups", "")
	if rec.Code != http.StatusConflict || body["code"] != "review_unavailable" {
		t.Fatalf("corrupt review reported empty success: %d %s", rec.Code, rec.Body.String())
	}
	if err := os.WriteFile(statePath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	ws, _ := h.lookup.Get("ws-1")
	ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor)
	rec, _ = serve(t, h, http.MethodGet, "/api/workspaces/ws-1/file-janitor/batches/latest/groups", "")
	if rec.Code != http.StatusConflict {
		t.Fatal("removed capability served old review")
	}
	empty, _ := groupHandler(t, 0)
	rec, body = serve(t, empty, http.MethodGet, "/api/workspaces/ws-1/file-janitor/batches/latest/groups", "")
	data, _ := json.Marshal(body)
	if rec.Code != http.StatusOK || !strings.Contains(string(data), `"groups":[]`) {
		t.Fatalf("known empty is not success: %d %s", rec.Code, data)
	}
	unconfigured, _ := newTestHandler(t, map[string]string{"ws-1": "local"})
	rec, _ = serve(t, unconfigured, http.MethodGet, "/api/workspaces/ws-1/file-janitor/batches/latest/groups", "")
	if rec.Code != http.StatusConflict {
		t.Fatal("unconfigured/attached-only must not be known empty")
	}
}
