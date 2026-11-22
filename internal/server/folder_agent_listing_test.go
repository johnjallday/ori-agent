package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

type intakeLibrary struct {
	fakeLearnLibrary
	rootPartial, scopePartial bool
	scanned                   []string
}

func (l *intakeLibrary) ScanFolderAgentRoot(context.Context, string, string, string, string) (int, bool, error) {
	return 8, l.rootPartial, nil
}
func (l *intakeLibrary) ScanFolderAgentScope(_ context.Context, _, _, _, id, _ string) (int, bool, error) {
	l.scanned = append(l.scanned, id)
	return 8, l.scopePartial, nil
}

func TestFolderAgentIntakeListsOnlyIssuedScopesForReviewedParents(t *testing.T) {
	for _, tc := range []struct{ rootPartial, scopePartial, skip bool }{{true, false, false}, {false, true, false}, {false, false, true}} {
		l := &intakeLibrary{rootPartial: tc.rootPartial, scopePartial: tc.scopePartial}
		l.scopes = []projectlibrary.ScanScopeChoice{{ID: "excluded", RelativeFolder: "Albums"}}
		if !tc.skip {
			l.scopes = append(l.scopes, projectlibrary.ScanScopeChoice{ID: "included", RelativeFolder: "Picker Songs"})
		}
		rec := folderagent.Record{Facts: foldermap.Facts{Members: []foldermap.Project{{Path: "Picker Songs/Track-1", Depth: 2}}}}
		result, err := listFolderAgentSongs(context.Background(), l, rec, "owner", "home", "root", "run", "songs", "scopes", nil, func(string, string, string) {})
		if err != nil || result.listed != 8 || result.partial != (tc.rootPartial || tc.scopePartial) || (result.unreached > 0) != tc.skip {
			t.Fatalf("%+v => %+v %v", tc, result, err)
		}
		if tc.skip && len(l.scanned) != 0 || !tc.skip && (len(l.scanned) != 1 || l.scanned[0] != "included") {
			t.Fatal(l.scanned)
		}
	}
}
