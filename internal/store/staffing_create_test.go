package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
)

func TestCreateStaffingProfileConcurrentCreatesHaveExactlyOneOwner(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	var wg sync.WaitGroup
	results := make(chan StaffingSelection, 2)
	errorsCh := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := c.CreateStaffingProfile(context.Background(), "Scout", &CreateAgentConfig{})
			results <- result
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	owned, refused := 0, 0
	for result := range results {
		if result.Name != "" {
			owned++
			if err := c.WithStaffingProfiles(context.Background(), []StaffingSelection{result}, func(map[string]*agent.Agent) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	for err := range errorsCh {
		if errors.Is(err, ErrStaffingNameTaken) {
			refused++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if owned != 1 || refused != 1 {
		t.Fatalf("owners=%d refused=%d", owned, refused)
	}
}

func TestCreateStaffingProfileRefusesExistingSourcesAndFolders(t *testing.T) {
	for _, scenario := range []string{"root", "legacy", "case_fold", "unloaded", "folder_only"} {
		t.Run(scenario, func(t *testing.T) {
			c, _, _ := compositeWithWorkspaces(t)
			name := "Scout"
			switch scenario {
			case "root", "case_fold":
				if err := c.CreateAgent(name, &CreateAgentConfig{SystemPrompt: "keep"}); err != nil {
					t.Fatal(err)
				}
				if scenario == "case_fold" {
					name = "scout"
				}
			case "legacy":
				if err := c.system.CreateAgent(name, &CreateAgentConfig{}); err != nil {
					t.Fatal(err)
				}
			case "folder_only", "unloaded":
				root, err := c.rootForWrite()
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root.agentsDir(), name)
				if err := os.MkdirAll(path, 0o750); err != nil {
					t.Fatal(err)
				}
				if scenario == "unloaded" {
					if err := os.WriteFile(root.definitionPath(name), []byte(`{"settings":{"system_prompt":"keep"}}`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := c.CreateStaffingProfile(context.Background(), name, &CreateAgentConfig{SystemPrompt: "overwrite"})
			if result.Name != "" || !errors.Is(err, ErrStaffingNameTaken) {
				t.Fatalf("claimed existing source: %+v %v", result, err)
			}
		})
	}
}
