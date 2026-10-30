package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

func entryPlanFiles(t *testing.T, root string, names ...string) {
	t.Helper()
	for i, name := range names {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("contents are not part of this review"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Unix(1700000000+int64(i), 0)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

func entryBasePlan() personalassistant.FolderSetupPlan {
	return personalassistant.NewFolderSetupPlan([]personalassistant.FolderPlanLine{{Kind: personalassistant.FolderPlanFolder, Name: "Links the folder where it is"}})
}

func TestFolderEntryPlan_UsesMetadataAndKeepsAListedChoice(t *testing.T) {
	for _, tc := range []struct {
		name            string
		files           []string
		preferred, want string
		candidates      []string
	}{
		{"none", []string{"notes.txt"}, "", "", nil},
		{"one", []string{"Song.rpp"}, "", "Song.rpp", nil},
		{"newest", []string{"Alternate.rpp", "Song.rpp"}, "", "Song.rpp", []string{"Song.rpp", "Alternate.rpp"}},
		{"kept", []string{"Alternate.rpp", "Song.rpp"}, "Alternate.rpp", "Alternate.rpp", []string{"Song.rpp", "Alternate.rpp"}},
		{"removed-choice", []string{"Song.rpp"}, "Removed.rpp", "Song.rpp", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			entryPlanFiles(t, root, tc.files...)
			plan, err := projectEntryFilePlan(entryBasePlan(), root, tc.preferred, &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".rpp"}})
			if err != nil || plan.EntryName != tc.want || !reflect.DeepEqual(plan.EntryCandidates, tc.candidates) {
				t.Fatalf("entry=%q candidates=%v, %v", plan.EntryName, plan.EntryCandidates, err)
			}
			if tc.want != "" && (len(plan.Lines) != 2 || plan.Lines[1].Name != "Opens "+tc.want) {
				t.Fatal("missing file consequence", plan.Lines)
			}
			encoded, err := json.Marshal(plan)
			if err != nil || strings.Contains(string(encoded), root) || strings.Contains(string(encoded), "contents are not") {
				t.Fatal("plan leaked private data", err)
			}
		})
	}
}

func TestFolderEntryPlan_TiesAreByNameAndSymlinksAreNotCandidates(t *testing.T) {
	root := t.TempDir()
	entryPlanFiles(t, root, "Song.rpp", "Alternate.rpp")
	at := time.Unix(1700000000, 0)
	if err := os.Chtimes(filepath.Join(root, "Alternate.rpp"), at, at); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "Song.rpp"), filepath.Join(root, "Linked.rpp")); err != nil {
		t.Fatal(err)
	}
	declaration := &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".rpp"}}
	plan, err := projectEntryFilePlan(entryBasePlan(), root, "", declaration)
	if err != nil || plan.EntryName != "Alternate.rpp" || !reflect.DeepEqual(plan.EntryCandidates, []string{"Alternate.rpp", "Song.rpp"}) {
		t.Fatal(plan, err)
	}
	link := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := projectEntryFilePlan(entryBasePlan(), link, "", declaration); err == nil {
		t.Fatal("followed a symlink root")
	}
}

func TestFolderEntryPlan_RefusesMoreThanTheBoundedCandidateSet(t *testing.T) {
	root := t.TempDir()
	for i := range 65 {
		entryPlanFiles(t, root, fmt.Sprintf("Song-%02d.rpp", i))
	}
	if _, err := projectEntryFilePlan(entryBasePlan(), root, "", &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".rpp"}}); err == nil {
		t.Fatal("accepted unbounded candidates")
	}
}

// Explicit installed-contract evidence, not an editable companion source check.
// Reads the verified disposable registry only; no journey, install or runtime.
func TestFolderEntryPlan_DisposableInstalledDeclaration(t *testing.T) {
	root := os.Getenv("ORI_FOLDER_REVIEW_CONTINUITY_SANDBOX")
	if root == "" {
		t.Skip("explicit disposable installed registry not supplied")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil || !strings.HasPrefix(root, tmp+string(filepath.Separator)+"ori-demo.") {
		t.Fatal("not a disposable demo root")
	}
	worktree, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(root, ".ori-demo-owner"))
	if err != nil || string(marker) != "worktree="+worktree+"\n" {
		t.Fatal("different demo owner", err)
	}
	h := &folderSetupHost{builder: &ServerBuilder{pluginHandler: pluginhttp.NewHandler(nil, nil, filepath.Join(root, "plugins"))}}
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		t.Fatal("missing reviewed integration")
	}
	installed, err := h.builder.pluginHandler.Manager().List()
	if err != nil {
		t.Fatal(err)
	}
	plan := entryBasePlan()
	plan.Intent.Integration = personalassistant.FolderInstallReady
	for _, candidate := range installed {
		if candidate.Name == entry.PluginID {
			plan.Intent.IntegrationVersion = candidate.Version
		}
	}
	if plan.Intent.IntegrationVersion == "" {
		t.Fatal("no installed REAPER declaration")
	}
	folder := t.TempDir()
	entryPlanFiles(t, folder, "Alternate.rpp", "Song.rpp")
	planned, err := h.projectEntryPlan(context.Background(), personalassistant.FolderSetupRequest{UserID: "local", Path: folder}, setupTarget{entry: entry}, plan)
	if err != nil || planned.EntryName != "Song.rpp" || len(planned.EntryCandidates) != 2 {
		t.Fatalf("installed entry contract: %+v %v", planned, err)
	}
	t.Logf("read installed REAPER %s declaration without a setup journey; selected %s", plan.Intent.IntegrationVersion, planned.EntryName)
}
