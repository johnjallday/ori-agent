package filejanitorhttp

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

func TestReviewKeepHTTP_AliasesTargetedForgetAndRememberedStatus(t *testing.T) {
	for _, prefix := range routePrefixPair {
		t.Run(prefix.name, func(t *testing.T) {
			h, _ := groupHandler(t, 2)
			review, card := readGroupCard(t, h)
			batchID := review["batch_id"].(string)
			batchPath := pathFor(prefix.prefix, "ws-1", "/batches/"+batchID)
			post := func(request filejanitor.ReviewDraftEdit) {
				t.Helper()
				data, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				rec, _ := serve(t, h, http.MethodPost, batchPath+"/draft", string(data))
				if rec.Code != http.StatusOK {
					t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
				}
			}
			post(draftRequest(t, h, batchID, "keep_group", card["id"].(string)))
			_, body := serve(t, h, http.MethodGet, batchPath+"?filter=skipped", "")
			kept := body["candidates"].([]any)
			if len(kept) != 2 || kept[0].(map[string]any)["skip_remembered"] != true || kept[1].(map[string]any)["skip_remembered"] != true {
				t.Fatal("keep status not backed by skip storage", body)
			}
			id := kept[0].(map[string]any)["id"].(string)
			reconsider := draftRequest(t, h, batchID, "reconsider_files", "")
			reconsider.CandidateIDs = []string{id}
			post(reconsider)
			_, body = serve(t, h, http.MethodGet, batchPath+"?filter=skipped", "")
			kept = body["candidates"].([]any)
			for _, raw := range kept {
				c := raw.(map[string]any)
				if c["state"] != "skipped" || c["skip_remembered"] != (c["id"] != id) {
					t.Fatal("forget revived a candidate or cleared unrelated skip", c)
				}
			}
			_, draft := serve(t, h, http.MethodGet, batchPath+"/draft", "")
			if len(draft["draft"].(map[string]any)["choices"].([]any)) != 0 {
				t.Fatal("forget selected a candidate")
			}
			rec, _ := serve(t, h, http.MethodPost, pathFor(prefix.prefix, "ws-1", "/skipped/reset"), `{}`)
			if rec.Code != http.StatusOK {
				t.Fatal("legacy whole-skip reset broke", rec.Body.String())
			}
		})
	}
}
