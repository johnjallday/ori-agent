package server

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestFolderAgentHiring_CharacterSuggestion(t *testing.T) {
	catalog := charactercatalog.MustLoad()
	for _, policy := range []folderlook.RootPolicy{folderlook.RootDownloads, folderlook.RootDesktop, folderlook.RootDocuments, ""} {
		t.Run(string(policy), func(t *testing.T) {
			st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
			if err != nil {
				t.Fatal(err)
			}
			h := folderAgentHiring{builder: &ServerBuilder{st: st}}
			preferred := folderagent.PreferredCharacter(policy)
			if got := h.Character(context.Background(), "u1", policy); got != preferred {
				t.Fatalf("empty roster: %q, want %q", got, preferred)
			}
			appearance := types.NewAgentAppearance()
			appearance.SetCharacter(preferred, 1)
			appearance.Mode = types.AppearanceModeGenerated
			// An inactive stored character must not consume the preferred face.
			if err := st.CreateAgent("Generated", &store.CreateAgentConfig{Appearance: appearance}); err != nil {
				t.Fatal(err)
			}
			if got := h.Character(context.Background(), "u1", policy); got != preferred {
				t.Fatalf("inactive character counted: %q", got)
			}
			appearance.Mode = types.AppearanceModeCharacter
			if err := st.CreateAgent("Wearing preferred", &store.CreateAgentConfig{Appearance: appearance}); err != nil {
				t.Fatal(err)
			}
			// The assistant profile cannot be reused, but its active face still
			// counts toward the least-worn suggestion, like the picker's taken list.
			if err := st.UpdateAgent("Wearing preferred", func(ag *agent.Agent) error {
				ag.Metadata = &types.AgentMetadata{Tags: []string{personalassistant.ProfileAssistantMarker("pa-1")}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			want := ""
			for _, ch := range catalog.Working() {
				if string(ch.ID) != preferred {
					want = string(ch.ID)
					break
				}
			}
			if got := h.Character(context.Background(), "u1", policy); got != want {
				t.Fatalf("least worn: %q, want catalog-first %q", got, want)
			}
			for i, ch := range catalog.Working() {
				if string(ch.ID) == preferred {
					continue
				}
				a := types.NewAgentAppearance()
				a.SetCharacter(string(ch.ID), ch.EntryVersion)
				a.Mode = types.AppearanceModeCharacter
				if err := st.CreateAgent(fmt.Sprintf("Agent %d", i), &store.CreateAgentConfig{Appearance: a}); err != nil {
					t.Fatal(err)
				}
			}
			if got := h.Character(context.Background(), "u1", policy); got != preferred {
				t.Fatalf("equally worn: %q, want %q", got, preferred)
			}
		})
	}
}
