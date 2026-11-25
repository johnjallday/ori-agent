package secondbrain

import (
	"reflect"
	"testing"
)

func TestInboxPassages(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{"Maya said disposal costs extra.", nil},
		{"Maya said disposal costs extra. Compare both roofing quotes before Friday.", []string{"Compare both roofing quotes before Friday"}},
		{"Ask Maya about disposal. Call Alex. Call Alex.", []string{"Ask Maya about disposal", "Call Alex"}},
		{"Maya said: “Compare the quotes.”", nil}, {"Do not compare the quotes.", nil}, {"Review was already completed.", nil}, {"- [ ] Compare both quotes", nil}, {"TODO Call Alex.", nil}, {"```\nCall Alex.\n```", nil},
	} {
		var titles []string
		seen := map[string]bool{}
		for _, a := range InboxPassages("k", tc.body+"\n\nCaptured by Ori on 2026-10-21.\n") {
			titles = append(titles, a.Title)
			if seen[a.ID] {
				t.Fatal("duplicate")
			}
			seen[a.ID] = true
		}
		if !reflect.DeepEqual(titles, tc.want) {
			t.Errorf("%q: got %v want %v", tc.body, titles, tc.want)
		}
	}
}
