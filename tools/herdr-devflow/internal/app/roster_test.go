package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
)

type liveRosterRunner struct {
	response string
}

func (r liveRosterRunner) Run(_ context.Context, command herdr.Command) (herdr.CommandResult, error) {
	if strings.Join(command.Args, " ") != "agent list" {
		return herdr.CommandResult{}, nil
	}
	return herdr.CommandResult{Stdout: []byte(r.response)}, nil
}

func TestLiveAgentRosterUsesAReadableFallbackForUnnamedAgents(t *testing.T) {
	client := herdr.New("fake-herdr", "", liveRosterRunner{response: `{
		"result": {
			"agents": [{
				"agent": "codex",
				"agent_status": "working",
				"cwd": "/tmp/worktrees/ori-agent-dev",
				"pane_id": "w1:p1"
			}]
		}
	}`})

	roster, err := collectLiveAgentRoster(context.Background(), client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(roster.Agents) != 1 {
		t.Fatalf("roster = %#v", roster.Agents)
	}
	agent := roster.Agents[0]
	if agent.Agent != "codex@ori-agent-dev" || agent.Kind != "codex" || agent.Status != "working" {
		t.Fatalf("unnamed live agent = %#v", agent)
	}
	encoded, err := json.Marshal(roster)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "w1:p1") || strings.Contains(string(encoded), `"target"`) {
		t.Fatalf("internal focus target leaked into status JSON: %s", encoded)
	}

	var output strings.Builder
	if err := renderLiveAgentRoster(&output, roster); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Open agents: 1", "AGENT", "KIND", "STATUS", "WORKTREE", "codex@ori-agent-dev"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("human roster did not contain %q:\n%s", want, output.String())
		}
	}
}

func TestLiveAgentRosterTreatsAnEmptySuccessfulListAsHealthy(t *testing.T) {
	client := herdr.New("fake-herdr", "", liveRosterRunner{response: `{"result":{"agents":[]}}`})
	roster, err := collectLiveAgentRoster(context.Background(), client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if roster.Agents == nil || len(roster.Agents) != 0 {
		t.Fatalf("empty roster = %#v, want a non-nil empty list", roster.Agents)
	}

	var output strings.Builder
	if err := renderLiveAgentRoster(&output, roster); err != nil {
		t.Fatal(err)
	}
	if output.String() != "No open agents.\n" {
		t.Fatalf("empty human roster = %q", output.String())
	}
}
