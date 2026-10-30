package server

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
)

func TestFolderHandoffOpen_ChangedFileAsksWithoutAnUnselectedOpen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		want  string
	}{
		{"one remaining", []string{"Remaining.rpp"}, personalassistant.FolderStopNeedsChoice},
		{"multiple", []string{"Song.rpp", "Other.rpp"}, personalassistant.FolderStopNeedsChoice},
		{"empty", nil, personalassistant.FolderStopPlanChanged},
		{"path", []string{"/private/Song.rpp"}, personalassistant.FolderStopPlanChanged},
		{"over bound", make([]string, 65), personalassistant.FolderStopPlanChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// This conversion consumes the failed owner's read-only disclosure;
			// it has no opener and cannot accidentally commit a single-file default.
			err := handoffOpenError(&sessionhttp.LibraryFileNotOfferedError{ProjectFiles: tc.files})
			var stop *foldersetup.OpenStop
			if !errors.As(err, &stop) || stop.Reason != tc.want {
				t.Fatalf("stop: %v", err)
			}
			if tc.want == personalassistant.FolderStopNeedsChoice {
				if len(stop.Candidates) != len(tc.files) || stop.Candidates[0] != tc.files[0] {
					t.Fatal("lost current candidates", stop)
				}
				tc.files[0] = "changed"
				if stop.Candidates[0] == "changed" {
					t.Fatal("aliased disclosure")
				}
			}
		})
	}
	var stop *foldersetup.OpenStop
	if err := handoffOpenError(sessionhttp.ErrLibraryFileNotOffered); !errors.As(err, &stop) || stop.Reason != personalassistant.FolderStopPlanChanged {
		t.Fatal("legacy refusal without candidates must stay a refusal", err)
	}
}
