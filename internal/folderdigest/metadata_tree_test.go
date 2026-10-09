package folderdigest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScan_MetadataTreeSharesWalkAndNeverOpensFiles(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"Aurora/Nested", ".hidden", "node_modules", "Library"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"Aurora/session.rpp", "Aurora/Nested/unreadable.txt", ".hidden/private.txt", "node_modules/private.txt", "Library/private.txt", "cloud.icloud"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("CONTENT_SENTINEL_NOT_METADATA"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "Aurora"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	ordinary, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.Metadata != nil {
		t.Fatal("background scan captured tree")
	}
	before, err := Scan(root, Options{CaptureTree: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ordinary.Candidates, before.Candidates) || ordinary.Entries != before.Entries {
		t.Fatal("tree changed scan semantics")
	}
	if len(before.Metadata.Nodes) != 4 || before.SkippedLinks != 1 {
		t.Fatal(before.Metadata, before.SkippedLinks)
	}
	for _, node := range before.Metadata.Nodes {
		if strings.Contains(node.Name, "private") || strings.Contains(node.Name, "linked") || strings.Contains(node.Name, "cloud") {
			t.Fatal("excluded entry disclosed", node)
		}
	}
	makeUnreadable(t, root)
	after, err := Scan(root, Options{CaptureTree: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Metadata, after.Metadata) {
		t.Fatal("unreadable contents changed metadata")
	}
	encoded, _ := json.Marshal(after)
	if strings.Contains(string(encoded), "unreadable.txt") || strings.Contains(string(encoded), "CONTENT_SENTINEL") {
		t.Fatal("raw tree serialized incidentally")
	}
}

func TestScan_MetadataTreeCapsWithoutInventingParents(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 100; i++ {
		dir := filepath.Join(root, fmt.Sprintf("folder-%03d", i))
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "note.txt"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Scan(root, Options{CaptureTree: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Metadata.Nodes) != MaxTreeNodes || result.Metadata.Omitted != 136 || result.RootCandidate().FileCount != 100 {
		t.Fatal(result.Metadata, result.RootCandidate())
	}
	seen := map[string]bool{"": true}
	for _, node := range result.Metadata.Nodes {
		if !seen[node.ParentID] {
			t.Fatal("orphan", node)
		}
		seen[node.ID] = true
	}
	bounded, err := Scan(root, Options{CaptureTree: true, MaxEntries: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !bounded.Partial || len(bounded.Metadata.Nodes) != 5 || bounded.PartialReason != PartialEntries {
		t.Fatal(bounded)
	}
}
