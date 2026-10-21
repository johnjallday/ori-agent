package vaultfixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
)

var now = time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC)

func TestBuildAll_IsDeterministicForOneClock(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	a, err := BuildAll(first, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildAll(second, now)
	if err != nil {
		t.Fatal(err)
	}
	for index := range a {
		left, err := softwarefixture.Hash(a[index].Path)
		if err != nil {
			t.Fatal(err)
		}
		right, err := softwarefixture.Hash(b[index].Path)
		if err != nil {
			t.Fatal(err)
		}
		if left != right {
			t.Fatalf("%s differs between two builds", a[index].Flavour)
		}
		if !a[index].LastOpened.Equal(b[index].LastOpened) {
			t.Fatalf("%s LastOpened differs", a[index].Flavour)
		}
	}
	album := filepath.Join(a[0].Path, "Projects", "Album Release.md")
	info, err := os.Stat(album)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-day); !info.ModTime().Equal(want) {
		t.Fatalf("Album Release mtime = %v, want %v", info.ModTime(), want)
	}
}

func TestBuild_OnlyObsidianFlavoursHoldObsidiansFolder(t *testing.T) {
	vaults, err := BuildAll(t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, vault := range vaults {
		info, err := os.Lstat(filepath.Join(vault.Path, ".obsidian"))
		has := err == nil && info.IsDir()
		if has != (vault.Kind == KindObsidian) {
			t.Errorf("%s: .obsidian present = %v, kind %s", vault.Flavour, has, vault.Kind)
		}
	}
}

func TestBuild_RefusesAnUnknownFlavour(t *testing.T) {
	if _, err := Build(t.TempDir(), "poetry", now); err == nil {
		t.Fatal("Build accepted an unknown flavour")
	}
}

func TestWriteVaultList_NamesOnlyObsidianVaultsInObsidiansShape(t *testing.T) {
	vaults, err := BuildAll(t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "obsidian", "obsidian.json")
	if err := WriteVaultList(path, vaults...); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Vaults map[string]struct {
			Path string `json:"path"`
			TS   int64  `json:"ts"`
			Open bool   `json:"open"`
		} `json:"vaults"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Vaults) != 2 {
		t.Fatalf("vaults = %d, want the two Obsidian vaults", len(list.Vaults))
	}
	seen := map[string]int64{}
	for id, entry := range list.Vaults {
		if len(id) != 16 {
			t.Fatalf("id %q is not Obsidian's 16-character shape", id)
		}
		seen[entry.Path] = entry.TS
	}
	for _, vault := range vaults[:2] {
		if seen[vault.Path] != vault.LastOpened.UnixMilli() {
			t.Fatalf("%s ts = %d, want %d", vault.Name, seen[vault.Path], vault.LastOpened.UnixMilli())
		}
	}
	if _, ok := seen[vaults[2].Path]; ok {
		t.Fatal("the plain Markdown folder is in Obsidian's list")
	}
}
