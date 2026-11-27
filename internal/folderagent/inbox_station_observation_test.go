package folderagent

import (
	"fmt"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"os"
	"path/filepath"
	"testing"
)

func TestInboxPileUsesTheWalkNotTheCappedTree(t *testing.T) {
	root := t.TempDir()
	const files = folderdigest.MaxTreeNodes + 18
	for i := range files {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("sample-%03d.pdf", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	protected := []string{filepath.Join(root, "sample-000.pdf")}
	r, err := ObserveInbox(root, protected)
	if err != nil || r.Pile == nil || r.Pile.Count != files-1 || len(r.Pile.Names) != 6 || r.Pile.Names[0] != "sample-001.pdf" || r.Pile.Policy != scopeHash(protected) || len(r.Metadata.Nodes) != folderdigest.MaxTreeNodes {
		t.Fatalf("wrong count, policy or samples from attachment: %+v %v", r.Pile, err)
	}
	for name, options := range map[string]folderdigest.Options{"partial": {MaxEntries: 12, PileFile: stationFileName}, "ordinary digest": {}} {
		t.Run(name, func(t *testing.T) {
			r, err := folderdigest.Scan(root, options)
			if name == "partial" {
				s := NewService(Deps{InboxStation: func(string, string) (InboxStation, bool) {
					return InboxStation{Anchor: StationAnchor{root, "ref", "lead"}, Manager: "Lead", Witness: r.StationWitness, Operation: "hire"}, true
				}})
				v, _, ok := s.inboxStation("owner", "ws")
				if !ok || !v.StationOffer || v.FilingPile != nil {
					t.Fatal("partial attachment must offer Build without a count")
				}
			}
			if err != nil || r.Pile != nil {
				t.Fatalf("no pile expected: %+v %v", r.Pile, err)
			}
		})
	}
}
