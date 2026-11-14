package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKnownFolderChipsShareTheRecognitionLookup(t *testing.T) {
	home := t.TempDir()
	remapped := filepath.Join(home, "Cloud", "Mes documents")
	if err := os.MkdirAll(remapped, 0750); err != nil {
		t.Fatal(err)
	}
	fixture := newFolderDigestFixture(t)
	service := NewFolderDigestService(fixture.store, FolderDigestDeps{
		HomeDir: func() (string, error) { return home, nil },
		KnownFolderLookup: func(_, id string) (string, error) {
			if id == "documents" {
				return remapped, nil
			}
			return "", errors.New("absent")
		},
		ValidateRoot: func(path string) (string, error) { return path, nil },
	})
	path, err := service.KnownFolderRoot("documents")
	want, _ := filepath.EvalSymlinks(remapped)
	if err != nil || path != want {
		t.Fatal("recognition lookup", path, err)
	}
	path, chosen, err := service.ChooseFolder(context.Background(), "local", FolderChoice{Chip: "documents"})
	if err != nil || !chosen || path != want {
		t.Fatal("chip and recognition differ", path, chosen, err)
	}
	chips := service.availableChips()
	if len(chips) != 1 || chips[0].ID != "documents" {
		t.Fatal("missing known folder blocked present one", chips)
	}
}
