package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestStaffingAssignmentEvidenceUsesOriginalSourceAndObservedOwnerRoleOnly(t *testing.T) {
	for _, scenario := range []string{"active", "foreign_owner", "trashed", "cleared", "replaced_instance", "source_changed", "restart", "corrupt_original_review", "unsafe_label"} {
		t.Run(scenario, func(t *testing.T) {
			h, workspaces, id, revision, cleanup := newGroupTemplateHomeHandler(t)
			defer cleanup()
			profilesPath := filepath.Join(t.TempDir(), "agents.json")
			profiles, err := store.NewFileStore(profilesPath, types.Settings{})
			if err != nil {
				t.Fatal(err)
			}
			h.SetAgentStore(profiles)
			f := newBuildFixtureOn(t, h, false)
			if err := profiles.UpdateAgent("Scout", func(profile *agent.Agent) error {
				profile.Settings.Provider, profile.Settings.Model = "scripted", "scripted-1"
				profile.Metadata = &types.AgentMetadata{Description: "Coordinates reviewed portfolio priorities and project handoffs."}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			receipts, err := grouprequirements.NewSQLiteStore(h.store.DB())
			if err != nil {
				t.Fatal(err)
			}
			h.SetGroupRequirementService(grouprequirements.NewService(workspaces, receipts), func(context.Context) (string, error) { return "local", nil })
			adapter := wireReviewedGroupStaffing(t, h, workspaces)
			request := map[string]any{"group_template_id": id, "revision": revision, "name": "Reviewed collection", "role_staffing": []groupHomeRoleChoice{{RoleID: "home-lead", Mode: "assign", Name: "Scout"}}}
			code, body := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, request))
			if code != http.StatusOK {
				t.Fatalf("review %d", code)
			}
			token := body["group_template_review"].(map[string]any)["review_token"].(string)
			request["group_review_token"], request["idempotency_key"] = token, "assignment-evidence"
			code, body = postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, request))
			if code != http.StatusOK {
				t.Fatalf("commit %d", code)
			}
			homeID := body["group_template"].(map[string]any)["home_workspace_id"].(string)
			if scenario == "foreign_owner" {
				if _, err := h.store.DB().Exec(`INSERT INTO users(id,created_at,updated_at) VALUES('someone-else',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "foreign_owner", "trashed", "replaced_instance", "unsafe_label":
				if err := workspaces.Update(homeID, func(current *workspace.Workspace) error {
					switch scenario {
					case "foreign_owner":
						current.OwnerUserID = "someone-else"
					case "trashed":
						current.Status = workspace.StatusTrashed
					case "replaced_instance":
						current.AgentInstances[0].ID = "replacement-instance"
					case "unsafe_label":
						current.Name = "PRIVATE_PATH_CANARY /private/work"
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "cleared":
				if err := adapter.UnstaffRoleFromWorkspace(t.Context(), homeID, "home-lead"); err != nil {
					t.Fatal(err)
				}
			case "source_changed":
				if err := profiles.UpdateAgent("Scout", func(profile *agent.Agent) error { profile.Settings.SystemPrompt = "Changed source"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "restart":
				restarted, err := store.NewFileStore(profilesPath, types.Settings{})
				if err != nil {
					t.Fatal(err)
				}
				h.SetAgentStore(restarted)
			case "corrupt_original_review":
				if _, err := h.store.DB().Exec(`UPDATE group_requirement_reviews SET payload_json=json_set(payload_json,'$.home_staffing.roles[0].source_witness','forged-source') WHERE token=?`, token); err != nil {
					t.Fatal(err)
				}
			}
			// The observer may make nested receipt reads only after releasing its
			// read cursor. Exercise the production database's strictest pool.
			h.store.DB().SetMaxOpenConns(1)
			before := len(h.agentStore.ListAgents())
			roster := h.workspaceBuildRoster(t.Context(), "local")
			facts := 0
			for _, candidate := range roster.Candidates {
				if candidate.Name != "Scout" {
					continue
				}
				for _, fact := range candidate.Evidence {
					if fact.Kind == "current_assignment" {
						facts++
						if fact.Value != "home-lead — Reviewed collection" {
							t.Fatal("unexpected assignment disclosure")
						}
					}
				}
			}
			want := 0
			if scenario == "active" {
				want = 1
			}
			encoded, err := json.Marshal(roster)
			if err != nil || strings.Contains(string(encoded), "PRIVATE_PATH_CANARY") || strings.Contains(string(encoded), "forged-source") {
				t.Fatal("private or unverified metadata escaped", err)
			}
			if facts != want || roster.Coverage.AssignmentsComplete || len(h.agentStore.ListAgents()) != before || f.provider.calls != 0 {
				t.Fatalf("assignment proof mismatch: scenario=%s facts=%d want=%d", scenario, facts, want)
			}
			foreign, complete := h.reviewedHomeAssignments(t.Context(), "someone-else")
			if len(foreign) != 0 || complete {
				t.Fatal("foreign owner acquired original review provenance")
			}
		})
	}
}
