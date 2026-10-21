package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

type fakeSecondBrainSources struct {
	source    secondbrain.Source
	connected bool
	err       error
}

func (f fakeSecondBrainSources) ConnectedSource(context.Context, string) (secondbrain.Source, bool, error) {
	return f.source, f.connected, f.err
}

func TestSecondBrainAuthority_RevalidatesTheConnectedFolder(t *testing.T) {
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(root, "Second Brain")
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o750); err != nil {
		t.Fatal(err)
	}
	source := secondbrain.Source{
		ID: secondbrain.SourceID(vault), Kind: secondbrain.KindObsidian, Name: "Second Brain", Path: vault,
		ConnectedAt: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC), RequestID: "connect-1",
	}
	binding := personalassistant.KnowledgeBinding{UserID: "local", HQWorkspaceID: "hq-local"}
	proposal := personalassistant.SecondBrainKnowledgeProposal(source)
	item := personalassistant.KnowledgeItem{
		ID: "item-1", SourceKind: personalassistant.SecondBrainSourceKind, Category: "how_you_work",
		SemanticKey: personalassistant.KnowledgeSemanticKey(binding, proposal), CurrentRevisionID: "rev-1",
		Revisions: []personalassistant.KnowledgeRevision{{ID: "rev-1", Evidence: proposal.Evidence}},
	}
	authority := &secondBrainAuthority{brain: fakeSecondBrainSources{source: source, connected: true}}
	if err := authority.Revalidate(ctx, binding, item); err != nil {
		t.Fatalf("the connected vault: %v", err)
	}

	for name, change := range map[string]func() (*secondBrainAuthority, personalassistant.KnowledgeItem){
		"disconnected": func() (*secondBrainAuthority, personalassistant.KnowledgeItem) {
			return &secondBrainAuthority{brain: fakeSecondBrainSources{}}, item
		},
		"unreadable record": func() (*secondBrainAuthority, personalassistant.KnowledgeItem) {
			return &secondBrainAuthority{brain: fakeSecondBrainSources{err: errors.New("down")}}, item
		},
		"another folder connected": func() (*secondBrainAuthority, personalassistant.KnowledgeItem) {
			other := source
			other.Path = filepath.Join(root, "Other")
			other.ID = secondbrain.SourceID(other.Path)
			return &secondBrainAuthority{brain: fakeSecondBrainSources{source: other, connected: true}}, item
		},
		"foreign evidence": func() (*secondBrainAuthority, personalassistant.KnowledgeItem) {
			foreign := item
			foreign.Revisions = []personalassistant.KnowledgeRevision{{ID: "rev-1", Evidence: []personalassistant.KnowledgeEvidence{{SourceKind: "folder_scan", SourceID: source.ID}}}}
			return authority, foreign
		},
		"another source kind": func() (*secondBrainAuthority, personalassistant.KnowledgeItem) {
			other := item
			other.SourceKind = personalassistant.FolderScanSourceKind
			return authority, other
		},
		"no authority": func() (*secondBrainAuthority, personalassistant.KnowledgeItem) {
			return nil, item
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, it := change()
			if err := a.Revalidate(ctx, binding, it); !errors.Is(err, errSecondBrainSourceUnavailable) {
				t.Fatalf("err = %v, want the source unavailable", err)
			}
		})
	}

	// The vault lost Obsidian's folder; a plain folder needs its notes.
	if err := os.Remove(filepath.Join(vault, ".obsidian")); err != nil {
		t.Fatal(err)
	}
	if err := authority.Revalidate(ctx, binding, item); !errors.Is(err, errSecondBrainSourceUnavailable) {
		t.Fatalf("a vault without .obsidian revalidated: %v", err)
	}
}
