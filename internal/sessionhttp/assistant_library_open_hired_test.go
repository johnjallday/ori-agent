package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// assistant-reads-projects 3.3 (FR16): a Home set up before Homes hired their
// assistant is offered it once. Yes records the approval and hires it before
// any song; Not now is remembered and grants nothing.
func TestLibrarySharingOffer_AcceptHiresAndDismissIsRemembered(t *testing.T) {
	t.Run("accept", func(t *testing.T) {
		f := newOpenFixture(t)
		var created []*agent.AssistantSetupProvenance
		f.handler.SetSharedAgentCreator(func(_ context.Context, _ agentworkspace.AssistantProgramRoleSpec, name string, marker *agent.AssistantSetupProvenance) (bool, error) {
			f.agents.mu.Lock()
			f.agents.names[name] = true
			f.agents.mu.Unlock()
			created = append(created, marker)
			return true, nil
		})
		if view := f.sharing(t); view.State != sharingNone || view.Hired || view.OfferDismissed {
			t.Fatalf("before = %+v", view)
		}
		code, payload := f.post(t, "/sharing", `{"request_id":"offer-yes","enabled":true,"hire":true,"team_digest":"`+openDigest+`"}`, f.handler.SetAssistantLibrarySharing)
		sharing, _ := payload["sharing"].(map[string]any)
		if code != http.StatusOK || sharing["state"] != sharingOn || sharing["hired"] != true || sharing["agent_name"] != "REAPER Assistant" {
			t.Fatalf("accept = %d %v", code, payload)
		}
		consent, _ := f.staffs.Consents().Read(openHomeID)
		if consent.Source != agentworkspace.ProjectStaffingConsentHomeSwitch || consent.Roles[0].AgentName != "REAPER Assistant" || len(created) != 1 {
			t.Fatalf("consent = %+v, creates %d", consent, len(created))
		}
		// Pressing it again changes nothing.
		if code, _ := f.post(t, "/sharing", `{"request_id":"offer-yes-2","enabled":true,"hire":true,"team_digest":"`+openDigest+`"}`, f.handler.SetAssistantLibrarySharing); code != http.StatusOK || len(created) != 1 {
			t.Fatalf("again = %d, creates %d", code, len(created))
		}
		// The first song binds it.
		if code, opened := f.open(t, openHomeID, "two", `{"request_id":"r2"}`); code != http.StatusOK || opened["staffing"] != "joined" {
			t.Fatalf("open = %d %v", code, opened)
		}
	})
	t.Run("accept with no model writes nothing", func(t *testing.T) {
		f := newOpenFixture(t)
		f.handler.staffingModelReady = func() bool { return false }
		f.handler.SetSharedAgentCreator(func(context.Context, agentworkspace.AssistantProgramRoleSpec, string, *agent.AssistantSetupProvenance) (bool, error) {
			t.Fatal("created with no model")
			return false, nil
		})
		code, payload := f.post(t, "/sharing", `{"request_id":"offer-yes","enabled":true,"hire":true,"team_digest":"`+openDigest+`"}`, f.handler.SetAssistantLibrarySharing)
		if code != http.StatusConflict || payload["reason"] != "needs_model" {
			t.Fatalf("no model = %d %v", code, payload)
		}
	})
	t.Run("dismiss", func(t *testing.T) {
		f := newOpenFixture(t)
		for _, id := range []string{"not-now", "not-now-again"} {
			code, payload := f.post(t, "/sharing/offer", `{"request_id":"`+id+`"}`, f.handler.DismissAssistantLibrarySharingOffer)
			sharing, _ := payload["sharing"].(map[string]any)
			if code != http.StatusOK || sharing["offer_dismissed"] != true || sharing["state"] != sharingNone {
				t.Fatalf("dismiss %s = %d %v", id, code, payload)
			}
		}
		if consent, _ := f.staffs.Consents().Read(openHomeID); consent != nil || len(f.sharedAgents()) != 0 {
			t.Fatalf("a dismissal granted or hired: %+v %v", consent, f.sharedAgents())
		}
	})
}

