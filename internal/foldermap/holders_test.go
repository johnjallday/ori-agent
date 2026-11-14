package foldermap

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func TestProjectHoldersUseRecordedChildrenAndFoundProjects(t *testing.T) {
	m := &Map{Folders: []Folder{
		{Path: "", State: StateWalked},
		{Path: "A", State: StateClosed, Marker: "go.mod", Shape: folderdigest.ShapeCode},
		{Path: "B", State: StateWalked},
		{Path: "B/Song", State: StateClosed, Marker: "*.rpp", Shape: folderdigest.ShapeAudio},
		{Path: "B/Code", State: StateClosed, Marker: "go.mod", Shape: folderdigest.ShapeCode},
		{Path: "B/Unopened", State: StateUnopened},
		{Path: "Unknown/Project", State: StateClosed, Marker: "go.mod", Shape: folderdigest.ShapeCode},
	}}
	holders := ProjectHolders(m)
	if len(holders) != 2 || holders[0].Name != "B" || holders[0].Index != 2 || holders[0].Count != 2 || len(holders[0].Shapes) != 2 {
		t.Fatal("wrong mixed holder", holders)
	}
	if holders[1].Name != "A" || holders[1].Count != 1 {
		t.Fatal("direct project missing", holders)
	}
}
