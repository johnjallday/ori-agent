package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
)

func staffingChoice(t *testing.T, st Store) StaffingSelection {
	t.Helper()
	profiles := ReadStaffingSnapshot(context.Background(), st).Profiles
	if len(profiles) != 1 {
		t.Fatalf("expected one canonical profile, got %d", len(profiles))
	}
	return StaffingSelection{Name: profiles[0].Name, Witness: profiles[0].Witness}
}

func TestStaffingSourceGuardRejectsChangedSelections(t *testing.T) {
	for _, change := range []string{"configuration", "replacement", "root", "omitted_witness", "wrong_name", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			c, _, _ := compositeWithWorkspaces(t)
			if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "private original"}); err != nil {
				t.Fatal(err)
			}
			selected := []StaffingSelection{staffingChoice(t, c)}
			switch change {
			case "configuration":
				if _, err := MutateDefinition(c, "Scout", func(a *agent.Agent) error { a.Settings.SystemPrompt = "changed"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := c.DeleteAgent("Scout"); err != nil {
					t.Fatal(err)
				}
				if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "private original"}); err != nil {
					t.Fatal(err)
				}
			case "root":
				c.SetRoot(t.TempDir())
			case "omitted_witness":
				selected[0].Witness = ""
			case "wrong_name":
				selected[0].Name = "Private"
			case "duplicate":
				selected = append(selected, selected[0])
			}
			called := false
			err := WithStaffingProfiles(context.Background(), c, selected, func(map[string]*agent.Agent) error { called = true; return nil })
			if !errors.Is(err, ErrStaffingSourceChanged) || called {
				t.Fatalf("stale selection reached binding: called=%v err=%v", called, err)
			}
		})
	}
}

func TestStaffingSourceGuardDetachesDefinitionsAndHonorsCallbackFailure(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t, workspaceCopy("ws-1", "Private", "Scout", "private copy"))
	if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "root prompt"}); err != nil {
		t.Fatal(err)
	}
	selected := staffingChoice(t, c)
	before := c.ReadStaffingSnapshot(context.Background()).Profiles[0].Witness
	refused := errors.New("target changed")
	err := WithStaffingProfiles(context.Background(), c, []StaffingSelection{selected}, func(profiles map[string]*agent.Agent) error {
		if profiles["Scout"].Settings.SystemPrompt != "root prompt" {
			t.Fatal("bound a workspace copy")
		}
		profiles["Scout"].Settings.SystemPrompt = "not persisted"
		return refused
	})
	if !errors.Is(err, refused) || c.ReadStaffingSnapshot(context.Background()).Profiles[0].Witness != before {
		t.Fatal("callback failure changed source")
	}
}

func TestStaffingSourceGuardFencesMutationAndRootSwitch(t *testing.T) {
	for _, operation := range []string{"edit", "delete", "root_switch"} {
		t.Run(operation, func(t *testing.T) {
			c, _, _ := compositeWithWorkspaces(t)
			if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
				t.Fatal(err)
			}
			selected := staffingChoice(t, c)
			entered, release, bound := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				bound <- c.WithStaffingProfiles(context.Background(), []StaffingSelection{selected}, func(map[string]*agent.Agent) error { close(entered); <-release; return nil })
			}()
			<-entered
			mutated := make(chan error, 1)
			newRoot := t.TempDir()
			go func() {
				switch operation {
				case "edit":
					_, err := MutateDefinition(c, "Scout", func(a *agent.Agent) error { a.Settings.Model = "changed"; return nil })
					mutated <- err
				case "delete":
					mutated <- c.DeleteAgent("Scout")
				case "root_switch":
					c.SetRoot(newRoot)
					mutated <- nil
				}
			}()
			select {
			case err := <-mutated:
				close(release)
				t.Fatalf("mutation crossed source fence: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			if err := <-bound; err != nil {
				t.Fatal(err)
			}
			if err := <-mutated; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStaffingSourceGuardCleanupRechecksReferenceAfterConcurrentBind(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	selected := staffingChoice(t, c)
	entered, release := make(chan struct{}), make(chan struct{})
	bound := make(chan error, 1)
	referenced := false // Protected by the source fence in both callbacks.
	go func() {
		bound <- c.WithStaffingProfiles(context.Background(), []StaffingSelection{selected}, func(map[string]*agent.Agent) error { close(entered); <-release; referenced = true; return nil })
	}()
	<-entered
	finished := make(chan error, 1)
	go func() {
		deleted, err := c.DeleteStaffingProfile(context.Background(), selected, func(*agent.Agent) bool { return !referenced }, func() { t.Error("purged shared sessions") })
		if deleted {
			t.Error("deleted profile bound by another workspace")
		}
		finished <- err
	}()
	close(release)
	if err := <-bound; err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, exists := c.GetAgent("Scout"); !exists {
		t.Fatal("shared profile lost")
	}
}

func TestStaffingSourceGuardSessionCleanupPrecedesNamesakeReplacement(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	selected := staffingChoice(t, c)
	entered, release := make(chan struct{}), make(chan struct{})
	deleted := make(chan error, 1)
	go func() {
		_, err := c.DeleteStaffingProfile(context.Background(), selected, func(*agent.Agent) bool { return true }, func() { close(entered); <-release })
		deleted <- err
	}()
	<-entered
	created := make(chan error, 1)
	go func() { created <- c.CreateAgent("Scout", &CreateAgentConfig{}) }()
	select {
	case err := <-created:
		close(release)
		t.Fatalf("replacement crossed session cleanup fence: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if staffingChoice(t, c).Witness == selected.Witness {
		t.Fatal("replacement retained source witness")
	}
}
