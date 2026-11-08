package agenthttp

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestAvatarOwnerReadsSnapshotDuringDefinitionEdits(t *testing.T) {
	for _, source := range []string{"legacy", "composite-legacy", "root"} {
		t.Run(source, func(t *testing.T) {
			var st store.Store
			var image string
			if source == "root" {
				composite, _ := rootScoutWithImage(t)
				st, image = composite, scoutImage(t, composite)
			} else {
				f := newReplaceFixture(t, "Scout")
				if result := f.send(t, "Scout", pngBytes, nil); result.Code != http.StatusOK {
					t.Fatalf("seed upload: HTTP %d", result.Code)
				}
				st, image = f.store, f.image(t, "Scout")
				if source == "composite-legacy" {
					composite, err := store.NewCompositeStore(st, t.TempDir(), t.TempDir(), types.Settings{})
					if err != nil {
						t.Fatal(err)
					}
					st = composite
				}
			}
			start := make(chan struct{})
			errors := make(chan error, 5)
			var workers sync.WaitGroup
			workers.Add(5)
			go func() {
				defer workers.Done()
				<-start
				for i := range 100 {
					// Even an unrelated PATCH adopts a cloned Appearance pointer.
					// The uploaded filename stays constant throughout this test.
					if _, err := store.MutateDefinition(st, "Scout", func(draft *agent.Agent) error {
						draft.Settings.SystemPrompt = fmt.Sprintf("synthetic revision %d", i)
						return nil
					}); err != nil {
						errors <- err
						return
					}
				}
			}()
			for range 4 {
				go func() {
					defer workers.Done()
					<-start
					for range 300 {
						if owner := avatarOwner(st, image); owner != "Scout" {
							errors <- fmt.Errorf("avatar owner = %q, want Scout", owner)
							return
						}
					}
				}()
			}
			close(start)
			workers.Wait()
			close(errors)
			for err := range errors {
				t.Error(err)
			}
		})
	}
}
