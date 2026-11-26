//go:build darwin

package filesearch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestChatPickBindsPhysicalSelectionWithoutInspection(t *testing.T) {
	for _, change := range []string{"owner", "target", "revision", "expired", "replaced-pick", "changed-folder", "changed-links", "files-only"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			ctx := t.Context()
			root := filepath.Join(f.home, "Albums")
			must(t, os.Mkdir(root, 0750))
			must(t, os.WriteFile(filepath.Join(root, "Recognizable.wav"), []byte("private sentinel"), 0600))
			calls := 0
			f.Pick = func(context.Context) (string, bool, error) { calls++; return root, true, nil }
			f.ValidLink = func(string, string, WorkspaceMatch) bool { return true }
			owner, revision := "local", "r1"
			target := foldercontext.Target{UserID: "assistant-user", WorkspaceID: "hq", AgentName: "atlas", ConversationID: "chat"}
			r, e := f.PickForChat(ctx, owner, target, revision, "custom")
			must(t, e)
			again, path, e := f.ResumeForChat(ctx, owner, target, revision, r.ID)
			must(t, e)
			body, e := json.Marshal(again)
			must(t, e)
			if path != root || r.ID == "" || again.Label != "Albums" || calls != 1 || strings.Contains(string(body), root) || strings.Contains(string(body), "Recognizable") || len(again.Matches) != 0 {
				t.Fatalf("unexpected pick %s (calls=%d)", body, calls)
			}
			switch change {
			case "owner":
				owner = "another"
			case "target":
				target.ConversationID = "other"
			case "revision":
				revision = "r2"
			case "expired":
				c := f.intents[owner]
				c.expires = time.Now()
				f.intents[owner] = c
			case "replaced-pick":
				_, e = f.Begin(owner, "documents")
				must(t, e)
			case "changed-folder":
				must(t, os.Rename(root, root+"-old"))
				must(t, os.Mkdir(root, 0750))
			case "changed-links":
				legacyLink(t, f, owner, "other", root)
			case "files-only":
				intent, e := f.Begin(owner, "custom")
				must(t, e)
				r, e = f.Review(ctx, owner, intent)
				must(t, e)
			}
			if _, _, err := f.ResumeForChat(ctx, owner, target, revision, r.ID); err == nil {
				t.Fatal("invalid resume succeeded")
			}
			if c := countStores(t, f); c != (storeCounts{}) {
				t.Fatalf("pick changed index: %+v", c)
			}
		})
	}
}
