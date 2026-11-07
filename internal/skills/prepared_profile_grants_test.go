package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreparedProfileSkillEnableDoesNotReenterProfileLookup(t *testing.T) {
	folder := t.TempDir()
	manager := NewManager(ManagerConfig{})
	manager.SetAgentFolderResolver(func(string) (string, bool) { return folder, true })
	apply, err := manager.PrepareProfileSkillEnable("Scout", []string{"project-skill"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, skillStateFileName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("preparing a grant wrote state")
	}
	manager.SetAgentFolderResolver(func(string) (string, bool) { panic("profile lookup re-entered under source fence") })
	if err := apply(); err != nil {
		t.Fatal(err)
	}
	registry, err := loadSkillRegistry(path)
	if err != nil || !registry.Skills["project-skill"].Enabled {
		t.Fatal("captured profile registry was not updated")
	}
}