// assistant-reads-projects 3.4 (FR18): every refusal of the staffing decision
// reaches the page as one of the outcomes libraryOpenedMessage words.
func TestStateForStaffingErr_EveryOutcomeThePageWords(t *testing.T) {
	now := time.Now()
	revoked := &agentworkspace.ProjectStaffingConsent{RevokedAt: &now}
	for name, tc := range map[string]struct {
		err     error
		consent *agentworkspace.ProjectStaffingConsent
		want    string
	}{
		"never asked":       {projectstaffing.ErrNoConsent, nil, "none"},
		"switched off":      {projectstaffing.ErrNoConsent, revoked, "off"},
		"older team":        {projectstaffing.ErrConsentStale, nil, "consent_stale"},
		"assistant missing": {projectstaffing.ErrAssistantMissing, nil, "assistant_missing"},
		"name held":         {projectstaffing.ErrReservationHeld, nil, "reconcile"},
		"not shared":        {projectstaffing.ErrNotShared, nil, "not_shared"},
		"invalid record":    {agentworkspace.ErrProjectStaffingConsentInvalid, nil, "invalid"},
		"anything else":     {errors.New("disk full"), nil, "failed"},
	} {
		if got := stateForStaffingErr(tc.err, tc.consent); got != tc.want {
			t.Errorf("%s = %q, want %q", name, got, tc.want)
		}
	}
	// A model the create needs is its own outcome, decided before the staffing
	// decision runs: Open says so instead of adding nobody silently.
	f := newOpenFixture(t)
	f.grant(t)
	f.handler.staffingModelReady = func() bool { return false }
	code, payload := f.open(t, openHomeID, "two", `{"request_id":"r2"}`)
	if payload["staffing"] != "needs_model" && payload["reason"] != "needs_model" && payload["needs_model"] != true {
		t.Fatalf("open with no model = %d %v", code, payload)
	}
}

// assistant-reads-projects 3.1 (FR17): the assistant hired when the folder was
// set up, before any song existed, is the one every song opened from the shelf
// binds. One agent, the same name, in two songs; nothing new is created.
func TestOpenLibrarySong_BindsTheAssistantHiredAtSetup(t *testing.T) {
	f := newOpenFixture(t)
	team := projectstaffing.Team{PluginID: "reaper-plugin", BlueprintID: "reaper-song", Digest: openDigest,
		Roles: []agentworkspace.AssistantProgramRoleSpec{{ID: "reaper-assistant", Label: "REAPER Assistant",
			Scope: agentworkspace.AssistantRoleScopeProject, Required: true, Primary: true}}}
	create := func(_ context.Context, _ agentworkspace.AssistantProgramRoleSpec, name string, _ *agent.AssistantSetupProvenance) (bool, error) {
		f.agents.mu.Lock()
		defer f.agents.mu.Unlock()
		if f.agents.names[name] {
			return false, errors.New("a second agent named " + name + " was created")
		}
		f.agents.names[name] = true
		return true, nil
	}
	if err := f.staffs.WithHome(openHomeID, func(tx *projectstaffing.Tx) error {
		_, err := tx.HireShared(context.Background(), team,
			projectstaffing.Grant{Source: agentworkspace.ProjectStaffingConsentFolderOffer, OfferID: "folder-agent-ws"}, true, create)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.sharedAgents(); len(got) != 1 || got[0] != "REAPER Assistant" {
		t.Fatalf("hired at setup = %v", got)
	}

	for _, entry := range []string{"two", "three"} {
		code, payload := f.open(t, openHomeID, entry, `{"request_id":"r-`+entry+`"}`)
		if code != http.StatusOK || payload["staffing"] != "joined" || payload["agent_name"] != "REAPER Assistant" {
			t.Fatalf("open %s = %d %v", entry, code, payload)
		}
	}
	if len(f.staffed) != 2 || f.staffed[0].Mode != "assign" || f.staffed[1].Mode != "assign" ||
		f.staffed[0].Name != "REAPER Assistant" || f.staffed[1].Name != "REAPER Assistant" {
		t.Fatalf("staffing = %+v (a song made its own assistant)", f.staffed)
	}
	if got := f.sharedAgents(); len(got) != 1 {
		t.Fatalf("agents after two opens = %v", got)
	}
	for _, id := range []string{"song-two", "song-three"} {
		song, _ := f.store.Get(id)
		instances := song.GetAgentInstances()
		if len(instances) != 1 || instances[0].Name != "REAPER Assistant" || instances[0].RoleSource != agentworkspace.RoleSourceAssigned {
			t.Fatalf("%s instances = %+v", id, instances)
		}
	}
}
