package onboarding

import (
	"errors"
	"fmt"
	"github.com/johnjallday/ori-agent/internal/types"
	"path/filepath"
	"sync"
	"testing"
)

func TestSelectedToolsMergeConcurrentAddsWithoutPersonalization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	m := NewManager(path)
	if err := m.SetUserProfile(&types.InferredProfile{PreferredTools: []string{"Before"}, Description: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	previous := m.GetUserProfile()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := m.AddPreferredTools([]string{"before", fmt.Sprintf("Fixture %d", i), "Before"})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if err := m.ReplaceUserProfile(previous, previous); !errors.Is(err, ErrProfileChanged) {
		t.Fatalf("stale replacement: %v", err)
	}
	p := NewManager(path).GetUserProfile()
	if len(p.PreferredTools) != 13 || p.Description != "unchanged" || !p.PersonalizedAt.IsZero() || !p.InferredAt.Equal(previous.InferredAt) || len(p.DetectedApps) > 0 {
		t.Fatalf("unexpected saved profile %+v", p)
	}
}
func TestSelectedToolsNoSaveAndPersistenceFailure(t *testing.T) {
	m := NewManager(t.TempDir())
	if _, err := m.AddPreferredTools(nil); err != nil || m.GetUserProfile() != nil {
		t.Fatal("empty selection wrote a profile")
	}
	if _, err := m.AddPreferredTools([]string{"Fixture"}); err == nil || m.GetUserProfile() != nil {
		t.Fatal("failed save changed in-memory profile")
	}
}
