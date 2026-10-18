package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// Saved-catalog domain evidence: not a browser discovery grant, installed Music
// prompt test, or a claim about live-model judgment. Both goals see the same
// inventory; the scripted model selects a different exact subset. The default
// 301-entry inventory still spans several search and review pages while the
// whole planning turn stays well inside the planner's 90-second deadline under
// the race detector on CI runners; ORI_ARRANGEMENT_PERF=1 runs the 1,001-entry
// inventory the envelope measurements use.
func TestArrangementPopulatedFlow_InvestigateReviewApplyRestart(t *testing.T) {
	total, clientCount := 301, 180
	if os.Getenv("ORI_ARRANGEMENT_PERF") == "1" {
		total, clientCount = 1001, 600
	}
	terminalID := fmt.Sprintf("entry-%04d", total-1)
	for _, tc := range []struct {
		label string
		count int
	}{{"Client", clientCount}, {"Experiment", total - clientCount}} {
		t.Run(tc.label, func(t *testing.T) {
			f, authority, _ := homeArrangementFixture(t)
			wanted := map[string]bool{}
			_, _, err := f.library.mutate(f.scope, f.doc(t).Revision, operation{key: "populated-fixture", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
				seed := doc.Entries[0]
				doc.Entries = nil
				for i := 0; i < total; i++ {
					entry := seed
					entry.ID = fmt.Sprintf("entry-%04d", i)
					label := "Experiment"
					if i < clientCount {
						label = "Client"
					}
					entry.Fields.DisplayName = fmt.Sprintf("%s %04d", label, i)
					doc.Entries = append(doc.Entries, entry)
					if label == tc.label {
						wanted[entry.ID] = true
					}
				}
				return "fixture", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before := f.doc(t)
			calls := 0
			goal := "Group only " + tc.label + " references; leave the rest catalog-only"
			provider := homePlanningProvider{chat: func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
				calls++
				raw, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				if len(raw) > workspacearrangement.MaxRequestBytes || strings.Contains(string(raw), before.Roots[0].Path) {
					t.Fatal("oversized or private request")
				}
				// The terminal catalog ID must not be dumped into overview or model history.
				if strings.Contains(string(raw), terminalID) {
					t.Fatal("full inventory leaked into model context")
				}
				reply := &llm.ChatResponse{Usage: llm.Usage{PromptTokens: 500, CompletionTokens: 100, TotalTokens: 600}}
				last := request.Messages[len(request.Messages)-1]
				switch calls {
				case 1:
					args, _ := json.Marshal(map[string]any{"filter": workspacearrangement.Filter{Text: tc.label}, "page_size": 20})
					reply.ToolCalls = []llm.ToolCall{{ID: "search", Name: "arrangement_search", Arguments: string(args)}}
				case 2:
					var page workspacearrangement.Page
					if last.Name != "arrangement_search" || json.Unmarshal([]byte(last.Content), &page) != nil || len(page.Rows) != 20 || page.Total != tc.count || page.NextCursor == "" {
						t.Fatal("search lost bounds/count", last)
					}
					for _, row := range page.Rows {
						if !wanted[row.ID] {
							t.Fatal("foreign search member", row.ID)
						}
					}
					args, _ := json.Marshal(map[string]any{"filter": workspacearrangement.Filter{Text: tc.label}})
					reply.ToolCalls = []llm.ToolCall{{ID: "select", Name: "arrangement_select", Arguments: string(args)}}
				case 3:
					var selection workspacearrangement.FrozenSelection
					if last.Name != "arrangement_select" || json.Unmarshal([]byte(last.Content), &selection) != nil || selection.Count != tc.count || selection.Handle == "" {
						t.Fatal("selection lost exact set", last)
					}
					draft := workspacearrangement.Draft{Version: 1, SourceRevision: selection.Revision, Goal: goal, Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.CreateCollection, Name: tc.label + " references", Members: workspacearrangement.Selection{Handle: selection.Handle}}}}
					args, _ := json.Marshal(draft)
					reply.ToolCalls = []llm.ToolCall{{ID: "propose", Name: "arrangement_propose", Arguments: string(args)}}
				default:
					t.Fatal("unexpected model call", calls)
				}
				return reply, nil
			}}
			var resolve ArrangementRuntimeResolver = func(ctx context.Context, actual ManagerAuthority) (ArrangementRuntime, error) {
				if actual != authority {
					t.Fatal("wrong Manager", actual)
				}
				return ArrangementRuntime{Provider: provider, Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64), Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
			}
			planned, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: goal, ExpectedRevision: before.Revision, ShareCatalogMetadata: true, AllowUpgrade: true})
			if err != nil {
				t.Fatal(err)
			}
			id := planned.History.Arrangement.ID
			afterPlan := f.doc(t)
			if calls != 3 || len(afterPlan.Collections) != 0 || !reflect.DeepEqual(before.Entries, afterPlan.Entries) {
				t.Fatal("planning executed effects")
			}
			members := afterPlan.Arrangements[0].Record.Draft.Operations[0].Members.IDs
			if len(members) != tc.count {
				t.Fatal("canonical selection truncated", len(members))
			}
			for _, id := range members {
				if !wanted[id] {
					t.Fatal("canonical selection widened", id)
				}
			}
			service := NewCollectionReviewService(f.library, resolve)
			review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, afterPlan.Revision, 1)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for offset := 0; offset < tc.count; offset += 50 {
				page, err := service.Page(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, review.Token, 0, offset)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(page)
				if len(raw) > 64<<10 || len(page.Members) > 50 || strings.Contains(string(raw), before.Roots[0].Path) {
					t.Fatal("unbounded/private review")
				}
				for _, member := range page.Members {
					if seen[member.ID] || !wanted[member.ID] {
						t.Fatal("duplicate/foreign review member", member.ID)
					}
					seen[member.ID] = true
				}
			}
			if !reflect.DeepEqual(seen, wanted) || len(f.doc(t).Collections) != 0 {
				t.Fatal("review lost exact membership or applied")
			}
			reviewDoc, _ := json.Marshal(f.doc(t))
			measureArrangementPages(t, "review", len(reviewDoc), tc.count, func(i int) error {
				page, err := service.Page(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, review.Token, 0, (i%((tc.count+49)/50))*50)
				if err == nil && (page.Total != tc.count || len(page.Members) > 50) {
					return fmt.Errorf("invalid review page")
				}
				return err
			})
			receipt, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, review.Token, "populated-apply", true)
			if err != nil {
				t.Fatal(err)
			}
			applied := f.doc(t)
			if calls != 3 || len(applied.Collections) != 1 || !reflect.DeepEqual(applied.Collections[0].EntryIDs, members) || !reflect.DeepEqual(before.Entries, applied.Entries) || !reflect.DeepEqual(before.Roots, applied.Roots) || !reflect.DeepEqual(before.Sessions, applied.Sessions) {
				t.Fatal("apply changed unselected catalog state")
			}
			restarted := NewStore(f.file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true })
			replay, err := NewCollectionReviewService(restarted, resolve).Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, review.Token, "populated-apply", true)
			if err != nil || !reflect.DeepEqual(receipt, replay) || !reflect.DeepEqual(applied, f.doc(t)) {
				t.Fatal("restart repeated or lost effects", err)
			}
			unavailable := NewStore(f.file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return false })
			catalog, err := unavailable.CollectionCatalog(t.Context(), f.scope, "", 0)
			if err != nil || len(catalog.Collections) != 1 {
				t.Fatal("provider loss hid catalog", err)
			}
			seen = map[string]bool{}
			for offset := 0; offset < tc.count; offset += 50 {
				page, err := unavailable.CollectionMembers(t.Context(), f.scope, applied.Collections[0].ID, catalog.ContextPin, offset)
				if err != nil {
					t.Fatal(err)
				}
				for _, member := range page.Members {
					if seen[member.ID] {
						t.Fatal("duplicate browse member")
					}
					seen[member.ID] = true
				}
			}
			appliedDoc, _ := json.Marshal(applied)
			measureArrangementPages(t, "browse-provider-unavailable", len(appliedDoc), tc.count, func(i int) error {
				page, err := unavailable.CollectionMembers(t.Context(), f.scope, applied.Collections[0].ID, catalog.ContextPin, (i%((tc.count+49)/50))*50)
				if err == nil && (page.Total != tc.count || len(page.Members) > 50) {
					return fmt.Errorf("invalid browse page")
				}
				return err
			})
			if !reflect.DeepEqual(seen, wanted) || !reflect.DeepEqual(applied, f.doc(t)) || calls != 3 {
				t.Fatal("read changed or lost membership")
			}
			// Source revocation preserves logical references. Forget is a
			// separately reviewed destructive metadata action, never a rescan.
			for _, root := range f.doc(t).Roots {
				revoke, err := f.roots.ReviewRevoke(f.scope, root.ID, f.doc(t).Revision)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := f.roots.CommitRevoke(f.scope, root.ID, revoke.Token, "populated-revoke-"+root.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := restarted.CollectionMembers(t.Context(), f.scope, applied.Collections[0].ID, catalog.ContextPin, 0); !errors.Is(err, ErrConflict) {
				t.Fatal("revocation did not invalidate old page", err)
			}
			revoked, err := restarted.CollectionCatalog(t.Context(), f.scope, "", 0)
			if err != nil || revoked.Collections[0].Members != tc.count {
				t.Fatal("revocation removed membership", err)
			}
			forgotten := map[string]bool{members[0]: true, members[len(members)-1]: true}
			for entryID := range forgotten {
				forget, err := f.library.ReviewForget(f.scope, entryID, f.doc(t).Revision)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.library.CommitForget(f.scope, entryID, forget.Token, "populated-forget-"+entryID); err != nil {
					t.Fatal(err)
				}
			}
			fresh, err := restarted.CollectionCatalog(t.Context(), f.scope, "", 0)
			if err != nil || fresh.Collections[0].ID != applied.Collections[0].ID || fresh.Collections[0].Members != tc.count-2 {
				t.Fatal("Forget changed collection identity/count", err)
			}
			seen = map[string]bool{}
			for offset := 0; offset < tc.count-2; offset += 50 {
				page, err := restarted.CollectionMembers(t.Context(), f.scope, applied.Collections[0].ID, fresh.ContextPin, offset)
				if err != nil {
					t.Fatal(err)
				}
				for _, member := range page.Members {
					if member.Availability != "revoked_source" {
						t.Fatal("revoked membership shown as available", member)
					}
					if seen[member.ID] || forgotten[member.ID] || !wanted[member.ID] {
						t.Fatal("Forget restored, duplicated or widened membership", member.ID)
					}
					seen[member.ID] = true
				}
			}
			if len(seen) != tc.count-2 {
				t.Fatal("Forget lost unrelated membership")
			}
			forgottenDoc := f.doc(t)
			// Old applied-operation replay may return redacted canonical history,
			// but it must not re-authorize revoked sources or restore membership.
			history, err := NewCollectionReviewService(restarted, resolve).Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, review.Token, "populated-apply", true)
			if err != nil || history == nil || !reflect.DeepEqual(forgottenDoc, f.doc(t)) || calls != 3 {
				t.Fatal("old receipt replay restored forgotten intent", err)
			}
		})
	}
}
