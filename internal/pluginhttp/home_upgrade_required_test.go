package pluginhttp

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

// A package existing Homes are pinned to is refused before the trust dialog
// (preview) and again on a confirmed update, with a stable code and a link to
// the Home page where the reviewed upgrade happens.
func TestUpdateHandlerSendsAHomePinnedPackageToTheHomeUpgrade(t *testing.T) {
	h := testHandler(t)
	recorded := claudeBundle(t)
	if rr := postInstall(t, h, recorded, true); rr.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	mustWrite(t, filepath.Join(recorded, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.2.0"}`)
	guarded := 0
	h.Manager().SetReplacementGuard(func(plugin.InstalledPlugin, string, string) error {
		guarded++
		return &plugin.HomeUpgradeRequiredError{HomeName: "Music Production Home", HomePath: "/workspaces/music-production-home/assistant"}
	})
	for _, confirm := range []bool{false, true} {
		rr := postUpdate(t, h, confirm)
		var body struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("confirm=%v: undecodable body %q: %v", confirm, rr.Body.String(), err)
		}
		if rr.Code != http.StatusConflict || body.Code != HomeUpgradeRequiredCode || body.Message == "" ||
			body.Details["home_path"] != "/workspaces/music-production-home/assistant" || body.Details["home_name"] != "Music Production Home" {
			t.Fatalf("confirm=%v: %d %s", confirm, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), `"trust"`) {
			t.Fatalf("confirm=%v: a refused replacement was offered for trust: %s", confirm, rr.Body.String())
		}
	}
	if guarded != 2 || installedPlugin(t, h, "reaper").Version != "0.1.0" {
		t.Fatalf("guard calls = %d, installed = %+v", guarded, installedPlugin(t, h, "reaper"))
	}
}
